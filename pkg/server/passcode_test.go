package server

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// Subdomain passcode storage (#1490).
//
// #466 specified "SHA-256 with salt, or bcrypt" and closed. What shipped was unsalted
// single-round SHA-256 with a plaintext fallback -- a control documented as fixed that was not.
// These pin both halves of the fix: the new format, and that legacy values still work and retire
// themselves.

func TestHashPasscodeIsSaltedAndSlow(t *testing.T) {
	const pass = "my-secure-passcode"

	h1 := HashPasscode(pass)
	h2 := HashPasscode(pass)

	if h1 == "" || h2 == "" {
		t.Fatal("hashing a passcode produced nothing")
	}
	// The property unsalted SHA-256 lacked: identical passcodes must not be visibly identical in
	// the database, across rows or across tenants.
	if h1 == h2 {
		t.Error("two hashes of the same passcode are identical -- the hash is unsalted")
	}
	if h1 == pass {
		t.Error("the passcode was stored as itself")
	}
	// And it must not be the old format, or nothing has changed.
	if len(h1) == 64 && !strings.HasPrefix(h1, "$") {
		t.Error("the stored value still looks like the legacy unsalted SHA-256 hex")
	}
	if _, err := bcrypt.Cost([]byte(h1)); err != nil {
		t.Errorf("stored value is not a bcrypt hash: %v", err)
	}

	if !VerifyPasscode(pass, h1) || !VerifyPasscode(pass, h2) {
		t.Error("a passcode did not verify against its own hash")
	}
	if VerifyPasscode("wrong-passcode", h1) {
		t.Error("the wrong passcode verified")
	}
}

// TestVerifyPasscodeAcceptsLegacyFormats — every deployment out there has values in the old
// formats. Rejecting them would lock people out of their own tunnels.
func TestVerifyPasscodeAcceptsLegacyFormats(t *testing.T) {
	const pass = "legacy-passcode"

	legacySHA := legacyPasscodeHash(pass)
	if !VerifyPasscode(pass, legacySHA) {
		t.Error("a legacy SHA-256 passcode no longer verifies")
	}
	if VerifyPasscode("wrong", legacySHA) {
		t.Error("the wrong passcode verified against a legacy SHA-256 value")
	}

	// Plaintext, from before #466 hashed anything at all.
	if !VerifyPasscode(pass, pass) {
		t.Error("a legacy plaintext passcode no longer verifies")
	}
	if VerifyPasscode("wrong", pass) {
		t.Error("the wrong passcode verified against a legacy plaintext value")
	}
}

// TestPasscodeNeedsUpgrade drives the transparent migration: legacy values report that they need
// re-hashing, current ones do not, so a value is upgraded exactly once.
func TestPasscodeNeedsUpgrade(t *testing.T) {
	const pass = "some-passcode"

	if !PasscodeNeedsUpgrade(legacyPasscodeHash(pass)) {
		t.Error("a legacy SHA-256 value should be upgraded")
	}
	if !PasscodeNeedsUpgrade(pass) {
		t.Error("a plaintext value should be upgraded")
	}
	if PasscodeNeedsUpgrade(HashPasscode(pass)) {
		t.Error("a current bcrypt value must not be re-hashed on every verify")
	}
	if PasscodeNeedsUpgrade("") {
		t.Error("an unset passcode is not something to upgrade")
	}
}

// TestVerifyPasscodeRejectsEmpties — an empty passcode must never open anything, in either
// position. "No passcode configured" and "the visitor typed nothing" are both refusals.
func TestVerifyPasscodeRejectsEmpties(t *testing.T) {
	h := HashPasscode("real")
	if VerifyPasscode("", h) {
		t.Error("an empty submission verified")
	}
	if VerifyPasscode("real", "") {
		t.Error("a passcode verified against an unset stored value")
	}
	if VerifyPasscode("", "") {
		t.Error("empty verified against empty")
	}
	if HashPasscode("") != "" {
		t.Error("hashing an empty passcode should yield nothing to store")
	}
}

// TestHashPasscodeRefusesOverLongInput — bcrypt silently truncates past 72 bytes, which would make
// two long passcodes sharing a prefix interchangeable. Refusing is safer than storing something
// that verifies more than it should.
func TestHashPasscodeRefusesOverLongInput(t *testing.T) {
	long := strings.Repeat("a", 73)
	if HashPasscode(long) != "" {
		t.Error("a passcode past bcrypt's 72-byte limit was hashed anyway")
	}
	if HashPasscode(strings.Repeat("a", 72)) == "" {
		t.Error("a passcode exactly at the limit should still hash")
	}
}
