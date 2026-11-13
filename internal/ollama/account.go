package ollama

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/berry-shake/cpa-ollama-plugin/internal/jsonfield"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// AccountInfo is the balance and usage of the Ollama account behind one key.
// Both are account-wide: every key of an account (and every member of a
// team) reports the same figures.
type AccountInfo struct {
	// Account identifies the figures, so the management page can add up
	// accounts without counting one account once per key. Ollama reports no
	// account ID; keys whose balance and 30-day usage read the same are
	// taken as one account. Empty when either query failed.
	Account      string       `json:"account,omitempty"`
	Balance      *BalanceInfo `json:"balance,omitempty"`
	BalanceError string       `json:"balance_error,omitempty"`
	Usage        *UsageInfo   `json:"usage,omitempty"`
	UsageError   string       `json:"usage_error,omitempty"`
	FetchedAt    string       `json:"fetched_at"`
}

// BalanceInfo is the remaining credit from GET /api/balance.
type BalanceInfo struct {
	// Included is the plan's credit for the current period; nil for legacy
	// plans, which report Session and Weekly limits instead.
	Included *IncludedCredits `json:"included,omitempty"`
	Session  *PlanLimit       `json:"session,omitempty"`
	Weekly   *PlanLimit       `json:"weekly,omitempty"`
	// PurchasedUSD is the unexpired purchased credit.
	PurchasedUSD float64 `json:"purchased_usd"`
}

// IncludedCredits is the plan credit of the current period. The period
// follows the plan's monthly reset; from is inclusive, until exclusive.
type IncludedCredits struct {
	BalanceUSD   float64 `json:"balance_usd"`
	AllowanceUSD float64 `json:"allowance_usd"`
	PeriodFrom   string  `json:"period_from,omitempty"`
	PeriodUntil  string  `json:"period_until,omitempty"`
}

// PlanLimit is a legacy plan's session or weekly limit.
type PlanLimit struct {
	// RemainingPercent ranges from 0 to 100.
	RemainingPercent float64 `json:"remaining_percent"`
	ResetsAt         string  `json:"resets_at,omitempty"`
}

