package huawei

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
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{name: "success", status: http.StatusOK, body: `{}`},
		{name: "non success", status: http.StatusUnauthorized, body: strings.Repeat("x", provider.MaxErrorResponseBodyBytes+1), wantErr: true},
		{name: "too large", status: http.StatusOK, body: strings.Repeat("x", provider.MaxResponseBodyBytes+1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldClient := provider.HTTPClient
			provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})}
			defer func() { provider.HTTPClient = oldClient }()
			_, err := NewHuawei("key", "secret").do(context.Background(), http.MethodGet, "https://example.com", "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("do() error = %v, want error: %v", err, tt.wantErr)
			}
			if tt.name == "non success" && (!strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "[truncated]")) {
				t.Fatalf("do() error = %v", err)
			}
		})
	}
}

func TestParseResponse(t *testing.T) {
	huawei := NewHuawei("key", "secret")
	records, err := huawei.parseResponse([]byte(`{"recordsets":[{"id":"1","name":"www.example.com.","type":"A","ttl":600,"records":["1.2.3.4"]}],"metadata":{"total_count":1}}`))
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
	provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body := `{}`
		if request.Method == http.MethodPost {
			body = `{"id":"created"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	huawei := NewHuawei("key", "secret")
	huawei.zoneId["example.com"] = "zone"
	record, err := huawei.Create(context.Background(), &provider.Record{DomainName: "example.com", RR: "www", Type: "A", Value: "1.2.3.4", TTL: 600})
	if err != nil || record.RecordId != "created" {
		t.Fatalf("Create() = %#v, %v", record, err)
	}
	if err := huawei.Update(context.Background(), record); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if err := huawei.Delete(context.Background(), record.RecordId, record.DomainName); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
}

func TestCRUDRejectsNilRecord(t *testing.T) {
	huawei := NewHuawei("key", "secret")
	if err := huawei.Update(context.Background(), nil); err == nil {
		t.Fatal("Update() accepted nil record")
	}
	if _, err := huawei.Create(context.Background(), nil); err == nil {
		t.Fatal("Create() accepted nil record")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
