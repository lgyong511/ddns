package volcengine

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"ddns/pkg/provider"
)

func TestDoRejectsNonSuccessOversizedAndAPIError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "non success", status: http.StatusForbidden, body: strings.Repeat("x", provider.MaxErrorResponseBodyBytes+1)},
		{name: "oversized", status: http.StatusOK, body: strings.Repeat("x", provider.MaxResponseBodyBytes+1)},
		{name: "api error", status: http.StatusOK, body: `{"ResponseMetadata":{"Error":{"Code":"Denied","Message":"denied"}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldClient := provider.HTTPClient
			provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})}
			defer func() { provider.HTTPClient = oldClient }()
			_, err := NewVolcengine("key", "secret").do(context.Background(), http.MethodPost, "ListRecords", url.Values{}, nil)
			if err == nil {
				t.Fatal("do() succeeded")
			}
			if tt.name == "non success" && (!strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "[truncated]")) {
				t.Fatalf("do() error = %v", err)
			}
		})
	}
}

func TestParseRecords(t *testing.T) {
	records, err := parseRecords([]byte(`{"Records":[{"RecordID":1,"Host":"www","Type":"A","Value":"1.2.3.4","TTL":600}]}`), "example.com", "www", "A")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].RecordId != "1" || records[0].Value != "1.2.3.4" {
		t.Fatalf("parseRecords() = %#v", records)
	}
}

func TestCRUDUsesSuccessfulResponses(t *testing.T) {
	originalClient := provider.HTTPClient
	defer func() { provider.HTTPClient = originalClient }()
	provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body := `{}`
		switch request.URL.Query().Get("Action") {
		case "ListZones":
			body = `{"Zones":[{"ZID":1,"ZoneName":"example.com"}]}`
		case "CreateRecord":
			body = `{"RecordID":2}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	volcengine := NewVolcengine("key", "secret")
	record, err := volcengine.Create(context.Background(), &provider.Record{DomainName: "example.com", RR: "www", Type: "A", Value: "1.2.3.4", TTL: 600})
	if err != nil || record.RecordId != "2" {
		t.Fatalf("Create() = %#v, %v", record, err)
	}
	if err := volcengine.Update(context.Background(), record); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if err := volcengine.Delete(context.Background(), record.RecordId, record.DomainName); err != nil {
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
			provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				body := tt.body
				if request.URL.Query().Get("Action") == "ListZones" {
					body = `{"Zones":[{"ZID":1,"ZoneName":"example.com"}]}`
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			_, err := NewVolcengine("key", "secret").Create(context.Background(), &provider.Record{DomainName: "example.com", RR: "www", Type: "A", Value: "1.2.3.4", TTL: 600})
			if err == nil {
				t.Fatal("Create() accepted invalid response")
			}
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
