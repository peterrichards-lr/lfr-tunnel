package provisioner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGenerateOrLoadToken_GeneratesOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")

	token, err := GenerateOrLoadToken(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(token) != 64 { // 32 random bytes, hex-encoded
		t.Errorf("expected a 64-char hex token, got %d chars", len(token))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("token file was not created: %v", err)
	}
	// Windows has no POSIX permission bits -- os.WriteFile's mode argument is
	// only meaningfully honored on Unix-like systems there (see the same
	// pattern in pkg/config/config_test.go).
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("expected token file permissions 0600, got %o", info.Mode().Perm())
	}
}

func TestGenerateOrLoadToken_LoadsExistingOnSecondRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")

	first, err := GenerateOrLoadToken(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := GenerateOrLoadToken(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first != second {
		t.Error("expected the same token to be loaded on the second call, got a different one")
	}
}

func TestGenerateOrLoadToken_RejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := GenerateOrLoadToken(path); err == nil {
		t.Fatal("expected error for empty token file")
	}
}

func TestValidToken(t *testing.T) {
	if !ValidToken("secret", "secret") {
		t.Error("expected matching tokens to be valid")
	}
	if ValidToken("secret", "wrong") {
		t.Error("expected mismatched tokens to be invalid")
	}
	if ValidToken("secret", "") {
		t.Error("expected empty presented token to be invalid")
	}
}
