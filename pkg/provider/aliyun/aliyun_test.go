package aliyun

import (
	"context"
	"io"
	"net/http"
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
		{name: "non success", status: http.StatusBadRequest, body: strings.Repeat("x", provider.MaxErrorResponseBodyBytes+1)},
		{name: "oversized", status: http.StatusOK, body: strings.Repeat("x", provider.MaxResponseBodyBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldClient := provider.HTTPClient
			provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})}
			defer func() { provider.HTTPClient = oldClient }()
			_, err := NewAliyun("key", "secret").do(context.Background(), newRequest(http.MethodGet, "DescribeDomainRecords"))
			if err == nil {
				t.Fatal("do() succeeded")
			}
			if tt.name == "non success" && (!strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "[truncated]")) {
				t.Fatalf("do() error = %v", err)
			}
		})
	}
}

func TestParseResponse(t *testing.T) {
	records, err := parseResponse([]byte(`{"TotalCount":1,"DomainRecords":{"Record":[{"RecordId":"1","DomainName":"example.com","RR":"www","Type":"A","Value":"1.2.3.4","TTL":600}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].RecordId != "1" || records[0].Value != "1.2.3.4" {
		t.Fatalf("parseResponse() = %#v", records)
	}
}

func TestCRUDUsesSuccessfulResponses(t *testing.T) {
	originalClient := provider.HTTPClient
	defer func() { provider.HTTPClient = originalClient }()
	provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body := `{}`
		if request.Header.Get("X-Acs-Action") == "AddDomainRecord" {
			body = `{"RecordId":"created"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	a := NewAliyun("key", "secret")
	record, err := a.Create(context.Background(), &provider.Record{DomainName: "example.com", RR: "www", Type: "A", Value: "1.2.3.4", TTL: 600})
	if err != nil || record.RecordId != "created" {
		t.Fatalf("Create() = %#v, %v", record, err)
	}
	if err := a.Update(context.Background(), record); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if err := a.Delete(context.Background(), record.RecordId, record.DomainName); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
}

func TestCRUDRejectsNilRecord(t *testing.T) {
	a := NewAliyun("key", "secret")
	if err := a.Update(context.Background(), nil); err == nil {
		t.Fatal("Update() accepted nil record")
	}
	if _, err := a.Create(context.Background(), nil); err == nil {
		t.Fatal("Create() accepted nil record")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
