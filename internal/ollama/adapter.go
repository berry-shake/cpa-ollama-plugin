package ollama

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/berry-shake/cpa-ollama-plugin/internal/web"
	"github.com/berry-shake/cpa-provider-core/config"
	"github.com/berry-shake/cpa-provider-core/credential"
	"github.com/berry-shake/cpa-provider-core/provider"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// The usage ranges an account query reads. The 7-day figures come out of
// the 30-day buckets (see RecentDays), so a query costs three requests:
// balance, 30 days (with the per-model breakdown) and 24 hours.
const (
	usageMonthQuery = EndpointUsage + "?range=30d&group_by=model"
	usageDayQuery   = EndpointUsage + "?range=24h"
)

// Adapter is the Ollama provider for cpa-provider-core.
type Adapter struct {
	catalog *Catalog
}

// New returns the Ollama adapter with an empty catalog.
func New() *Adapter {
	return &Adapter{catalog: NewCatalog()}
}

var _ provider.Adapter = (*Adapter)(nil)

// Spec describes the plugin.
func (a *Adapter) Spec() provider.Spec {
	return provider.Spec{
		ID:          ProviderID,
		DisplayName: "Ollama",
		Author:      "berry-shake",
		Repository:  "https://github.com/berry-shake/cpa-ollama-plugin",
		Config: config.Profile{
			BaseURL:           "https://ollama.com",
			ModelPrefix:       ProviderID,
			DataDir:           "plugins/ollama-data",
			Database:          "ollama.db",
			BaseURLSuffixes:   []string{"/v1", "/api"},
			BaseURLHelp:       "Ollama cloud origin (https://ollama.com); a trailing /v1 or /api is dropped. Auth files may override it with base_url.",
			ModelPrefixHelp:   `Default CPA prefix for Ollama keys ("ollama" -> ollama/gpt-oss:120b). Empty adds no prefix. An auth file's own prefix field wins.`,
			ModelsRefreshHelp: "How often to reload Ollama's cloud model list; 0 disables polling.",
		},
		Page: web.Page,
		Logo: web.Logo,
		Texts: provider.Texts{
			LoginStart:          "Ollama uses API keys: add one on the Ollama plugin page or upload an ollama auth file",
			ModelsRoute:         "Ollama cloud model list with capabilities, context length and thinking levels.",
			ModelsRefreshRoute:  "Reload the Ollama model catalog and re-register changed models.",
			AddCredentialRoute:  `Add an Ollama API key. Body: {"api_key","label","note","models","prefix","proxy_url"|"proxy_id"}.`,
			EditCredentialRoute: `Edit an Ollama credential. Body: {"auth_index","label","note","models","prefix","proxy_url"|"proxy_id","priority"}.`,
			QuotaRoute:          "Balance and 24h / 7d / 30d usage of the Ollama account behind one credential (?auth_index=).",
		},
	}
}

// Endpoint is where Ollama serves a protocol.
func (a *Adapter) Endpoint(protocol provider.Protocol) string {
	switch protocol {
	case provider.Responses:
		return EndpointResponses
	case provider.Messages:
		return EndpointMessages
	default:
		return EndpointChat
	}
}

// Catalog is Ollama's cloud model list.
func (a *Adapter) Catalog() provider.CatalogSource {
	return a.catalog
}

// VerifyKey checks a new key with one GET /api/balance.
func (a *Adapter) VerifyKey(ctx context.Context, env provider.Env, client *http.Client, cred credential.Credential) *pluginabi.Error {
	_, perr := env.Errors.GetJSON(ctx, client, env.BaseURL(cred)+EndpointBalance, cred.APIKey, env.UserAgent, EndpointBalance)
	return perr
}

// AccountKey caches account answers per key and origin.
func (a *Adapter) AccountKey(env provider.Env, cred credential.Credential) string {
	return credential.Fingerprint(cred.APIKey) + " " + env.BaseURL(cred)
}

// FetchAccount loads the balance and the 24h / 7d / 30d usage of the
// account behind a key. The calls run one after another in a single turn of
// the key's egress, each with its own timeout. A failure of one call is
// reported next to the others' results; the error is returned only when all
// of them fail. The answer is complete when all of them succeed.
func (a *Adapter) FetchAccount(env provider.Env, client *http.Client, cred credential.Credential) (any, bool, *pluginabi.Error) {
	baseURL := env.BaseURL(cred)
	get := func(path string) (map[string]any, *pluginabi.Error) {
		ctx, cancel := context.WithTimeout(env.Context, provider.AccountCallTimeout)
		defer cancel()
		return env.Errors.GetJSON(ctx, client, baseURL+path, cred.APIKey, env.UserAgent, path)
	}
	balanceRaw, balanceErr := get(EndpointBalance)
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

	info := AccountInfo{FetchedAt: env.Now().UTC().Format(time.RFC3339)}
	if balanceErr != nil {
		info.BalanceError = balanceErr.Message
	} else {
		info.Balance = BalanceFrom(balanceRaw)
	}
	usage := &UsageInfo{}
	var usageErrors []string
	if monthErr != nil {
		usageErrors = append(usageErrors, monthErr.Message)
	} else {
		usage.Month = UsageTotals(monthRaw)
		usage.Week = RecentDays(monthRaw, 7)
		usage.Models = ModelUsageFrom(monthRaw)
	}
	if dayErr != nil {
		if len(usageErrors) == 0 || usageErrors[0] != dayErr.Message {
			usageErrors = append(usageErrors, dayErr.Message)
		}
	} else {
		usage.Day = UsageTotals(dayRaw)
	}
	if usage.Day != nil || usage.Week != nil || usage.Month != nil {
		info.Usage = usage
	}
	info.UsageError = strings.Join(usageErrors, "; ")
	if balanceErr == nil && monthErr == nil {
		info.Account = AccountFingerprint(balanceRaw, monthRaw)
	}
	if balanceErr != nil && monthErr != nil && dayErr != nil {
		return info, false, balanceErr
	}
	return info, balanceErr == nil && monthErr == nil && dayErr == nil, nil
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

// Quota turns an account answer into CPA's quota panel response.
func (a *Adapter) Quota(info any) pluginapi.QuotaFetchResponse {
	account, _ := info.(AccountInfo)
	return QuotaFromAccount(account)
}
