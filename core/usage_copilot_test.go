package core

import (
	"testing"
	"time"
)

// The fixture is the shape GitHub actually answers with on a plan that grants
// chat and completions but no premium interactions.
const copilotFreePlanPayload = `{
  "login": "someone",
  "copilot_plan": "individual",
  "quota_reset_date": "2026-10-01",
  "quota_snapshots": {
    "chat": {"percent_remaining": 62.5, "has_quota": true, "unlimited": false, "entitlement": 200},
    "completions": {"percent_remaining": 100.0, "has_quota": true, "unlimited": false, "entitlement": 2000},
    "premium_interactions": {"percent_remaining": 0.0, "has_quota": false, "unlimited": false, "entitlement": 0}
  }
}`

func TestCopilotUsageReportsGrantedQuotasAsUsedPercent(t *testing.T) {
	usage, err := parseCopilotUsage([]byte(copilotFreePlanPayload))
	if err != nil {
		t.Fatalf("parseCopilotUsage() error = %v", err)
	}
	if len(usage.Windows) != 2 {
		t.Fatalf("windows = %#v, want chat and completions only", usage.Windows)
	}
	if usage.Windows[0].Label != "Chat" || usage.Windows[0].Percent != 37.5 {
		t.Fatalf("chat window = %#v, want 37.5%% used", usage.Windows[0])
	}
	if usage.Windows[1].Label != "Code" || usage.Windows[1].Percent != 0 {
		t.Fatalf("completions window = %#v, want an untouched quota", usage.Windows[1])
	}
	reset := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)
	if !usage.Windows[0].Reset.Equal(reset) {
		t.Fatalf("reset = %v, want the monthly reset date %v", usage.Windows[0].Reset, reset)
	}
}

// A quota the plan does not grant must not become a full bar. Reading
// percent_remaining without checking has_quota would show exactly that.
func TestCopilotUsageOmitsQuotasThePlanDoesNotGrant(t *testing.T) {
	usage, err := parseCopilotUsage([]byte(copilotFreePlanPayload))
	if err != nil {
		t.Fatal(err)
	}
	for _, window := range usage.Windows {
		if window.Label == "Premium" {
			t.Fatalf("ungranted quota rendered as a bar: %#v", window)
		}
	}
}

func TestCopilotUsagePrefersPremiumWhenThePlanGrantsIt(t *testing.T) {
	payload := `{
      "quota_reset_date": "2026-10-01",
      "quota_snapshots": {
        "chat": {"percent_remaining": 90.0, "has_quota": true, "entitlement": 300},
        "premium_interactions": {"percent_remaining": 25.0, "has_quota": true, "entitlement": 1500}
      }
    }`
	usage, err := parseCopilotUsage([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if len(usage.Windows) != 2 || usage.Windows[0].Label != "Premium" {
		t.Fatalf("windows = %#v, want Premium first", usage.Windows)
	}
	if usage.Windows[0].Percent != 75 {
		t.Fatalf("premium percent = %v, want 75 used", usage.Windows[0].Percent)
	}
}

// An unlimited quota has no fill level, so it must not be drawn as one.
func TestCopilotUsageSkipsUnlimitedQuotas(t *testing.T) {
	payload := `{
      "quota_snapshots": {
        "chat": {"percent_remaining": 100.0, "has_quota": true, "unlimited": true, "entitlement": 0}
      }
    }`
	if _, err := parseCopilotUsage([]byte(payload)); err == nil {
		t.Fatal("unlimited quota produced a bar instead of an explicit absence")
	}
}

// Nothing readable must stay an error rather than an empty window list, which
// the Overview would otherwise render as a card promising limits it lacks.
func TestCopilotUsageKeepsUnreadablePayloadsExplicit(t *testing.T) {
	for name, payload := range map[string]string{
		"kaputt":       `{"quota_snapshots":`,
		"ohne Quoten":  `{"login":"someone"}`,
		"leere Quoten": `{"quota_snapshots":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			usage, err := parseCopilotUsage([]byte(payload))
			if err == nil {
				t.Fatalf("payload accepted, windows = %#v", usage.Windows)
			}
		})
	}
}

func TestCopilotResetDateStaysAbsentWhenUnreadable(t *testing.T) {
	for _, value := range []string{"", "irgendwann", "2026-13-45"} {
		if got := parseCopilotResetDate(value); !got.IsZero() {
			t.Fatalf("parseCopilotResetDate(%q) = %v, want no invented deadline", value, got)
		}
	}
}
