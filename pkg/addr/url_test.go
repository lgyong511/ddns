package addr

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestURLFetch(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{name: "address", status: http.StatusOK, body: "address: 8.8.8.8"},
		{name: "non success", status: http.StatusBadGateway, body: "failed", wantErr: true},
		{name: "oversize", status: http.StatusOK, body: strings.Repeat("x", 1<<20+1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := NewUrl("https://example.com")
			fetcher.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})
			got, err := fetcher.Fetch(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("Fetch() error = %v, want error: %v", err, tt.wantErr)
			}
			if !tt.wantErr && (len(got) != 1 || got[0].String() != "8.8.8.8") {
				t.Fatalf("Fetch() = %v", got)
			}
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
