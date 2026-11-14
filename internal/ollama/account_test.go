package ollama

import "testing"

// CPA's panel labels a bar with its description when there is one, so the
// buckets carry only the window name: the plan's included credits are
// monthly, a legacy plan's limits are session and 7d.
func TestQuotaFromAccountNamesWindows(t *testing.T) {
	out := QuotaFromAccount(AccountInfo{Balance: &BalanceInfo{
		Included: &IncludedCredits{BalanceUSD: 15, AllowanceUSD: 60, PeriodUntil: "2026-11-25T07:20:34Z"},
	}})
	if len(out.Groups) != 1 || len(out.Groups[0].Buckets) != 1 {
		t.Fatalf("groups = %+v", out.Groups)
	}
	if b := out.Groups[0].Buckets[0]; b.Window != "monthly" || b.RemainingFraction != 0.25 || b.Description != "" || b.ResetTime != "2026-11-25T07:20:34Z" {
		t.Fatalf("included bucket = %+v", b)
	}

	legacy := QuotaFromAccount(AccountInfo{Balance: &BalanceInfo{
		Session: &PlanLimit{RemainingPercent: 80, ResetsAt: "2026-11-14T05:00:00Z"},
		Weekly:  &PlanLimit{RemainingPercent: 50, ResetsAt: "2026-11-20T00:00:00Z"},
	}})
	if len(legacy.Groups) != 1 || len(legacy.Groups[0].Buckets) != 2 {
		t.Fatalf("legacy groups = %+v", legacy.Groups)
	}
	for i, want := range []string{"session", "7d"} {
		if b := legacy.Groups[0].Buckets[i]; b.Window != want || b.Description != "" {
			t.Errorf("legacy bucket %d = %+v, want window %s and no description", i, b, want)
		}
	}
}
