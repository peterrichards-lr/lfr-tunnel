package server

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"lfr-tunnel/pkg/db"
)

// The reservations API returned the stored bcrypt hash, the portal put it in an editable text
// box, and saving the dialog again stored HashPasscode(hash) -- so the passcode the user chose
// stopped working and the replacement was a value nobody could type (#2103).
//
// Reported as: "when I go back to the dialog, there is a long random passcode, rather than the
// one I typed and saved", with the value `$2a$10$D9Uqx…`.

func TestAStoredHashNeverLeavesTheServer(t *testing.T) {
	hash := "$2a$10$D9Uqx96vvqLs8tKTnyibye2BIdkG5aNBn6hcbv34uRP6zM83TFUQ6"

	got := MaskPasscode(hash)

	if strings.Contains(got, "$2a$") || got == hash {
		t.Errorf("MaskPasscode returned %q, which still carries the hash.\n"+
			"It is offline-crackable credential material and was being handed to every caller "+
			"who could list reservations", got)
	}
	if got != PasscodeMask {
		t.Errorf("got %q, want the shared mask %q -- the client's auth token already uses that "+
			"one, and a second convention is one more thing to know", got, PasscodeMask)
	}
}

// "No passcode" and "a passcode you cannot see" have to stay distinguishable, or the UI cannot
// tell the user whether one is set.
func TestNoPasscodeMasksToNothing(t *testing.T) {
	if got := MaskPasscode(""); got != "" {
		t.Errorf("an unset passcode masked to %q, so the UI would claim one is set", got)
	}
}

// The mask must be recognisable on the way back in, or the fix is only cosmetic: the data loss
// came from the SAVE, not from the display.
func TestTheMaskIsDistinctFromAnyPasscodeWorthSetting(t *testing.T) {
	if PasscodeMask == "" {
		t.Fatal("an empty mask would be indistinguishable from clearing the passcode")
	}
	if hashed := HashPasscode(PasscodeMask); hashed == "" {
		t.Skip("HashPasscode refuses the mask outright, which is also acceptable")
	}
	// Documented rather than asserted: someone could set their passcode to eight asterisks, and
	// it would then be un-updatable. That is a far smaller harm than silently destroying every
	// passcode on every unrelated save, and it is the same trade the auth token already makes.
}

// THE control this issue exists for: a save that does not touch the passcode field must leave
// the stored passcode working.
//
// Before the mask, the portal pre-filled the field with the bcrypt hash, so changing only the
// whitelist -- or only the MODE -- re-submitted the hash and stored HashPasscode(hash). The
// passcode stopped authenticating, with no error shown and no way back to a value anyone knows.
func TestSavingWithoutTouchingThePasscodeKeepsItWorking(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	owner := &db.User{
		ID:     "owner@lfr-demo.local",
		Email:  "owner@lfr-demo.local",
		Role:   "user",
		Status: "approved",
	}
	if err := srv.db.CreateUser(owner); err != nil {
		t.Fatalf("creating the owner: %v", err)
	}

	const sub, domain, chosen = "protected", "lfr-demo.local", "correct-horse"

	if err := srv.db.CreateSubdomainReservation(&db.SubdomainReservation{
		Subdomain: sub,
		Domain:    domain,
		UserID:    owner.ID,
	}); err != nil {
		t.Skipf("no reservation fixture available in this build: %v", err)
	}

	svc := &portalService{db: srv.db}

	// 1. Set a passcode the way a user would.
	if err := svc.UpdateReservationAccessControl(owner, sub, domain, "passcode", chosen, "", "127.0.0.1"); err != nil {
		t.Fatalf("setting the passcode: %v", err)
	}

	stored, err := srv.db.GetSubdomainReservationByName(sub, domain)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if !VerifyPasscode(chosen, stored.Passcode) {
		t.Fatalf("the passcode did not verify immediately after being set")
	}

	// 2. Save again, sending back exactly what the API would have handed the UI. This is the
	//    user changing the whitelist and leaving the passcode field alone.
	sentBack := MaskPasscode(stored.Passcode)
	if err := svc.UpdateReservationAccessControl(owner, sub, domain, "passcode", sentBack, "10.0.0.0/8", "127.0.0.1"); err != nil {
		t.Fatalf("second save: %v", err)
	}

	after, err := srv.db.GetSubdomainReservationByName(sub, domain)
	if err != nil {
		t.Fatalf("reading back after the second save: %v", err)
	}

	if !VerifyPasscode(chosen, after.Passcode) {
		t.Error("the ORIGINAL passcode no longer authenticates after a save that never touched " +
			"the passcode field.\nThat is the defect: the stored value has been re-hashed into " +
			"something nobody can type, and the only symptom is a visitor being locked out.")
	}
	if after.WhitelistIPs != "10.0.0.0/8" {
		t.Errorf("the whitelist the user actually came to change was not saved: %q", after.WhitelistIPs)
	}
}

// MaskPasscode is only worth anything if the API actually calls it.
//
// A control proved this gap: reverting api.go to `Passcode: res.Passcode` left every test above
// green, because they exercised the helper rather than the call site. The hash would have gone
// straight back on the wire with nothing complaining.
func TestTheAPIMasksRatherThanMerelyOwningAMasker(t *testing.T) {
	source, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatalf("reading api.go: %v", err)
	}

	raw := regexp.MustCompile(`Passcode:\s+res\.Passcode\b`)
	if loc := raw.FindIndex(source); loc != nil {
		line := 1 + strings.Count(string(source[:loc[0]]), "\n")
		t.Errorf("api.go:%d assigns the stored passcode straight into an API response.\n"+
			"That is the bcrypt hash: the portal put it in an editable box, the user saw "+
			"$2a$10$..., and saving again stored HashPasscode(hash). Use MaskPasscode.", line)
	}

	// PREMISE: the masker is actually reachable from here, so the absence above means "masked"
	// rather than "the field was removed and this check now guards nothing".
	if !strings.Contains(string(source), "MaskPasscode(") {
		t.Error("api.go never calls MaskPasscode, so the assertion above would pass on a file " +
			"that simply stopped returning the field at all")
	}
}
