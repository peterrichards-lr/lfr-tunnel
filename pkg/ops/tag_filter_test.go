package ops

import "testing"

// TestTagFilterIsOperatorSupplied covers the value that briefly carried one organisation's
// project name as a hardcoded constant in MIT, provider-neutral code. It is now supplied by
// the operator, and absent by default.
func TestTagFilterIsOperatorSupplied(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"key=value", "Project=my-tunnel", "Name=tag:Project,Values=my-tunnel"},
		{"whitespace tolerated", "  Project = my-tunnel  ", "Name=tag:Project,Values=my-tunnel"},
		{"any key, not just Project", "Environment=staging", "Name=tag:Environment,Values=staging"},
		{"value may contain dashes", "Project=lfr-tunnel", "Name=tag:Project,Values=lfr-tunnel"},

		// Unset is the default and must simply mean "do not filter by tag".
		{"empty", "", ""},
		{"whitespace only", "   ", ""},

		// Malformed input widens the search back to address-only, which is the no-tag
		// behaviour anyway -- so it is ignored rather than fatal.
		{"no separator", "Project", ""},
		{"missing value", "Project=", ""},
		{"missing key", "=my-tunnel", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tagFilter(tc.in); got != tc.want {
				t.Errorf("tagFilter(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
