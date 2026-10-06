package mail

import (
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

// buildMessage must not let text a user typed become message structure (#2344 review). A token
// name reaches the subject and the plain body of the expiry emails, and token names are only
// checked for being non-empty. Each case parses the message the way a mail client would, rather
// than searching the raw text, because what matters is what the recipient's client sees.

func parse(t *testing.T, raw string) (*mail.Message, []string) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("the built message does not parse: %v\n%s", err, raw)
	}
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("bad Content-Type: %v", err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var types []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading parts: %v", err)
		}
		types = append(types, p.Header.Get("Content-Type"))
	}
	return msg, types
}

func TestASubjectCannotAddAHeader(t *testing.T) {
	for _, injected := range []string{"name\r\nBcc: victim@example.com", "name\nBcc: victim@example.com", "name\rBcc: victim@example.com"} {
		raw, err := buildMessage("from@example.com", "to@example.com", "Expiring: "+injected, "<p>hi</p>", "hi")
		if err != nil {
			t.Fatal(err)
		}
		msg, _ := parse(t, raw)
		if bcc := msg.Header.Get("Bcc"); bcc != "" {
			t.Errorf("subject %q produced a Bcc header: %q", injected, bcc)
		}
		subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(subject, "Bcc: victim@example.com") {
			t.Errorf("the injected text should survive as inert subject text, got %q", subject)
		}
	}
}

// The boundary used to be a fixed string, so a body containing it could close the plain-text part
// and open an HTML part of the sender's choosing.
func TestABodyCannotAddAPart(t *testing.T) {
	oldBoundary := "lfr-" + "tunnel-boundary-12345"
	plain := "hi\r\n--" + oldBoundary + "\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n<a href=\"https://evil.example\">click</a>"
	raw, err := buildMessage("from@example.com", "to@example.com", "s", "<p>real</p>", plain)
	if err != nil {
		t.Fatal(err)
	}
	_, types := parse(t, raw)
	if len(types) != 2 {
		t.Fatalf("expected exactly the two parts the gateway wrote, got %d: %v", len(types), types)
	}
	if !strings.HasPrefix(types[0], "text/plain") || !strings.HasPrefix(types[1], "text/html") {
		t.Errorf("parts are not the gateway's own plain then HTML: %v", types)
	}
}

func TestANonASCIISubjectIsEncoded(t *testing.T) {
	raw, err := buildMessage("from@example.com", "to@example.com", "Token: café ✓", "<p>hi</p>", "hi")
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := parse(t, raw)
	header := msg.Header.Get("Subject")
	if !strings.HasPrefix(header, "=?UTF-8?") {
		t.Errorf("a non-ASCII subject must be RFC 2047 encoded, got raw %q", header)
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(header)
	if err != nil || decoded != "Token: café ✓" {
		t.Errorf("the subject did not round-trip: %q (%v)", decoded, err)
	}
}
