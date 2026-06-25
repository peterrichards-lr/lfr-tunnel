package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// LoadOrCreateCA loads the Root CA certificate and key, or generates a self-signed Root CA if missing.
func LoadOrCreateCA(certPath, keyPath string) (*x509.Certificate, *rsa.PrivateKey, error) {
	if certPath == "" || keyPath == "" {
		return nil, nil, fmt.Errorf("ca certificate or key path not configured")
	}

	// 1. Ensure target directories exist
	if err := os.MkdirAll(filepath.Dir(certPath), 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create CA cert directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create CA key directory: %w", err)
	}

	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)

	if certErr == nil && keyErr == nil {
		// 2. Load existing CA cert and key
		certBytes, err := os.ReadFile(certPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}
		keyBytes, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read CA key: %w", err)
		}

		// Decode and parse cert
		block, _ := pem.Decode(certBytes)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, nil, fmt.Errorf("invalid CA certificate PEM format")
		}
		caCert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse CA certificate: %w", err)
		}

		// Decode and parse key
		keyBlock, _ := pem.Decode(keyBytes)
		if keyBlock == nil {
			return nil, nil, fmt.Errorf("invalid CA private key PEM format")
		}
		var caKey *rsa.PrivateKey
		switch keyBlock.Type {
		case "RSA PRIVATE KEY":
			caKey, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
		case "PRIVATE KEY":
			var parsedKey interface{}
			parsedKey, err = x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
			if err == nil {
				var ok bool
				caKey, ok = parsedKey.(*rsa.PrivateKey)
				if !ok {
					return nil, nil, fmt.Errorf("CA private key is not an RSA key")
				}
			}
		default:
			return nil, nil, fmt.Errorf("unsupported CA key block type: %s", keyBlock.Type)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse CA private key: %w", err)
		}

		return caCert, caKey, nil
	}

	// 3. Generate a new self-signed CA
	caKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate CA key pair: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate CA serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:         "Liferay Tunnel Root CA",
			Organization:       []string{"Liferay SE"},
			OrganizationalUnit: []string{"Tunnels"},
		},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().AddDate(10, 0, 0), // 10 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to self-sign CA certificate: %w", err)
	}

	caCert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse self-signed cert: %w", err)
	}

	// Write key to disk with secure 0600 permissions
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open CA key file for writing: %w", err)
	}
	defer keyOut.Close() //nolint:errcheck

	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(caKey)}); err != nil {
		return nil, nil, fmt.Errorf("failed to write CA private key: %w", err)
	}

	// Write cert to disk with 0644 permissions
	certOut, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open CA cert file for writing: %w", err)
	}
	defer certOut.Close() //nolint:errcheck

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return nil, nil, fmt.Errorf("failed to write CA cert: %w", err)
	}

	return caCert, caKey, nil
}

// GenerateClientP12 signs a client certificate and packages it into a password-protected PKCS#12 bundle (.p12).
func GenerateClientP12(caCert *x509.Certificate, caKey *rsa.PrivateKey, identity string, email string, name string, validityDays int, pfxPassword string) ([]byte, error) {
	// 1. Generate client key
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate client key: %w", err)
	}

	// 2. Serial number
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to generate client serial number: %w", err)
	}

	// 3. Client certificate template
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:         identity, // e.g. "guest:uuid" or "user:id"
			Organization:       []string{"Liferay SE"},
			OrganizationalUnit: []string{"Tunnels"},
			ExtraNames: []pkix.AttributeTypeAndValue{
				{
					Type:  []int{1, 2, 840, 113549, 1, 9, 1}, // Email address OID
					Value: email,
				},
				{
					Type:  []int{2, 5, 4, 3}, // Common Name / Full Name
					Value: name,
				},
			},
		},
		NotBefore:   time.Now().Add(-5 * time.Minute),
		NotAfter:    time.Now().AddDate(0, 0, validityDays),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	// 4. Sign certificate using Root CA
	derBytes, err := x509.CreateCertificate(rand.Reader, template, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign client certificate: %w", err)
	}

	clientCert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse signed certificate: %w", err)
	}

	// 5. Encode into PKCS#12 bundle
	pfxBytes, err := pkcs12.Legacy.Encode(clientKey, clientCert, []*x509.Certificate{caCert}, pfxPassword)
	if err != nil {
		return nil, fmt.Errorf("failed to encode PKCS#12 bundle: %w", err)
	}

	return pfxBytes, nil
}

// SignClientCSR parses a client CSR, signs it using the Root CA, and returns the PEM certificate.
func SignClientCSR(caCert *x509.Certificate, caKey *rsa.PrivateKey, csrPEM []byte, identity string, validityDays int) ([]byte, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || (block.Type != "CERTIFICATE REQUEST" && block.Type != "NEW CERTIFICATE REQUEST") {
		return nil, fmt.Errorf("invalid CSR PEM format")
	}

	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CSR: %w", err)
	}

	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("CSR signature verification failed: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to generate client serial number: %w", err)
	}

	// Enforce CN to match identity
	subject := csr.Subject
	subject.CommonName = identity

	template := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               subject,
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().AddDate(0, 0, validityDays),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, caCert, csr.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign client certificate from CSR: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: derBytes,
	})

	return certPEM, nil
}
