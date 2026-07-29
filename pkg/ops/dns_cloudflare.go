package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CloudflareClient is a small hand-rolled REST client for the four
// Cloudflare API v4 endpoints this adapter needs. BaseURL is injectable so
// tests can point it at an httptest.NewServer, mirroring pkg/webhook's
// testing pattern.
type CloudflareClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewCloudflareClient builds a client against the real Cloudflare API.
func NewCloudflareClient(token string) *CloudflareClient {
	return &CloudflareClient{
		BaseURL: "https://api.cloudflare.com/client/v4",
		Token:   token,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfResultInfo struct {
	Page       int `json:"page"`
	TotalPages int `json:"total_pages"`
}

type cfResponse struct {
	Success    bool            `json:"success"`
	Errors     []cfError       `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo cfResultInfo    `json:"result_info"`
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfDNSRecord struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  *bool  `json:"proxied,omitempty"`
	Priority *int   `json:"priority,omitempty"`
}

func (c *CloudflareClient) request(ctx context.Context, method, path string, body interface{}) (*cfResponse, error) {
	var reqBody *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling cloudflare request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	} else {
		reqBody = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("building cloudflare request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudflare request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck

	var envelope cfResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decoding cloudflare response: %w", err)
	}
	if !envelope.Success {
		return nil, fmt.Errorf("cloudflare API error: %s", cfErrorsString(envelope.Errors))
	}
	return &envelope, nil
}

func cfErrorsString(errs []cfError) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = fmt.Sprintf("[%d] %s", e.Code, e.Message)
	}
	return strings.Join(parts, "; ")
}

// CloudflareProvider implements Provider against the Cloudflare API v4.
type CloudflareProvider struct {
	Client *CloudflareClient
}

// NewCloudflareProvider builds a CloudflareProvider against the real Cloudflare API.
func NewCloudflareProvider(token string) *CloudflareProvider {
	return &CloudflareProvider{Client: NewCloudflareClient(token)}
}

func (p *CloudflareProvider) Name() string { return "cloudflare" }

func (p *CloudflareProvider) LookupZone(ctx context.Context, domain string) (ZoneRef, bool, error) {
	resp, err := p.Client.request(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(domain), nil)
	if err != nil {
		return ZoneRef{}, false, err
	}

	var zones []cfZone
	if err := json.Unmarshal(resp.Result, &zones); err != nil {
		return ZoneRef{}, false, fmt.Errorf("decoding cloudflare zones list: %w", err)
	}

	for _, z := range zones {
		if strings.EqualFold(z.Name, domain) {
			return ZoneRef{Domain: domain, ID: z.ID}, true, nil
		}
	}
	return ZoneRef{}, false, nil
}

// CreateZone is intentionally unsupported: Cloudflare zone creation requires
// an out-of-band registrar nameserver delegation step this tool has no
// business automating.
func (p *CloudflareProvider) CreateZone(_ context.Context, domain string) (ZoneRef, error) {
	return ZoneRef{}, fmt.Errorf("cloudflare zone creation is not supported by this tool; create the zone %q via the Cloudflare dashboard and complete registrar delegation first", domain)
}

func (p *CloudflareProvider) ListRecords(ctx context.Context, zone ZoneRef) ([]ProviderRecord, error) {
	var records []ProviderRecord
	page := 1
	for {
		resp, err := p.Client.request(ctx, http.MethodGet, fmt.Sprintf("/zones/%s/dns_records?page=%d&per_page=100", zone.ID, page), nil)
		if err != nil {
			return nil, err
		}

		var batch []cfDNSRecord
		if err := json.Unmarshal(resp.Result, &batch); err != nil {
			return nil, fmt.Errorf("decoding cloudflare dns_records: %w", err)
		}

		for _, r := range batch {
			if r.Type == "NS" || r.Type == "SOA" {
				continue
			}
			records = append(records, cfRecordToProviderRecord(zone.Domain, r))
		}

		if resp.ResultInfo.TotalPages == 0 || resp.ResultInfo.Page >= resp.ResultInfo.TotalPages {
			break
		}
		page++
	}
	return records, nil
}

func (p *CloudflareProvider) ApplyChange(ctx context.Context, zone ZoneRef, change Change) error {
	payload := recordToCFPayload(zone.Domain, change.Desired)

	switch change.Action {
	case ActionCreate:
		_, err := p.Client.request(ctx, http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", zone.ID), payload)
		return err
	case ActionUpdate:
		if change.Current == nil || change.Current.ProviderID == "" {
			return fmt.Errorf("cannot update %s %s: no existing record id known", change.Desired.Name, change.Desired.Type)
		}
		_, err := p.Client.request(ctx, http.MethodPut, fmt.Sprintf("/zones/%s/dns_records/%s", zone.ID, change.Current.ProviderID), payload)
		return err
	default:
		return fmt.Errorf("unsupported change action %s", change.Action)
	}
}

// toCloudflareName converts a spec-relative name ("@", "tunnel", "*") into
// Cloudflare's fully-qualified record name convention.
func toCloudflareName(zoneDomain, name string) string {
	if name == "@" || name == "" {
		return zoneDomain
	}
	return name + "." + zoneDomain
}

// fromCloudflareName is the inverse of toCloudflareName.
func fromCloudflareName(zoneDomain, cfName string) string {
	if strings.EqualFold(cfName, zoneDomain) {
		return "@"
	}
	return strings.TrimSuffix(cfName, "."+zoneDomain)
}

// toCloudflareContent and fromCloudflareContent handle the one wire-format
// quirk Cloudflare's API has that Record.Value doesn't: TXT record content
// is stored and returned WITH literal double quotes embedded in the string
// itself (confirmed against the live production zone), not just displayed
// quoted -- so both directions need to add/strip that quoting explicitly.
func toCloudflareContent(r Record) string {
	if r.Type == RecordTypeTXT {
		return `"` + strings.ReplaceAll(r.Value, `"`, `\"`) + `"`
	}
	return r.Value
}

func fromCloudflareContent(recordType RecordType, content string) string {
	if recordType == RecordTypeTXT {
		unquoted := strings.TrimSuffix(strings.TrimPrefix(content, `"`), `"`)
		return strings.ReplaceAll(unquoted, `\"`, `"`)
	}
	return content
}

func recordToCFPayload(zoneDomain string, r Record) cfDNSRecord {
	payload := cfDNSRecord{
		Type:    string(r.Type),
		Name:    toCloudflareName(zoneDomain, r.Name),
		Content: toCloudflareContent(r),
		TTL:     r.TTL,
	}
	if r.Type == RecordTypeA || r.Type == RecordTypeAAAA || r.Type == RecordTypeCNAME {
		proxied := false
		if r.Cloudflare.Proxied != nil {
			proxied = *r.Cloudflare.Proxied
		}
		payload.Proxied = &proxied
	}
	if r.Priority != nil {
		p := *r.Priority
		payload.Priority = &p
	}
	return payload
}

func cfRecordToProviderRecord(zoneDomain string, r cfDNSRecord) ProviderRecord {
	recordType := RecordType(r.Type)
	rec := Record{
		Name:  fromCloudflareName(zoneDomain, r.Name),
		Type:  recordType,
		Value: fromCloudflareContent(recordType, r.Content),
		TTL:   r.TTL,
	}
	if r.Priority != nil {
		p := *r.Priority
		rec.Priority = &p
	}
	if r.Proxied != nil {
		proxied := *r.Proxied
		rec.Cloudflare.Proxied = &proxied
	}
	return ProviderRecord{Record: rec, ProviderID: r.ID}
}
