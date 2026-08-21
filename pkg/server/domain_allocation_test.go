package server

import "testing"

// TestTopRankedDomain is the regression test for #1153. getActiveDomainsForRequest ranks
// candidate domains; registration must use the top one rather than every entry. Consuming
// the whole list gave a user who never asked for a domain a lease -- and an
// auto-reservation -- on all of them.
func TestTopRankedDomain(t *testing.T) {
	cases := []struct {
		name       string
		candidates []string
		want       int
	}{
		{"ranked list narrows to the top candidate", []string{"lfr-demo.se", "lfr-demo.online"}, 1},
		{"a single candidate is unchanged", []string{"lfr-demo.se"}, 1},
		{"no candidates stays empty", nil, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := topRankedDomain(tc.candidates)
			if len(got) != tc.want {
				t.Fatalf("expected %d domain(s), got %d (%v)", tc.want, len(got), got)
			}
			if tc.want > 0 && got[0] != tc.candidates[0] {
				t.Errorf("expected the highest-ranked candidate %q, got %q -- the ordering the rule computed is the whole point", tc.candidates[0], got[0])
			}
		})
	}
}

// TestTopRankedDomainDoesNotMutateCaller guards the slicing: the caller's slice must not
// be re-ordered or truncated underneath it.
func TestTopRankedDomainDoesNotMutateCaller(t *testing.T) {
	candidates := []string{"first.example", "second.example", "third.example"}
	_ = topRankedDomain(candidates)

	if len(candidates) != 3 {
		t.Fatalf("caller's slice was truncated, now %v", candidates)
	}
	if candidates[0] != "first.example" || candidates[2] != "third.example" {
		t.Errorf("caller's slice was re-ordered: %v", candidates)
	}
}
