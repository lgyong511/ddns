package baidu

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"ddns/pkg/provider"
)

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
			oldClient := provider.HTTPClient
			provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})}
			defer func() { provider.HTTPClient = oldClient }()
			_, err := NewBaidu("key", "secret").do(context.Background(), http.MethodGet, "/test", url.Values{}, nil)
			if err == nil {
				t.Fatal("do() succeeded")
			}
			if tt.name == "non success" && (!strings.Contains(err.Error(), "HTTP 502") || !strings.Contains(err.Error(), "[truncated]")) {
				t.Fatalf("do() error = %v", err)
			}
		})
	}
}

func TestParseResponse(t *testing.T) {
	records, err := parseResponse([]byte(`{"records":[{"id":"1","rr":"www","type":"A","value":"1.2.3.4","ttl":600}]}`), "example.com", "A")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].RecordId != "1" || records[0].DomainName != "example.com" {
		t.Fatalf("parseResponse() = %#v", records)
	}
}

func TestCRUDUsesSuccessfulResponses(t *testing.T) {
	originalClient := provider.HTTPClient
	defer func() { provider.HTTPClient = originalClient }()
	provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"recordId":"created"}`)), Header: make(http.Header)}, nil
	})}
	b := NewBaidu("key", "secret")
	record, err := b.Create(context.Background(), &provider.Record{DomainName: "example.com", RR: "www", Type: "A", Value: "1.2.3.4", TTL: 600})
	if err != nil || record.RecordId != "created" {
		t.Fatalf("Create() = %#v, %v", record, err)
	}
	if err := b.Update(context.Background(), record); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if err := b.Delete(context.Background(), record.RecordId, record.DomainName); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
}

func TestCreateRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: "{"},
		{name: "missing record ID", body: `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalClient := provider.HTTPClient
			defer func() { provider.HTTPClient = originalClient }()
			provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})}
			_, err := NewBaidu("key", "secret").Create(context.Background(), &provider.Record{DomainName: "example.com", RR: "www", Type: "A", Value: "1.2.3.4", TTL: 600})
			if err == nil {
				t.Fatal("Create() accepted invalid response")
			}
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
