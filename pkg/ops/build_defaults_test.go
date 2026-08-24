package ops

import (
	"strings"
	"testing"
)

// TestFormatDefault covers the reporting added for #1256. An empty deployment default is
// invisible in the finished binary, which is how v1.48.0 shipped with no default gateway
// and nobody noticed until someone downloaded the client. The build has to say which mode
// it is in, and say it accurately: each default means something different when unset, so a
// shared message would tell users something untrue about two of the three.
func TestFormatDefault(t *testing.T) {
	cases := []struct {
		name      string
		value     string
		whenEmpty string
		want      string
	}{
		{
			name:      "DefaultServerURL",
			value:     "https://tunnel.example.com",
			whenEmpty: "clients will ask to be pointed at a gateway",
			want:      "  DefaultServerURL: https://tunnel.example.com",
		},
		{
			name:      "DefaultServerURL",
			value:     "",
			whenEmpty: "clients will ask to be pointed at a gateway",
			want:      "  DefaultServerURL: (unset -- clients will ask to be pointed at a gateway)",
		},
		{
			name:      "DefaultPortalURL",
			value:     "",
			whenEmpty: "browser login falls back to the gateway",
			want:      "  DefaultPortalURL: (unset -- browser login falls back to the gateway)",
		},
	}

	for _, tc := range cases {
		got := formatDefault(tc.name, tc.value, tc.whenEmpty)
		if got != tc.want {
			t.Errorf("formatDefault(%q, %q, ...):\n got: %q\nwant: %q", tc.name, tc.value, got, tc.want)
		}
	}
}

// TestFormatDefaultDistinguishesTheConsequences guards the specific mistake made while
// writing this: a single shared "when empty" message claimed that any unset default would
// make clients ask for a gateway, which is only true of the server URL.
func TestFormatDefaultDistinguishesTheConsequences(t *testing.T) {
	server := formatDefault("DefaultServerURL", "", "clients will ask to be pointed at a gateway")
	portal := formatDefault("DefaultPortalURL", "", "browser login falls back to the gateway")

	if server == portal {
		t.Fatal("each default must explain its own consequence, not share one message")
	}
	if strings.Contains(portal, "ask to be pointed at a gateway") {
		t.Errorf("an unset portal URL does not make clients ask for a gateway, got: %q", portal)
	}
}
