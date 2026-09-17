package config

import "testing"

// CompareVersions is now the ONE ordering both the client and the gateway use to decide
// whether a client is below the floor (#1988). If the two ever disagreed, a client would
// believe itself acceptable while the gateway refused it, or the reverse.
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		v1, v2 string
		want   int
	}{
		{"v1.48.0", "v1.48.0", 0},
		{"1.48.0", "v1.48.0", 0},
		{"v1.40.0", "v1.45.0", -1},
		{"v1.45.0", "v1.40.0", 1},
		// Ten sorts above nine, which string comparison gets wrong.
		{"v1.9.0", "v1.10.0", -1},
		{"v1.48.35", "v1.48.5", 1},
		// Missing components read as zero.
		{"v1.48", "v1.48.0", 0},
		{"v1.48", "v1.48.1", -1},
		// An unparseable component reads as zero rather than failing: a version string the
		// gateway cannot parse must not be able to lock everybody out.
		{"dev", "v1.45.0", -1},
		// An empty floor sorts BELOW every real version, so an unset min_client_version can
		// refuse nobody. Asserted rather than assumed: the opposite would make an empty
		// config key lock out the whole fleet.
		{"v1.0.0", "", 1},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.v1, tc.v2); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.v1, tc.v2, got, tc.want)
		}
	}
}
