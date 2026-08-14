package provider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/time/rate"
)

func TestReadResponseBody(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "within limit", body: "ok"},
		{name: "too large", body: strings.Repeat("x", MaxResponseBodyBytes+1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReadResponseBody(strings.NewReader(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReadResponseBody() error = %v, want error: %v", err, tt.wantErr)
			}
		})
	}
}

func TestRateLimitedTransportHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	transport := &rateLimitedTransport{limiter: rate.NewLimiter(1, 0), base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(req); err == nil {
		t.Fatal("RoundTrip() succeeded with canceled context")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
