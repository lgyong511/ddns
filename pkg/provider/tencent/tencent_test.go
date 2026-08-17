package tencent

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
		{name: "non success", status: http.StatusTooManyRequests, body: strings.Repeat("x", provider.MaxErrorResponseBodyBytes+1), wantErr: true},
		{name: "too large", status: http.StatusOK, body: strings.Repeat("x", provider.MaxResponseBodyBytes+1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldClient := provider.HTTPClient
			provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})}
			defer func() { provider.HTTPClient = oldClient }()
			_, err := NewTencent("key", "secret").do(context.Background(), "DescribeRecordList", `{}`)
			if (err != nil) != tt.wantErr {
				t.Fatalf("do() error = %v, want error: %v", err, tt.wantErr)
			}
			if tt.name == "non success" && (!strings.Contains(err.Error(), "HTTP 429") || !strings.Contains(err.Error(), "[truncated]")) {
				t.Fatalf("do() error = %v", err)
			}
		})
	}
}

func TestParseResponseRejectsBusinessErrorAndParsesRecord(t *testing.T) {
	if _, err := parseResponse([]byte(`{"Response":{"Error":{"Code":"AuthFailure","Message":"denied"}}}`), "example.com"); err == nil {
		t.Fatal("parseResponse() accepted business error")
	}
	longMessage := strings.Repeat("x", provider.MaxErrorResponseBodyBytes+1)
	longBody := []byte(`{"Response":{"Error":{"Code":"AuthFailure","Message":"` + longMessage + `"}}}`)
	if _, err := parseResponse(longBody, "example.com"); err == nil || !strings.Contains(err.Error(), "[truncated]") {
		t.Fatalf("parseResponse() error = %v, want truncated message", err)
	}
	records, err := parseResponse([]byte(`{"Response":{"RecordList":[{"RecordId":1,"Name":"www","Type":"A","Value":"1.2.3.4","TTL":600}]}}`), "example.com")
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
		body := `{"Response":{}}`
		if request.Header.Get("X-TC-Action") == "CreateRecord" {
			body = `{"Response":{"RecordId":1}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	tencent := NewTencent("key", "secret")
	record, err := tencent.Create(context.Background(), &provider.Record{DomainName: "example.com", RR: "www", Type: "A", Value: "1.2.3.4", TTL: 600})
	if err != nil || record.RecordId != "1" {
		t.Fatalf("Create() = %#v, %v", record, err)
	}
	if err := tencent.Update(context.Background(), record); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if err := tencent.Delete(context.Background(), record.RecordId, record.DomainName); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
}

func TestCRUDRejectsNilRecord(t *testing.T) {
	tencent := NewTencent("key", "secret")
	if err := tencent.Update(context.Background(), nil); err == nil {
		t.Fatal("Update() accepted nil record")
	}
	if _, err := tencent.Create(context.Background(), nil); err == nil {
		t.Fatal("Create() accepted nil record")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
