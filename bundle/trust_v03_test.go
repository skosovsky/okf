package bundle

import (
	"strings"
	"testing"
)

func TestTrustStateV03(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		generated   string
		events      string
		wantTier    TrustTier
		wantEvents  string
		wantReasons string
	}{
		{"no generation", "", "{by: 'human:ada', at: 2026-11-18}", TrustHumanReviewed, "1", "no_generation"},
		{"empty generation", "generated: []", "{by: 'human:ada', at: 2026-11-18}", TrustHumanReviewed, "1", "no_generation"},
		{"missing generation actor", "generated: {at: 2026-11-19}", "{by: 'human:ada', at: 2026-11-18}", TrustHumanReviewed, "1", "no_generation"},
		{"empty generation actor", "generated: {by: null, at: 2026-11-19}", "{by: 'human:ada', at: 2026-11-18}", TrustHumanReviewed, "1", "no_generation"},
		{"incomparable generation", "generated: {by: 'human:ada'}", "{by: 'human:ada', at: 2026-11-18}", TrustUnverified, "0", "generation_time_unavailable"},
		{"historical review", "generated: {by: 'process:writer', at: 2026-11-18T10:00:00Z}", "{by: 'human:ada', at: 2026-11-18}", TrustUnverified, "0", "before_generation"},
		{"current process and historical review", "generated: {by: 'process:writer', at: 2026-11-18T10:00:00Z}", "[{by: 'human:ada', at: 2026-11-18}, {by: 'team:ops', at: '2026-11-18T12:00:00+02:00'}]", TrustMachineConfirmed, "01", "before_generation at_or_after_generation"},
		{"current human and later process", "generated: {by: 'process:writer', at: 2026-11-18}", "[{by: 'human:ada', at: '2026-11-18t00:00:00z'}, {by: 'qa.bot:ops', at: 2026-11-19}]", TrustHumanReviewed, "11", "at_or_after_generation at_or_after_generation"},
		{"missing offset or actor", "", "[{by: 'human:ada', at: '2026-11-18T10:00:00'}, {at: 2026-11-18}]", TrustUnverified, "00", "invalid_verification invalid_verification"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			document, err := ParseDocument("---\n" + tt.generated + "\nverified: " + tt.events + "\n---\n")
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			state, err := document.Frontmatter.TrustStateV03Context(t.Context())

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			var current strings.Builder
			var reasons []string
			for _, event := range state.Verifications {
				reasons = append(reasons, event.Reason)
				if event.Current {
					current.WriteByte('1')
				} else {
					current.WriteByte('0')
				}
			}
			if state.Tier != tt.wantTier || current.String() != tt.wantEvents {
				t.Fatalf("tier and current events = %s, %s; want %s, %s", state.Tier, current.String(), tt.wantTier, tt.wantEvents)
			}
			if strings.Join(reasons, " ") != tt.wantReasons {
				t.Fatalf("verification reasons = %v; want %s", reasons, tt.wantReasons)
			}
		})
	}
}
