package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestCloudflareClient(t *testing.T, handler http.HandlerFunc) *CloudflareClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &CloudflareClient{BaseURL: server.URL, Token: "test-token", HTTP: server.Client()}
}

func TestCloudflareProvider_LookupZone(t *testing.T) {
	var gotAuth, gotPath string
	client := newTestCloudflareClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		_, _ = fmt.Fprint(w, `{"success":true,"errors":[],"result":[{"id":"zone-123","name":"example.com"}]}`) //nolint:errcheck
	})
	provider := &CloudflareProvider{Client: client}

	zone, exists, err := provider.LookupZone(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("LookupZone failed: %v", err)
	}
	if !exists || zone.ID != "zone-123" {
		t.Fatalf("expected zone-123 to exist, got %+v exists=%v", zone, exists)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("expected Authorization header 'Bearer test-token', got %q", gotAuth)
	}
	if !strings.Contains(gotPath, "/zones") || !strings.Contains(gotPath, "name=example.com") {
		t.Errorf("expected GET /zones?name=example.com, got %q", gotPath)
	}
}

func TestCloudflareProvider_LookupZone_NotFound(t *testing.T) {
	client := newTestCloudflareClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"success":true,"errors":[],"result":[]}`) //nolint:errcheck
	})
	provider := &CloudflareProvider{Client: client}

	_, exists, err := provider.LookupZone(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("LookupZone failed: %v", err)
	}
	if exists {
		t.Fatal("expected exists=false for an empty result set")
	}
}

func TestCloudflareProvider_ListRecords_PaginatesAndFiltersNSAndSOA(t *testing.T) {
	callCount := 0
	client := newTestCloudflareClient(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var body string
		if r.URL.Query().Get("page") == "1" {
			body = `{"success":true,"errors":[],"result":[
				{"id":"r1","type":"A","name":"example.com","content":"1.2.3.4","ttl":120,"proxied":false},
				{"id":"r2","type":"NS","name":"example.com","content":"ns1.cloudflare.com","ttl":86400}
			],"result_info":{"page":1,"total_pages":2}}`
		} else {
			body = `{"success":true,"errors":[],"result":[
				{"id":"r3","type":"TXT","name":"_dmarc.example.com","content":"v=DMARC1; p=reject;","ttl":120},
				{"id":"r4","type":"SOA","name":"example.com","content":"ignored","ttl":86400}
			],"result_info":{"page":2,"total_pages":2}}`
		}
		_, _ = fmt.Fprint(w, body) //nolint:errcheck
	})
	provider := &CloudflareProvider{Client: client}

	records, err := provider.ListRecords(context.Background(), ZoneRef{Domain: "example.com", ID: "zone-123"})
	if err != nil {
		t.Fatalf("ListRecords failed: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 paginated requests, got %d", callCount)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records after filtering NS/SOA, got %d: %+v", len(records), records)
	}
	if records[0].Name != "@" || records[0].Type != RecordTypeA {
		t.Errorf("expected first record to be the apex A record, got %+v", records[0])
	}
	if records[1].Name != "_dmarc" || records[1].Type != RecordTypeTXT {
		t.Errorf("expected second record to be _dmarc TXT, got %+v", records[1])
	}
}

func TestCloudflareProvider_TXTContentIsQuotedOnWriteAndUnquotedOnRead(t *testing.T) {
	// Cloudflare stores/returns TXT content WITH literal double quotes
	// embedded in the string itself (confirmed against the live production
	// zone) -- not just display quoting. Both directions must round-trip
	// through that, or every TXT record spuriously diffs as changed.
	var gotBody cfDNSRecord
	client := newTestCloudflareClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)                                            //nolint:errcheck
			_, _ = fmt.Fprint(w, `{"success":true,"errors":[],"result":{"id":"new-id"}}`) //nolint:errcheck
			return
		}
		body := `{"success":true,"errors":[],"result":[
			{"id":"r1","type":"TXT","name":"_dmarc.example.com","content":"\"v=DMARC1; p=reject;\"","ttl":120}
		],"result_info":{"page":1,"total_pages":1}}`
		_, _ = fmt.Fprint(w, body) //nolint:errcheck
	})
	provider := &CloudflareProvider{Client: client}
	zone := ZoneRef{Domain: "example.com", ID: "zone-123"}

	change := Change{Desired: Record{Name: "_dmarc", Type: RecordTypeTXT, Value: "v=DMARC1; p=reject;", TTL: 120}, Action: ActionCreate}
	if err := provider.ApplyChange(context.Background(), zone, change); err != nil {
		t.Fatalf("ApplyChange failed: %v", err)
	}
	if gotBody.Content != `"v=DMARC1; p=reject;"` {
		t.Errorf("expected literal-quoted TXT content on write, got %q", gotBody.Content)
	}

	records, err := provider.ListRecords(context.Background(), zone)
	if err != nil {
		t.Fatalf("ListRecords failed: %v", err)
	}
	if len(records) != 1 || records[0].Value != "v=DMARC1; p=reject;" {
		t.Errorf("expected unquoted TXT value on read, got %+v", records)
	}
}

func TestCloudflareProvider_ApplyChange_CreateAndUpdate(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody cfDNSRecord
	client := newTestCloudflareClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)                                            //nolint:errcheck
		_, _ = fmt.Fprint(w, `{"success":true,"errors":[],"result":{"id":"new-id"}}`) //nolint:errcheck
	})
	provider := &CloudflareProvider{Client: client}
	zone := ZoneRef{Domain: "example.com", ID: "zone-123"}

	priority := 10
	createChange := Change{
		Desired: Record{Name: "@", Type: RecordTypeMX, Value: "tunnel.example.com", TTL: 120, Priority: &priority},
		Action:  ActionCreate,
	}
	if err := provider.ApplyChange(context.Background(), zone, createChange); err != nil {
		t.Fatalf("ApplyChange (create) failed: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/zones/zone-123/dns_records" {
		t.Errorf("expected POST /zones/zone-123/dns_records, got %s %s", gotMethod, gotPath)
	}
	if gotBody.Name != "example.com" || gotBody.Priority == nil || *gotBody.Priority != 10 {
		t.Errorf("unexpected create payload: %+v", gotBody)
	}

	updateChange := Change{
		Desired: Record{Name: "tunnel", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120},
		Current: &ProviderRecord{ProviderID: "existing-id"},
		Action:  ActionUpdate,
	}
	if err := provider.ApplyChange(context.Background(), zone, updateChange); err != nil {
		t.Fatalf("ApplyChange (update) failed: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/zones/zone-123/dns_records/existing-id" {
		t.Errorf("expected PUT /zones/zone-123/dns_records/existing-id, got %s %s", gotMethod, gotPath)
	}
	if gotBody.Name != "tunnel.example.com" || gotBody.Proxied == nil || *gotBody.Proxied != false {
		t.Errorf("unexpected update payload: %+v", gotBody)
	}
}

func TestCloudflareProvider_ApplyChange_UpdateWithoutCurrentIDFails(t *testing.T) {
	provider := &CloudflareProvider{Client: &CloudflareClient{}}
	change := Change{Desired: Record{Name: "@", Type: RecordTypeA}, Action: ActionUpdate}

	if err := provider.ApplyChange(context.Background(), ZoneRef{}, change); err == nil {
		t.Fatal("expected an error when updating without a known existing record id")
	}
}

func TestCloudflareProvider_ErrorResponseSurfacesAsGoError(t *testing.T) {
	client := newTestCloudflareClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"success":false,"errors":[{"code":9109,"message":"authentication error"}],"result":null}`) //nolint:errcheck
	})
	provider := &CloudflareProvider{Client: client}

	_, _, err := provider.LookupZone(context.Background(), "example.com")
	if err == nil {
		t.Fatal("expected a Go error for a success:false response")
	}
	if !strings.Contains(err.Error(), "authentication error") {
		t.Errorf("expected the error to include the cloudflare error message, got: %v", err)
	}
}

func TestCloudflareProvider_CreateZoneIsUnsupported(t *testing.T) {
	requestMade := false
	client := newTestCloudflareClient(t, func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
	})
	provider := &CloudflareProvider{Client: client}

	if _, err := provider.CreateZone(context.Background(), "example.com"); err == nil {
		t.Fatal("expected CreateZone to return an error")
	}
	if requestMade {
		t.Error("expected CreateZone to make zero HTTP calls")
	}
}