// UsageWindow sums the usage of one time range.
type UsageWindow struct {
	Requests int64 `json:"requests"`
	// USD is the value of the requests, whether the plan covered them or
	// purchased credits paid for them; nil when Ollama reports only request
	// counts (ranges with legacy-plan requests).
	USD *float64 `json:"usd,omitempty"`
	// InputTokens includes CachedTokens.
	InputTokens  int64 `json:"input_tokens"`
	CachedTokens int64 `json:"cached_input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// ModelUsage is one model's share of the 30-day usage. Model is empty for
// the remainder, the models outside Ollama's top ten.
type ModelUsage struct {
	Model    string   `json:"model"`
	Requests int64    `json:"requests"`
	USD      *float64 `json:"usd,omitempty"`
}

// UsageInfo is the account usage from GET /api/usage. Day is the 24h range
// (24 complete UTC hours plus the current one), Week and Month the 7d and 30d
// ranges (complete UTC days plus today so far).
type UsageInfo struct {
	Day   *UsageWindow `json:"day,omitempty"`
	Week  *UsageWindow `json:"week,omitempty"`
	Month *UsageWindow `json:"month,omitempty"`
	// Models breaks the 30-day usage down by model, largest first.
	Models []ModelUsage `json:"models,omitempty"`
}

// GetJSON GETs a JSON object from Ollama with a bearer token. path names the
// endpoint in errors.
func GetJSON(ctx context.Context, client *http.Client, target, token, userAgent, path string) (map[string]any, *pluginabi.Error) {
	req, errReq := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if errReq != nil {
		return nil, TransportError(errReq)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, errDo := client.Do(req)
	if errDo != nil {
		return nil, TransportError(errDo)
	}
	defer func() { _ = resp.Body.Close() }()
	body, errRead := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if errRead != nil {
		return nil, TransportError(errRead)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, UpstreamError(resp.StatusCode, resp.Header, body, path)
	}
	var out map[string]any
	if errUnmarshal := json.Unmarshal(body, &out); errUnmarshal != nil {
		return nil, &pluginabi.Error{Code: "invalid_upstream_response", Message: "ollama: decode " + path + ": " + errUnmarshal.Error(), HTTPStatus: http.StatusBadGateway}
	}
	return out, nil
}

// BalanceFrom reads a GET /api/balance response:
// {"included":{"balance_usd","allowance_usd","period":{"from","until"}} or
// {"session":{...},"weekly":{...}},"purchased":{"balance_usd"}}.
func BalanceFrom(raw map[string]any) *BalanceInfo {
	info := &BalanceInfo{}
	if included, ok := raw["included"].(map[string]any); ok {
		_, hasBalance := included["balance_usd"]
		_, hasAllowance := included["allowance_usd"]
		if hasBalance || hasAllowance {
			credits := &IncludedCredits{
				BalanceUSD:   numberField(included, "balance_usd"),
				AllowanceUSD: numberField(included, "allowance_usd"),
			}
			if period, okPeriod := included["period"].(map[string]any); okPeriod {
				credits.PeriodFrom = jsonfield.String(period, "from")
				credits.PeriodUntil = jsonfield.String(period, "until")
			}
			info.Included = credits
		}
		info.Session = planLimitFrom(included["session"])
		info.Weekly = planLimitFrom(included["weekly"])
	}
	if purchased, ok := raw["purchased"].(map[string]any); ok {
		info.PurchasedUSD = numberField(purchased, "balance_usd")
	}
	return info
}

func planLimitFrom(raw any) *PlanLimit {
	fields, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return &PlanLimit{
		RemainingPercent: math.Max(0, math.Min(100, numberField(fields, "remaining_percent"))),
		ResetsAt:         jsonfield.String(fields, "resets_at"),
	}
}

// UsageTotals reads the totals of a GET /api/usage response; nil when the
// response has none.
func UsageTotals(raw map[string]any) *UsageWindow {
	totals, ok := raw["totals"].(map[string]any)
	if !ok {
		return nil
	}
	window := usageMetricsFrom(totals)
	return &window
}

// RecentDays sums the buckets of a daily GET /api/usage response that start
// at most days before today 00:00 UTC, "today" being the day the response
// ends in. RecentDays(month, 7) is what the 7d range reports, read from the
// 30d buckets so the account's rate limit is spent on one request less.
// It returns nil when the response has no usable range.
func RecentDays(raw map[string]any, days int) *UsageWindow {
	until, errUntil := time.Parse(time.RFC3339, jsonfield.String(raw, "until"))
	buckets, ok := raw["buckets"].([]any)
	if errUntil != nil || !ok {
		return nil
	}
	start := until.UTC().Truncate(24*time.Hour).AddDate(0, 0, -days)
	window := &UsageWindow{}
	if totals := UsageTotals(raw); totals != nil && totals.USD != nil {
		// The range reports cost; a window without requests costs $0.
		zero := 0.0
		window.USD = &zero
	}
	for _, item := range buckets {
		bucket, okBucket := item.(map[string]any)
		if !okBucket {
			continue
		}
		from, errFrom := time.Parse(time.RFC3339, jsonfield.String(bucket, "from"))
		if errFrom != nil || from.Before(start) {
			continue
		}
		window.add(usageMetricsFrom(bucket))
	}
	return window
}

// ModelUsageFrom reads the per-model totals of a GET /api/usage?group_by=model
// response: up to ten models, then the remainder under an empty name.
func ModelUsageFrom(raw map[string]any) []ModelUsage {
	totals, _ := raw["totals"].(map[string]any)
	groups, _ := totals["groups"].([]any)
	var out []ModelUsage
	for _, item := range groups {
		group, ok := item.(map[string]any)
		if !ok {
			continue
		}
		model := jsonfield.String(group, "model")
		if model == "" {
			continue
		}
		metrics := usageMetricsFrom(group)
		out = append(out, ModelUsage{Model: model, Requests: metrics.Requests, USD: metrics.USD})
	}
	if remainder, ok := totals["remainder"].(map[string]any); ok {
		metrics := usageMetricsFrom(remainder)
		if metrics.Requests > 0 {
			out = append(out, ModelUsage{Requests: metrics.Requests, USD: metrics.USD})
		}
	}
	return out
}

// usageMetricsFrom reads the metrics shared by totals, buckets and groups.
func usageMetricsFrom(fields map[string]any) UsageWindow {
	window := UsageWindow{
		Requests:     int64(numberField(fields, "request_count")),
		InputTokens:  int64(numberField(fields, "input_tokens")),
		CachedTokens: int64(numberField(fields, "cached_input_tokens")),
		OutputTokens: int64(numberField(fields, "output_tokens")),
	}
	if usd, ok := optionalNumber(fields["usage_usd"]); ok {
		window.USD = &usd
	}
	return window
}

func (w *UsageWindow) add(other UsageWindow) {
	w.Requests += other.Requests
	w.InputTokens += other.InputTokens
	w.CachedTokens += other.CachedTokens
	w.OutputTokens += other.OutputTokens
	if other.USD != nil {
		sum := *other.USD
		if w.USD != nil {
			sum += *w.USD
		}
		w.USD = &sum
	}
}

// AccountFingerprint names the account behind a balance and a 30-day usage
// response. Ollama reports no account ID, but every key of an account reads
// the same figures at the same moment, so identical figures are taken as one
// account. The range end (the time of the request) is left out.
func AccountFingerprint(balance, month map[string]any) string {
	canonical, errMarshal := json.Marshal([]any{balance, month["from"], month["totals"]})
	if errMarshal != nil {
		return ""
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])[:16]
}

// QuotaFromAccount renders account info for CPA's quota panel: a bar for the
// plan credit left this period (or the legacy session and weekly limits),
// then the purchased credit and the 24h / 7d / 30d usage as figures. Usage is
// shown in USD, or as a request count when Ollama reports no cost.
func QuotaFromAccount(info AccountInfo) pluginapi.QuotaFetchResponse {
	var out pluginapi.QuotaFetchResponse
	if balance := info.Balance; balance != nil {
		if included := balance.Included; included != nil {
			fraction := 0.0
			if included.AllowanceUSD > 0 {
				fraction = math.Max(0, math.Min(1, included.BalanceUSD/included.AllowanceUSD))
			}
			out.Groups = append(out.Groups, pluginapi.QuotaGroup{
				DisplayName: "Included credits",
				Buckets: []pluginapi.QuotaBucket{{
					Window:            "plan period",
					RemainingFraction: fraction,
					ResetTime:         included.PeriodUntil,
					Description:       fmt.Sprintf("$%.2f of $%.2f left", included.BalanceUSD, included.AllowanceUSD),
				}},
			})
		}
		var limits []pluginapi.QuotaBucket
		for _, limit := range []struct {
			window string
			value  *PlanLimit
		}{{"session", balance.Session}, {"weekly", balance.Weekly}} {
			if limit.value == nil {
				continue
			}
			limits = append(limits, pluginapi.QuotaBucket{
				Window:            limit.window,
				RemainingFraction: limit.value.RemainingPercent / 100,
				ResetTime:         limit.value.ResetsAt,
				Description:       strconv.FormatFloat(limit.value.RemainingPercent, 'f', -1, 64) + "% left",
			})
		}
		if len(limits) > 0 {
			out.Groups = append(out.Groups, pluginapi.QuotaGroup{DisplayName: "Plan limits", Buckets: limits})
		}
		out.Summary = append(out.Summary, pluginapi.QuotaMetric{Key: "purchased", Label: "Purchased credits", Value: balance.PurchasedUSD, Format: "currency", Currency: "USD"})
	}
	if usage := info.Usage; usage != nil {
		for _, window := range []struct {
			key, label string
			value      *UsageWindow
		}{
			{"usage_24h", "24h", usage.Day},
			{"usage_7d", "7d", usage.Week},
			{"usage_30d", "30d", usage.Month},
		} {
			switch {
			case window.value == nil:
			case window.value.USD != nil:
				out.Summary = append(out.Summary, pluginapi.QuotaMetric{Key: window.key, Label: "Usage " + window.label, Value: *window.value.USD, Format: "currency", Currency: "USD"})
			default:
				out.Summary = append(out.Summary, pluginapi.QuotaMetric{Key: window.key + "_requests", Label: "Requests " + window.label, Value: float64(window.value.Requests), Format: "number"})
			}
		}
	}
	return out
}

func numberField(fields map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if value, ok := optionalNumber(fields[key]); ok {
			return value
		}
	}
	return 0
}

// optionalNumber accepts JSON numbers and decimal strings; null is absent.
func optionalNumber(raw any) (float64, bool) {
	switch value := raw.(type) {
	case float64:
		return value, true
	case string:
		parsed, errParse := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if errParse != nil {
			return 0, false
		}
		return parsed, true
	case json.Number:
		parsed, errParse := value.Float64()
		return parsed, errParse == nil
	default:
		return 0, false
	}
}
