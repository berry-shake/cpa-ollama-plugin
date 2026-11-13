package plugin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/berry-shake/cpa-ollama-plugin/internal/credential"
	"github.com/berry-shake/cpa-ollama-plugin/internal/ollama"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	accountCallTimeout = 20 * time.Second
	// accountCacheTTL is how long a complete account query is reused. Ollama
	// allows 10 balance and usage requests a minute per user, shared by all
	// of the user's keys, and recommends polling once a minute.
	accountCacheTTL = time.Minute
)

// The usage ranges an account query reads. The 7-day figures come out of
// the 30-day buckets (see ollama.RecentDays), so a query costs three requests:
// balance, 30 days (with the per-model breakdown) and 24 hours.
const (
	usageMonthQuery = ollama.EndpointUsage + "?range=30d&group_by=model"
	usageDayQuery   = ollama.EndpointUsage + "?range=24h"
)

type rpcQuotaFetchRequest struct {
	pluginapi.QuotaFetchRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// Ollama account queries (balance, usage and the check made when a key is
// saved) go out one at a time per egress: a query waits for the one ahead of
// it on the same proxy, or the same direct line, to finish. Keys on different
// egresses are queried in parallel.

// accountTurn resolves the egress for a credential's proxy and waits for
// its turn, giving up when ctx ends. release must be called when done.
func (s *Service) accountTurn(ctx context.Context, proxyURL string) (client *http.Client, release func(), perr *pluginabi.Error) {
	setting, errProxy := ollama.ResolveEgress(proxyURL, s.globalProxy())
	if errProxy != nil {
		return nil, nil, ollama.RequestError("invalid_proxy", errProxy.Error())
	}
	release, errWait := s.accountQueue.Acquire(ctx, setting)
	if errWait != nil {
		return nil, nil, ollama.TransportError(fmt.Errorf("waiting for the egress: %w", errWait))
	}
	return s.clients.Client(setting), release, nil
}

// accountGET checks one account endpoint with a credential, in its egress's turn.
func (s *Service) accountGET(ctx context.Context, cred credential.Credential, path string) (map[string]any, *pluginabi.Error) {
	client, release, perr := s.accountTurn(ctx, cred.ProxyURL)
	if perr != nil {
		return nil, perr
	}
	defer release()
	return ollama.GetJSON(ctx, client, cred.EffectiveBaseURL(s.config())+path, cred.APIKey, s.userAgent(), path)
}

// fetchAccount loads the balance and the 24h / 7d / 30d usage of the
// account behind a key. The calls run one after another in a single turn of
// the key's egress, each with its own timeout; time spent waiting for the
// turn does not count. A failure of one call is reported next to the others'
// results; the error is returned only when all of them fail. A complete
// answer is reused for accountCacheTTL.
func (s *Service) fetchAccount(cred credential.Credential) (ollama.AccountInfo, *pluginabi.Error) {
	baseURL := cred.EffectiveBaseURL(s.config())
	cacheKey := credential.Fingerprint(cred.APIKey) + " " + baseURL
	if info, ok := s.accounts.get(cacheKey, s.now()); ok {
		return info, nil
	}
	client, release, perr := s.accountTurn(s.ctx, cred.ProxyURL)
	if perr != nil {
		return ollama.AccountInfo{FetchedAt: s.now().UTC().Format(time.RFC3339)}, perr
	}
	defer release()
	// The query ahead in the queue may have been for the same key.
	if info, ok := s.accounts.get(cacheKey, s.now()); ok {
		return info, nil
	}
	get := func(path string) (map[string]any, *pluginabi.Error) {
		ctx, cancel := context.WithTimeout(s.ctx, accountCallTimeout)
		defer cancel()
		return ollama.GetJSON(ctx, client, baseURL+path, cred.APIKey, s.userAgent(), path)
	}
	balanceRaw, balanceErr := get(ollama.EndpointBalance)
	var (
		monthRaw, dayRaw map[string]any
		monthErr, dayErr *pluginabi.Error
	)
	if balanceErr != nil && skipsUsage(balanceErr) {
		// A rejected key, a suspended account or a spent rate limit fails the
		// usage calls the same way; do not spend more requests on them.
		monthErr, dayErr = balanceErr, balanceErr
	} else {
		monthRaw, monthErr = get(usageMonthQuery)
		dayRaw, dayErr = get(usageDayQuery)
	}

	info := ollama.AccountInfo{FetchedAt: s.now().UTC().Format(time.RFC3339)}
	if balanceErr != nil {
		info.BalanceError = balanceErr.Message
	} else {
		info.Balance = ollama.BalanceFrom(balanceRaw)
	}
	usage := &ollama.UsageInfo{}
	var usageErrors []string
	if monthErr != nil {
		usageErrors = append(usageErrors, monthErr.Message)
	} else {
		usage.Month = ollama.UsageTotals(monthRaw)
		usage.Week = ollama.RecentDays(monthRaw, 7)
		usage.Models = ollama.ModelUsageFrom(monthRaw)
	}
	if dayErr != nil {
		if len(usageErrors) == 0 || usageErrors[0] != dayErr.Message {
			usageErrors = append(usageErrors, dayErr.Message)
		}
	} else {
		usage.Day = ollama.UsageTotals(dayRaw)
	}
	if usage.Day != nil || usage.Week != nil || usage.Month != nil {
		info.Usage = usage
	}
	info.UsageError = strings.Join(usageErrors, "; ")
	if balanceErr == nil && monthErr == nil {
		info.Account = ollama.AccountFingerprint(balanceRaw, monthRaw)
	}
	switch {
	case balanceErr != nil && monthErr != nil && dayErr != nil:
		return info, balanceErr
	case balanceErr == nil && monthErr == nil && dayErr == nil:
		s.accounts.put(cacheKey, info, s.now())
	}
	return info, nil
}

// skipsUsage reports a balance error the usage endpoint would answer the
// same way: 401 (invalid key), 403 (account suspended) or 429 (the rate
// limit both endpoints share).
func skipsUsage(perr *pluginabi.Error) bool {
	switch perr.HTTPStatus {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	}
	return false
}

// quotaFetch does not call rememberHost: CPA sends quota requests without the
// host summary, and an empty one would wipe the cached global proxy.
func (s *Service) quotaFetch(req pluginapi.QuotaFetchRequest) []byte {
	cred, _, errCred := credential.FromMaps(req.StorageJSON, req.Metadata)
	if errCred != nil || cred.APIKey == "" {
		// Fall back to the auth file on disk when CPA sends no storage.
		if fileCred, ok := s.credentialByAuthIndex(req.AuthIndex); ok {
			cred = fileCred
		}
	}
	if cred.APIKey == "" {
		return errorEnvelopeFrom(ollama.AuthError("ollama: credential has no api_key"))
	}
	info, perr := s.fetchAccount(cred)
	if perr != nil {
		return errorEnvelopeFrom(perr)
	}
	return okEnvelope(ollama.QuotaFromAccount(info))
}

// accountCache keeps complete account answers per key and origin.
type accountCache struct {
	mu      sync.Mutex
	entries map[string]accountCacheEntry
}

type accountCacheEntry struct {
	info ollama.AccountInfo
	at   time.Time
}

func (c *accountCache) get(key string, now time.Time) (ollama.AccountInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || now.Before(entry.at) || now.Sub(entry.at) >= accountCacheTTL {
		return ollama.AccountInfo{}, false
	}
	return entry.info, true
}

func (c *accountCache) put(key string, info ollama.AccountInfo, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]accountCacheEntry)
	}
	for other, entry := range c.entries {
		if now.Sub(entry.at) >= accountCacheTTL {
			delete(c.entries, other)
		}
	}
	c.entries[key] = accountCacheEntry{info: info, at: now}
}
