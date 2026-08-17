package dnsla

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"ddns/pkg/provider"
)

func TestParseRecordListResponse(t *testing.T) {
	body := []byte(`{
		"code": 200,
		"msg": "",
		"data": {
			"total": 1,
			"results": [
				{
					"id": "85394988049110016",
					"domainId": "85371689655342080",
					"host": "www",
					"type": 1,
					"data": "1.1.1.1",
					"ttl": 600,
					"disable": false,
					"system": false
				}
			]
		}
	}`)

	records, err := parseRecordListResponse(body, "example.com")
	if err != nil {
		t.Fatalf("parseRecordListResponse returned error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].RecordId != "85394988049110016" {
		t.Fatalf("unexpected record id: %s", records[0].RecordId)
	}
	if records[0].RR != "www" {
		t.Fatalf("unexpected rr: %s", records[0].RR)
	}
	if records[0].Type != "A" {
		t.Fatalf("unexpected type: %s", records[0].Type)
	}
	if records[0].Value != "1.1.1.1" {
		t.Fatalf("unexpected value: %s", records[0].Value)
	}
	if records[0].TTL != 600 {
		t.Fatalf("unexpected ttl: %d", records[0].TTL)
	}
}

func TestBuildBasicAuth(t *testing.T) {
	provider := NewDNSLA("myApiId", "mySecret")
	if got := provider.basicAuth(); got != "Basic bXlBcGlJZDpteVNlY3JldA==" {
		t.Fatalf("unexpected basic auth: %s", got)
	}
}

func TestParseDomainIDResponse(t *testing.T) {
	body := []byte(`{"code":200,"data":{"id":"85369994254488576","name":"example.com"}}`)
	id, err := parseDomainIDResponse(body)
	if err != nil {
		t.Fatalf("parseDomainIDResponse returned error: %v", err)
	}
	if id != "85369994254488576" {
		t.Fatalf("unexpected domain id: %s", id)
	}
}

func TestRecordTypeCode(t *testing.T) {
	if got := recordTypeCode("A"); got != 1 {
		t.Fatalf("expected A to map to 1, got %d", got)
	}
	if got := recordTypeCode("AAAA"); got != 28 {
		t.Fatalf("expected AAAA to map to 28, got %d", got)
	}
}

func TestResolveDomainIDConcurrentAccess(t *testing.T) {
	originalClient := provider.HTTPClient
	provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":200,"data":{"id":"domain-id"}}`)),
			Header:     make(http.Header),
		}, nil
	})}
	defer func() { provider.HTTPClient = originalClient }()

	dnsla := NewDNSLA("id", "secret")
	const callers = 32
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := dnsla.resolveDomainID(context.Background(), "example.com")
			if err != nil {
				errs <- err
			} else if id != "domain-id" {
				errs <- fmt.Errorf("domain ID = %q, want %q", id, "domain-id")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestMutationsRejectBusinessErrors(t *testing.T) {
	originalClient := provider.HTTPClient
	provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"code":500,"msg":"operation failed"}`
		if request.URL.Path == "/api/domain" {
			body = `{"code":200,"data":{"id":"domain-id"}}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
	defer func() { provider.HTTPClient = originalClient }()

	dnsla := NewDNSLA("id", "secret")
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "create",
			call: func() error {
				_, err := dnsla.Create(context.Background(), &provider.Record{DomainName: "example.com", RR: "www", Type: "A", Value: "1.2.3.4", TTL: 600})
				return err
			},
		},
		{
			name: "update",
			call: func() error {
				return dnsla.Update(context.Background(), &provider.Record{RecordId: "record-id", DomainName: "example.com", RR: "www", Type: "A", Value: "1.2.3.4", TTL: 600})
			},
		},
		{
			name: "delete",
			call: func() error {
				return dnsla.Delete(context.Background(), "record-id", "example.com")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil || !strings.Contains(err.Error(), "code=500, msg=operation failed") {
				t.Fatalf("operation error = %v, want business error", err)
			}
		})
	}
}

func TestDoRejectsNonSuccessAndOversizedResponse(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "non success", status: http.StatusBadGateway, body: strings.Repeat("x", provider.MaxErrorResponseBodyBytes+1)},
		{name: "oversized", status: http.StatusOK, body: strings.Repeat("x", provider.MaxResponseBodyBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalClient := provider.HTTPClient
			provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})}
			defer func() { provider.HTTPClient = originalClient }()
			_, err := NewDNSLA("id", "secret").do(context.Background(), http.MethodGet, "/record", nil, nil)
			if err == nil {
				t.Fatal("do() succeeded")
			}
			if tt.name == "non success" && (!strings.Contains(err.Error(), "HTTP 502") || !strings.Contains(err.Error(), "[truncated]")) {
				t.Fatalf("do() error = %v", err)
			}
		})
	}
}

func TestCRUDUsesSuccessfulResponses(t *testing.T) {
	originalClient := provider.HTTPClient
	defer func() { provider.HTTPClient = originalClient }()
	provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"code":200,"data":{"id":"created"}}`)), Header: make(http.Header)}, nil
	})}
	dnsla := NewDNSLA("id", "secret")
	dnsla.domainIDCache["example.com"] = "domain"
	record, err := dnsla.Create(context.Background(), &provider.Record{DomainName: "example.com", RR: "www", Type: "A", Value: "1.2.3.4", TTL: 600})
	if err != nil || record.RecordId != "created" {
		t.Fatalf("Create() = %#v, %v", record, err)
	}
	if err := dnsla.Update(context.Background(), record); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if err := dnsla.Delete(context.Background(), record.RecordId, record.DomainName); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
