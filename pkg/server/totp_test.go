package server

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

func TestTOTP(t *testing.T) {
	// 1. Generate secret
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("failed to generate TOTP secret: %v", err)
	}
	if secret == "" {
		t.Fatal("expected non-empty TOTP secret")
	}

	// 2. Validate empty or invalid secret/code
	if ValidateTOTP("", "123456") {
		t.Error("expected empty secret validation to fail")
	}
	if ValidateTOTP(secret, "12345") {
		t.Error("expected too-short code validation to fail")
	}
	if ValidateTOTP(secret, "1234567") {
		t.Error("expected too-long code validation to fail")
	}
	if ValidateTOTP("invalid-base32-@", "123456") {
		t.Error("expected invalid base32 secret validation to fail")
	}

	// 3. Decode standard base32 and compute correct token to verify ValidateTOTP passes
	cleanSecret := strings.ToUpper(strings.TrimSpace(secret))
	missingPadding := len(cleanSecret) % 8
	if missingPadding > 0 {
		cleanSecret += strings.Repeat("=", 8-missingPadding)
	}
	key, err := base32.StdEncoding.DecodeString(cleanSecret)
	if err != nil {
		t.Fatalf("failed to decode base32 secret in test: %v", err)
	}

	currentUnix := time.Now().Unix()
	currentStep := uint64(currentUnix / 30)

	// Calculate standard code for current step
	code := calculateTOTP(key, currentStep)

	// Validate the correct code passes
	if !ValidateTOTP(secret, code) {
		t.Errorf("expected code %s to validate successfully for secret %s", code, secret)
	}

	// Calculate standard code for previous step (-30s)
	prevCode := calculateTOTP(key, currentStep-1)
	if !ValidateTOTP(secret, prevCode) {
		t.Errorf("expected previous step code %s to validate successfully (drift validation)", prevCode)
	}

	// Verify incorrect code fails
	// Determine a code that is guaranteed to be incorrect by checking the 3 valid codes
	correctCodes := map[string]bool{
		calculateTOTP(key, currentStep-1): true,
		calculateTOTP(key, currentStep):   true,
		calculateTOTP(key, currentStep+1): true,
	}
	incorrectCode := "999999"
	if correctCodes[incorrectCode] {
		incorrectCode = "888888"
		if correctCodes[incorrectCode] {
			incorrectCode = "777777"
		}
	}

	if ValidateTOTP(secret, incorrectCode) {
		t.Errorf("expected incorrect code %s to fail validation", incorrectCode)
	}
}
