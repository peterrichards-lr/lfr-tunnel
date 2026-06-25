package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateCAAndSignClientCert(t *testing.T) {
	// 1. Create a temp directory for CA files
	tmpDir, err := os.MkdirTemp("", "lfr-ca-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	certPath := filepath.Join(tmpDir, "ca.crt")
	keyPath := filepath.Join(tmpDir, "ca.key")

	// 2. Generate new Root CA
	caCert, caKey, err := LoadOrCreateCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateCA failed: %v", err)
	}

	if caCert == nil || caKey == nil {
		t.Fatal("expected non-nil CA cert and key")
	}

	if caCert.Subject.CommonName != "Liferay Tunnel Root CA" {
		t.Errorf("unexpected CA CN: %s", caCert.Subject.CommonName)
	}

	// Verify files were written
	if _, err := os.Stat(certPath); err != nil {
		t.Errorf("CA cert file not written: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("CA key file not written: %v", err)
	}

	// 3. Reload Root CA from files
	reloadedCert, reloadedKey, err := LoadOrCreateCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("reloading CA failed: %v", err)
	}

	if reloadedCert.Subject.CommonName != caCert.Subject.CommonName {
		t.Errorf("reloaded CA CN mismatch: got %s, expected %s", reloadedCert.Subject.CommonName, caCert.Subject.CommonName)
	}

	if reloadedKey == nil {
		t.Fatal("expected non-nil reloaded CA key")
	}

	// 4. Sign a client certificate .p12 bundle
	identity := "guest:test-uuid"
	email := "guest@example.com"
	name := "Test Guest"
	password := "testpassword"
	validityDays := 30

	pfxBytes, err := GenerateClientP12(caCert, caKey, identity, email, name, validityDays, password)
	if err != nil {
		t.Fatalf("GenerateClientP12 failed: %v", err)
	}

	if len(pfxBytes) == 0 {
		t.Fatal("generated PKCS#12 bundle is empty")
	}
}

func TestVerifyClientCertificate(t *testing.T) {
	// 1. DN extract CN test
	t.Run("ExtractCN", func(t *testing.T) {
		tests := []struct {
			dn       string
			expected string
		}{
			{"CN=guest:uuid,O=Liferay SE,C=US", "guest:uuid"},
			{"CN=user:123/O=Liferay SE", "user:123"},
			{"/CN=guest:456", "guest:456"},
			{"guest:789", "guest:789"},
			{"", ""},
		}

		for _, tc := range tests {
			got := ExtractCN(tc.dn)
			if got != tc.expected {
				t.Errorf("ExtractCN(%q) = %q; expected %q", tc.dn, got, tc.expected)
			}
		}
	})

	// 2. Verify proxy header-based verification
	t.Run("ProxyHeaders", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "https://sub.domain.com", nil)
		req.Header.Set("X-SSL-Client-Verify", "SUCCESS")
		req.Header.Set("X-SSL-Client-S-DN", "CN=guest:uuid,O=Liferay SE")

		identity, ok := VerifyClientCertificate(req, nil)
		if !ok {
			t.Fatal("expected client certificate verification to succeed")
		}
		if identity != "guest:uuid" {
			t.Errorf("expected identity guest:uuid, got %s", identity)
		}
	})

	t.Run("ProxyHeaders_Failure", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "https://sub.domain.com", nil)
		req.Header.Set("X-SSL-Client-Verify", "FAILED")
		req.Header.Set("X-SSL-Client-S-DN", "CN=guest:uuid")

		_, ok := VerifyClientCertificate(req, nil)
		if ok {
			t.Fatal("expected client certificate verification to fail")
		}
	})
}
