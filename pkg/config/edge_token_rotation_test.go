package config

import "testing"

// Rotating an edge token without a flag day (#1491).
//
// An edge token could previously only be changed all at once: edit central's config, restart it,
// re-provision every edge, with a window in which no edge could authenticate. That makes rotation
// something nobody does, which is why the token from initial provisioning is still the one in use.

func TestAcceptedTokenHashes(t *testing.T) {
	cases := []struct {
		name string
		node EdgeNodeConfig
		want []string
	}{
		{
			// The shape every existing deployment has. It must keep working untouched.
			name: "current token only",
			node: EdgeNodeConfig{ID: "edge-us", TokenHash: "aaa"},
			want: []string{"aaa"},
		},
		{
			// Mid-rotation: both authenticate, so edges can be rolled one at a time.
			name: "rotation in progress",
			node: EdgeNodeConfig{ID: "edge-us", TokenHash: "aaa", AdditionalTokenHashes: []string{"bbb"}},
			want: []string{"aaa", "bbb"},
		},
		{
			// An unconfigured node has TokenHash "". A caller presenting "" must not match it,
			// so empties are dropped rather than returned as an accepted value.
			name: "unconfigured node accepts nothing",
			node: EdgeNodeConfig{ID: "edge-broken"},
			want: nil,
		},
		{
			name: "empty additional entries are ignored",
			node: EdgeNodeConfig{ID: "edge-us", TokenHash: "aaa", AdditionalTokenHashes: []string{"", "ccc", ""}},
			want: []string{"aaa", "ccc"},
		},
		{
			// The end of a rotation: the old hash is removed and only the new one remains, which
			// is the step that actually revokes the old token.
			name: "old token removed",
			node: EdgeNodeConfig{ID: "edge-us", AdditionalTokenHashes: []string{"bbb"}},
			want: []string{"bbb"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.node.AcceptedTokenHashes()
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}
