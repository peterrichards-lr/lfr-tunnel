package regionvocab

import "testing"

// The rule decides which region name reaches region_probes, and therefore which name anything
// matching a stored region has to use. Getting it wrong is silent: the join simply matches
// nothing and every session is reported unverifiable (#1919).

func TestTheShorterNameWins(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		want  string
	}{
		// The case that was actually got wrong. "central" reads like the canonical name
		// and is what pkg/db assumed; the rule discards it because it is five characters
		// longer than "eu".
		{"central's own aliases", []string{"central", "eu"}, "eu"},
		{"order does not matter", []string{"eu", "central"}, "eu"},
		{"edge prefix loses", []string{"edge-in", "in"}, "in"},
		{"alphabetical breaks a tie", []string{"za", "ap"}, "ap"},
		{"single name", []string{"only"}, "only"},
		{"nothing", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Canonical(tc.names); got != tc.want {
				t.Errorf("Canonical(%v) = %q, want %q", tc.names, got, tc.want)
			}
		})
	}
}

func TestCentralRegionIsNotTheObviousName(t *testing.T) {
	// Stated as its own test because the obvious answer is wrong, and the next person to
	// read CentralAliases will assume "central" the way #1888 did.
	if got := CentralRegion(); got != "eu" {
		t.Errorf("central's sessions must be matched against %q, got %q", "eu", got)
	}
	if CentralRegion() == CentralNodeID {
		t.Error("the node id and the region name are different vocabularies; if they ever " +
			"match, the mapping this package exists for has become a no-op")
	}
}

func TestCentralAliasesAreBothDeclared(t *testing.T) {
	// The gateway builds its advertised regions from this list, so dropping one silently
	// stops advertising a name clients may already have cached.
	want := map[string]bool{"eu": true, "central": true}
	if len(CentralAliases) != len(want) {
		t.Fatalf("expected %d aliases, got %v", len(want), CentralAliases)
	}
	for _, a := range CentralAliases {
		if !want[a] {
			t.Errorf("unexpected alias %q -- adding one changes what the gateway advertises", a)
		}
	}
}
