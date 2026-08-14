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

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
