package ollama

import (
	"encoding/json"
	"math"
	"testing"
)

// CPA's panel shows one Ollama group of bars and no figures. It labels a bar
// with its description when there is one, so the buckets carry only the
// window name: the plan's included credits are monthly, a legacy plan's
// limits are session and 7d.
func TestQuotaFromAccountNamesWindows(t *testing.T) {
	usd := 42.88
	out := QuotaFromAccount(AccountInfo{
		Balance: &BalanceInfo{
			Included:     &IncludedCredits{BalanceUSD: 15, AllowanceUSD: 60, PeriodUntil: "2026-11-25T07:20:34Z"},
			PurchasedUSD: 5,
		},
		Usage: &UsageInfo{Month: &UsageWindow{USD: &usd}},
	})
	if len(out.Groups) != 1 || out.Groups[0].DisplayName != "Ollama" || len(out.Groups[0].Buckets) != 1 {
		t.Fatalf("groups = %+v", out.Groups)
	}
	if len(out.Summary) != 0 || out.Subscription != nil {
		t.Fatalf("the panel shows bars only: summary %+v subscription %+v", out.Summary, out.Subscription)
	}
	if b := out.Groups[0].Buckets[0]; b.Window != "monthly" || b.RemainingFraction != 0.25 || b.Description != "" || b.ResetTime != "2026-11-25T07:20:34Z" {
		t.Fatalf("included bucket = %+v", b)
	}

	legacy := QuotaFromAccount(AccountInfo{Balance: &BalanceInfo{
		Session: &PlanLimit{RemainingPercent: 80, ResetsAt: "2026-11-14T05:00:00Z"},
		Weekly:  &PlanLimit{RemainingPercent: 50, ResetsAt: "2026-11-20T00:00:00Z"},
	}})
	if len(legacy.Groups) != 1 || legacy.Groups[0].DisplayName != "Ollama" || len(legacy.Groups[0].Buckets) != 2 {
		t.Fatalf("legacy groups = %+v", legacy.Groups)
	}
	for i, want := range []string{"session", "7d"} {
		if b := legacy.Groups[0].Buckets[i]; b.Window != want || b.Description != "" {
			t.Errorf("legacy bucket %d = %+v, want window %s and no description", i, b, want)
		}
	}
}

// "NaN" and "Infinity" parse as floats but are no amount, and JSON cannot
// carry them to the page or CPA's quota panel.
func TestOptionalNumberRejectsNonFinite(t *testing.T) {
	for _, in := range []any{"NaN", "Infinity", "-Inf", math.Inf(1), math.NaN()} {
		if got, ok := optionalNumber(in); ok || got != 0 {
			t.Errorf("optionalNumber(%v) = %v, %v; want 0, false", in, got, ok)
		}
	}
	for in, want := range map[any]float64{"12.5": 12.5, 3.0: 3, json.Number("7"): 7} {
		if got, ok := optionalNumber(in); !ok || got != want {
			t.Errorf("optionalNumber(%v) = %v, %v; want %v", in, got, ok, want)
		}
	}
}
