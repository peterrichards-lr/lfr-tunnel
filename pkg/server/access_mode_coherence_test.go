package server

import (
	"errors"
	"strings"
	"testing"

	"lfr-tunnel/pkg/db"
)

// A mode must not name a factor the reservation does not have (#2156).
//
// The proxy decides what to enforce from whether each VALUE is non-empty, not from the mode, so
// "and" with an empty whitelist enforced the passcode alone while the portal displayed the
// strictest setting in the product. Found in production: one reservation in exactly that state,
// showing "Passcode AND Whitelist" and challenging for a passcode only.
func TestMissingAccessControlValue(t *testing.T) {
	const hash = "$2a$10$D9Uqx96vvqLs8tKTnyibye2BIdkG5aNBn6hcbv34uRP6zM83TFUQ6"
	const ips = "10.0.0.0/8"

	cases := []struct {
		name       string
		mode       string
		passcode   string
		whitelist  string
		wantReject bool
	}{
		// The reported defect.
		{"and with no whitelist", "and", hash, "", true},
		{"and with no passcode", "and", "", ips, true},
		{"and with neither", "and", "", "", true},
		{"and with both", "and", hash, ips, false},

		// The same class: a mode naming a single factor that is absent. Each of these is
		// enforced as fully public today, because with both values empty the proxy returns
		// early before applying anything.
		{"passcode mode with no passcode", "passcode", "", "", true},
		{"passcode mode with a passcode", "passcode", hash, "", false},
		{"whitelist mode with no whitelist", "whitelist", "", "", true},
		{"whitelist mode with a whitelist", "whitelist", "", ips, false},

		// "or" is deliberately never rejected. An unset mode defaults to it, so every
		// reservation nobody has configured is "or" with neither value -- four of them in
		// production -- and rejecting that would make untouched reservations unsaveable.
		{"or with neither is the unconfigured default", "or", "", "", false},
		{"or with one factor is meaningful", "or", hash, "", false},
		{"or with both", "or", hash, ips, false},

		// Public keeps its values on purpose, so that switching back restores them.
		{"public with values kept", "public", hash, ips, false},
		{"public with nothing", "public", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := missingAccessControlValue(tc.mode, tc.passcode, tc.whitelist)
			if tc.wantReject && got == "" {
				t.Errorf("mode %q with passcode=%t whitelist=%t was accepted; it claims a factor "+
					"that is not there, and the proxy would enforce fewer than the UI displays",
					tc.mode, tc.passcode != "", tc.whitelist != "")
			}
			if !tc.wantReject && got != "" {
				t.Errorf("mode %q with passcode=%t whitelist=%t was rejected (%q); this is a state "+
					"the product supports", tc.mode, tc.passcode != "", tc.whitelist != "", got)
			}
		})
	}
}

// The reason it explains which value is missing: a bare "invalid request" tells the user to
// guess which of two fields to fill in.
func TestTheRejectionNamesTheMissingValue(t *testing.T) {
	got := missingAccessControlValue("and", "$2a$10$something", "")
	if !strings.Contains(got, "whitelist") {
		t.Errorf("the message for and-without-a-whitelist does not mention the whitelist: %q", got)
	}
	if got := missingAccessControlValue("and", "", "10.0.0.0/8"); !strings.Contains(got, "passcode") {
		t.Errorf("the message for and-without-a-passcode does not mention the passcode: %q", got)
	}
}

// Validation resolves against STORED state, not the request body.
//
// PasscodeMask is the non-empty string "********", so a request carrying it LOOKS like a
// passcode to anything that inspects the body. This asserts the direction that actually costs
// something: a reservation with NO stored passcode, whose form sends the mask, must still be
// refused "and" -- because once saved it has one factor, whatever the request appeared to say.
//
// The first version of this test asserted the opposite hazard, that request-body validation
// would falsely REJECT a masked edit. It would not: the mask is non-empty, so that check
// passes, and the mutation ran green. A guard that cannot fail is not a guard.
func TestTheMaskCannotStandInForAPasscodeThatIsNotThere(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	owner := &db.User{ID: "m1@lfr-demo.local", Email: "m1@lfr-demo.local", Role: "user", Status: "approved"}
	if err := srv.db.CreateUser(owner); err != nil {
		t.Fatalf("creating the owner: %v", err)
	}

	const sub, domain = "nopasscode", "lfr-demo.local"
	if err := srv.db.CreateSubdomainReservation(&db.SubdomainReservation{
		Subdomain: sub, Domain: domain, UserID: owner.ID,
	}); err != nil {
		t.Skipf("no reservation fixture available in this build: %v", err)
	}

	svc := &portalService{db: srv.db}

	// No passcode has ever been set. The form sends the mask anyway, with a whitelist and "and".
	err := svc.UpdateReservationAccessControl(owner, sub, domain, "and", PasscodeMask, "10.0.0.0/8", "127.0.0.1")
	if err == nil {
		t.Fatal("and was accepted on a reservation with no stored passcode, because the request " +
			"carried PasscodeMask.\nThe mask is the non-empty string \"********\", so it looks " +
			"like a passcode to anything reading the request body -- but it resolves to no " +
			"passcode at all, and the tunnel would enforce the whitelist alone while displaying " +
			"Passcode AND Whitelist.")
	}
	if !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("got %v, which does not map to 400", err)
	}
}

