package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// GenerateTOTPSecret generates a secure random 20-byte base32 secret (160 bits).
func GenerateTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// Use NoPadding standard base32 for maximum authenticator compatibility
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

// ValidateTOTP validates a 6-digit TOTP token against a base32 secret.
// It allows a time window drift of -1 to +1 steps (30 seconds each) to accommodate clock drift.
func ValidateTOTP(secret string, code string) bool {
	secret = strings.ToUpper(strings.TrimSpace(secret))
	if secret == "" {
		return false
	}

	// Restore base32 padding if missing
	missingPadding := len(secret) % 8
	if missingPadding > 0 {
		secret += strings.Repeat("=", 8-missingPadding)
	}

	key, err := base32.StdEncoding.DecodeString(secret)
	if err != nil {
		return false
	}

	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}

	currentUnix := time.Now().Unix()
	currentStep := currentUnix / 30

	// Validate against current step, previous step (-30s), and next step (+30s)
	for stepOffset := -1; stepOffset <= 1; stepOffset++ {
		counter := uint64(currentStep + int64(stepOffset))
		if calculateTOTP(key, counter) == code {
			return true
		}
	}

	return false
}

// calculateTOTP performs the HMAC-SHA1 dynamic truncation (RFC 6238 / RFC 4226)
func calculateTOTP(key []byte, counter uint64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	sum := mac.Sum(nil)

	// Dynamic Truncation
	offset := sum[len(sum)-1] & 0xf
	binCode := binary.BigEndian.Uint32(sum[offset : offset+4])
	binCode &= 0x7fffffff
	otp := binCode % 1000000

	return fmt.Sprintf("%06d", otp)
}
