package mail

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// generateSelfSignedCert generates a self-signed key/cert pair in memory.
func generateSelfSignedCert(t *testing.T) tls.Certificate {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Liferay Tunnel Test"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(1 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("failed to load x509 key pair: %v", err)
	}

	return cert
}

func startMockSMTPServer(t *testing.T, cert *tls.Certificate) (net.Listener, chan string) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start listener: %v", err)
	}

	dataChan := make(chan string, 1)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // Listener stopped
			}

			go handleMockConnection(t, conn, cert, dataChan)
		}
	}()

	return listener, dataChan
}

func handleMockConnection(t *testing.T, conn net.Conn, cert *tls.Certificate, dataChan chan string) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	respond := func(code int, msg string) {
		_, _ = writer.WriteString(fmt.Sprintf("%d %s\r\n", code, msg))
		_ = writer.Flush()
	}

	respond(220, "smtp.liferay-tunnel-test.local")

	var currentConn net.Conn = conn
	var isTLS bool = false
	var receivedBody strings.Builder

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)

		if strings.HasPrefix(strings.ToUpper(line), "EHLO") || strings.HasPrefix(strings.ToUpper(line), "HELO") {
			if !isTLS && cert != nil {
				_, _ = writer.WriteString("250-smtp.liferay-tunnel-test.local\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n")
			} else {
				_, _ = writer.WriteString("250-smtp.liferay-tunnel-test.local\r\n250 AUTH PLAIN\r\n")
			}
			_ = writer.Flush()
		} else if strings.ToUpper(line) == "STARTTLS" && cert != nil && !isTLS {
			respond(220, "Ready to start TLS")

			// Upgrade to TLS connection
			tlsConn := tls.Server(currentConn, &tls.Config{Certificates: []tls.Certificate{*cert}})
			if err := tlsConn.Handshake(); err != nil {
				t.Errorf("TLS handshake failed: %v", err)
				return
			}
			currentConn = tlsConn
			reader = bufio.NewReader(currentConn)
			writer = bufio.NewWriter(currentConn)
			isTLS = true
		} else if strings.HasPrefix(strings.ToUpper(line), "AUTH PLAIN") {
			respond(235, "Authentication successful")
		} else if strings.HasPrefix(strings.ToUpper(line), "MAIL FROM:") {
			respond(250, "Ok")
		} else if strings.HasPrefix(strings.ToUpper(line), "RCPT TO:") {
			respond(250, "Ok")
		} else if strings.ToUpper(line) == "DATA" {
			respond(354, "Start mail input; end with <CRLF>.<CRLF>")

			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				if dataLine == ".\r\n" || dataLine == ".\n" {
					break
				}
				receivedBody.WriteString(dataLine)
			}
			respond(250, "Ok: queued")
			dataChan <- receivedBody.String()
			break
		} else if strings.ToUpper(line) == "QUIT" {
			respond(221, "Bye")
			break
		} else {
			respond(500, "Unrecognized command")
		}
	}
}

func TestSMTPClient_Send(t *testing.T) {
	cert := generateSelfSignedCert(t)
	server, dataChan := startMockSMTPServer(t, &cert)
	defer server.Close()

	_, portStr, err := net.SplitHostPort(server.Addr().String())
	if err != nil {
		t.Fatalf("failed to split port: %v", err)
	}
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	cfg := &Config{
		SMTPHost:           "127.0.0.1",
		SMTPPort:           port,
		SMTPUsername:       "testuser",
		SMTPPassword:       "testpass",
		SMTPFromAddress:    "noreply@liferay.com",
		InsecureSkipVerify: true,
	}

	client := NewSMTPClient(cfg)
	err = client.Send("dev@liferay.com", "Test Subject", "<h1>Hello World</h1>")
	if err != nil {
		t.Fatalf("failed to send mail: %v", err)
	}

	// Retrieve email body from mock server
	select {
	case body := <-dataChan:
		if !strings.Contains(body, "Subject: Test Subject") {
			t.Errorf("expected subject header, got: %s", body)
		}
		if !strings.Contains(body, "Content-Type: text/html; charset=UTF-8") {
			t.Errorf("expected MIME type header, got: %s", body)
		}
		if !strings.Contains(body, "<h1>Hello World</h1>") {
			t.Errorf("expected HTML body, got: %s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for email payload to arrive at mock server")
	}
}
