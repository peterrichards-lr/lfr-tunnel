package config

import (
	"fmt"
	"strings"
)

// Version is the current version of lfr-tunnel
// This is overridden by ldflags during the build process
var Version = "v1.48.51"

// CompareVersions returns -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2.
//
// It lives here, beside the version constant, rather than in pkg/client where it was born,
// because BOTH ends of the min_version policy now order versions (#1988): the client decides
// whether to warn about itself, and the gateway decides whether to refuse a registration. Two
// implementations of "older than" would be free to disagree, and the failure that produces --
// a client that believes it is acceptable and a gateway that does not, or the reverse -- is
// exactly the one a version floor exists to prevent.
//
// pkg/config is the lowest package both already import, so this needs no new dependency in
// either direction. client.CompareVersions remains as a thin forwarder: it is the name
// cmd/lfr-tunnel and pkg/client/interceptor.go already call.
//
// Leading "v" is optional on either side, missing components read as zero ("v1.2" == "v1.2.0"),
// and an unparseable component reads as zero rather than failing -- a version string the
// gateway cannot parse must not be able to lock everybody out.
func CompareVersions(v1, v2 string) int {
	v1 = strings.TrimPrefix(v1, "v")
	v2 = strings.TrimPrefix(v2, "v")

	p1 := strings.Split(v1, ".")
	p2 := strings.Split(v2, ".")

	for i := 0; i < len(p1) || i < len(p2); i++ {
		var n1, n2 int
		if i < len(p1) {
			fmt.Sscanf(p1[i], "%d", &n1) //nolint:errcheck
		}
		if i < len(p2) {
			fmt.Sscanf(p2[i], "%d", &n2) //nolint:errcheck
		}
		if n1 < n2 {
			return -1
		} else if n1 > n2 {
			return 1
		}
	}
	return 0
}