// ...and the legitimate masked edit still goes through: an "and" reservation that DOES have a
// passcode, whose owner is changing only the whitelist.
func TestAMaskedEditOnAReservationThatHasAPasscodeIsAccepted(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	owner := &db.User{ID: "m2@lfr-demo.local", Email: "m2@lfr-demo.local", Role: "user", Status: "approved"}
	if err := srv.db.CreateUser(owner); err != nil {
		t.Fatalf("creating the owner: %v", err)
	}

	const sub, domain, chosen = "hasboth", "lfr-demo.local", "correct-horse"
	if err := srv.db.CreateSubdomainReservation(&db.SubdomainReservation{
		Subdomain: sub, Domain: domain, UserID: owner.ID,
	}); err != nil {
		t.Skipf("no reservation fixture available in this build: %v", err)
	}

	svc := &portalService{db: srv.db}

	if err := svc.UpdateReservationAccessControl(owner, sub, domain, "passcode", chosen, "", "127.0.0.1"); err != nil {
		t.Fatalf("setting the passcode: %v", err)
	}
	stored, err := srv.db.GetSubdomainReservationByName(sub, domain)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}

	// Treating the mask as "no passcode supplied" would reject this edit, which is the
	// over-strict failure in the other direction.
	if err := svc.UpdateReservationAccessControl(owner, sub, domain, "and",
		MaskPasscode(stored.Passcode), "10.0.0.0/8", "127.0.0.1"); err != nil {
		t.Fatalf("a masked edit on a reservation that HAS a passcode was rejected: %v", err)
	}

	after, err := srv.db.GetSubdomainReservationByName(sub, domain)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if !VerifyPasscode(chosen, after.Passcode) {
		t.Error("the original passcode no longer authenticates after the mode change")
	}
	if after.AccessMode != "and" || after.WhitelistIPs != "10.0.0.0/8" {
		t.Errorf("the edit was not saved: mode=%q whitelist=%q", after.AccessMode, after.WhitelistIPs)
	}
}

// ...and the reported state is refused, with a 400 rather than a 500.
func TestAndWithoutAWhitelistIsRefused(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	owner := &db.User{
		ID:     "owner2@lfr-demo.local",
		Email:  "owner2@lfr-demo.local",
		Role:   "user",
		Status: "approved",
	}
	if err := srv.db.CreateUser(owner); err != nil {
		t.Fatalf("creating the owner: %v", err)
	}

	const sub, domain = "halfand", "lfr-demo.local"
	if err := srv.db.CreateSubdomainReservation(&db.SubdomainReservation{
		Subdomain: sub,
		Domain:    domain,
		UserID:    owner.ID,
	}); err != nil {
		t.Skipf("no reservation fixture available in this build: %v", err)
	}

	svc := &portalService{db: srv.db}

	err := svc.UpdateReservationAccessControl(owner, sub, domain, "and", "a-passcode", "", "127.0.0.1")
	if err == nil {
		t.Fatal("and with an empty whitelist was accepted; the tunnel would display " +
			"'Passcode AND Whitelist' and enforce the passcode alone")
	}
	// ErrInvalidRequest is what maps to 400. Anything else reports a server fault for what is
	// a user-correctable mistake, and the UI shows no useful message.
	if !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("got %v, which does not map to 400", err)
	}

	// And nothing was written: a refused save must not leave the mode changed.
	after, readErr := srv.db.GetSubdomainReservationByName(sub, domain)
	if readErr != nil {
		t.Fatalf("reading back: %v", readErr)
	}
	if after.AccessMode == "and" {
		t.Error("the refused mode was stored anyway; validation runs after the fields are " +
			"assigned to the record, so it must return before UpdateSubdomainReservation")
	}
}
