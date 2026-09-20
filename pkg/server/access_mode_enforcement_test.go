package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Access control could not be set from the client Inspector OR from either portal (#2098).
//
// Every UI offers the same five modes -- public, passcode, whitelist, or, and -- and the API
// accepted only "and", "or" or "". Three of five were rejected outright. Behind that, the proxy
// never consulted the mode except to choose AND vs OR, so there was no way to express "keep
// these values, do not apply them", which is what Public means.

func accessCheck(t *testing.T, passcode, whitelist, mode, visitorIP string) bool {
	t.Helper()

	lease := &TunnelLease{}
	lease.SetAccessControls(passcode, whitelist, mode)

	p := &ProxyHandler{}
	r := httptest.NewRequest("GET", "http://protected.lfr-demo.se/", nil)
	r.RemoteAddr = visitorIP + ":12345"
	w := httptest.NewRecorder()

	return p.checkAccessControls(w, r, lease, "protected.lfr-demo.se")
}

// Public: stored, not applied. The passcode stays in the reservation so switching back
// re-applies it without retyping, and the tunnel is reachable meanwhile.
func TestPublicDoesNotEnforceAStoredPasscode(t *testing.T) {
	if !accessCheck(t, "hashed-passcode", "10.0.0.0/8", "public", "203.0.113.9") {
		t.Error("a visitor was refused on a tunnel whose mode is Public.\n" +
			"Public has to mean the stored passcode and whitelist are not applied -- otherwise " +
			"the only way to open a tunnel is to delete what you typed.")
	}
}

// Passcode mode applies ONLY the passcode.
//
// The discriminating case is a visitor the whitelist would have admitted. Under OR that visitor
// is let straight through; under Passcode the whitelist is not a factor, so they must still be
// challenged. An earlier version of this test used a NON-whitelisted IP, which OR also refuses
// -- so it passed whether or not the narrowing existed, and a control proved it.
func TestPasscodeModeIgnoresTheWhitelist(t *testing.T) {
	whitelisted := "10.1.2.3"

	if accessCheck(t, "hashed-passcode", "10.0.0.0/8", "passcode", whitelisted) {
		t.Error("a whitelisted visitor was admitted under Passcode mode with no passcode.\n" +
			"That is OR's behaviour. Selecting Passcode must stop enforcing a whitelist the " +
			"user typed earlier and then switched away from.")
	}
}

// Whitelist mode applies ONLY the whitelist.
//
// Discriminated by WHICH page is served: OR would challenge for the passcode, since one is
// stored; Whitelist must refuse the IP outright, because the passcode is not a factor.
func TestWhitelistModeIgnoresThePasscode(t *testing.T) {
	lease := &TunnelLease{}
	lease.SetAccessControls("hashed-passcode", "10.0.0.0/8", "whitelist")

	p := &ProxyHandler{}
	r := httptest.NewRequest("GET", "http://protected.lfr-demo.se/", nil)
	r.RemoteAddr = "203.0.113.9:12345" // outside the whitelist
	w := httptest.NewRecorder()

	if p.checkAccessControls(w, r, lease, "protected.lfr-demo.se") {
		t.Fatal("a non-whitelisted visitor was admitted under Whitelist mode")
	}

	body := w.Body.String()
	if strings.Contains(strings.ToLower(body), "passcode") {
		t.Error("Whitelist mode served the passcode challenge. The passcode is not one of this " +
			"mode's factors, so the visitor should be refused on the IP alone -- serving the " +
			"challenge offers a way in that the chosen mode does not include.")
	}
}

// The historic behaviour, unchanged: OR lets either factor through.
func TestOrStillLetsAWhitelistedVisitorThrough(t *testing.T) {
	if !accessCheck(t, "hashed-passcode", "10.0.0.0/8", "or", "10.1.2.3") {
		t.Error("OR refused a whitelisted visitor")
	}
}

// ...and AND still requires both, so a whitelisted visitor without a passcode is stopped.
func TestAndStillRequiresBoth(t *testing.T) {
	if accessCheck(t, "hashed-passcode", "10.0.0.0/8", "and", "10.1.2.3") {
		t.Error("AND let a whitelisted visitor through with no passcode")
	}
}

// PREMISE: with nothing stored the tunnel is open regardless of mode, so the assertions above
// are about the mode rather than about empty fields.
func TestNothingStoredIsOpenWhateverTheMode(t *testing.T) {
	for _, mode := range []string{"", "public", "passcode", "whitelist", "or", "and"} {
		if !accessCheck(t, "", "", mode, "203.0.113.9") {
			t.Errorf("mode %q refused a visitor with no passcode and no whitelist stored", mode)
		}
	}
}
