package server

import (
	"crypto/x509"
	"net/http"
	"strings"
)

// ExtractCN parses the Common Name (CN) from a Subject DN string.
func ExtractCN(dn string) string {
	idx := strings.Index(strings.ToUpper(dn), "CN=")
	if idx == -1 {
		if !strings.Contains(dn, "=") {
			return strings.TrimSpace(dn)
		}
		return ""
	}

	val := dn[idx+3:]
	endIdx := len(val)
	if commaIdx := strings.Index(val, ","); commaIdx != -1 && commaIdx < endIdx {
		endIdx = commaIdx
	}
	if slashIdx := strings.Index(val, "/"); slashIdx != -1 && slashIdx < endIdx {
		endIdx = slashIdx
	}

	return strings.TrimSpace(val[:endIdx])
}

// VerifyClientCertificate checks the TLS peer certificates or the trusted proxy headers for client certificate authentication.
func VerifyClientCertificate(r *http.Request, caCert *x509.Certificate) (string, bool) {
	// 1. Direct TLS Connection verification (Go standalone mode)
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		cert := r.TLS.PeerCertificates[0]
		if caCert != nil {
			roots := x509.NewCertPool()
			roots.AddCert(caCert)
			opts := x509.VerifyOptions{
				Roots:     roots,
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			}
			if _, err := cert.Verify(opts); err != nil {
				return "", false
			}
		}
		return cert.Subject.CommonName, true
	}

	// 2. Nginx Reverse Proxy Offloading (X-SSL-Client-Verify / X-SSL-Client-S-DN headers)
	clientVerify := r.Header.Get("X-SSL-Client-Verify")
	clientDN := r.Header.Get("X-SSL-Client-S-DN")

	if strings.ToUpper(clientVerify) == "SUCCESS" && clientDN != "" {
		cn := ExtractCN(clientDN)
		if cn != "" {
			return cn, true
		}
	}

	return "", false
}
