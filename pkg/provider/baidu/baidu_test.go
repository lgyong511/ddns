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
		name string
		body string
	}{
		{name: "non success", body: "bad"},
		{name: "oversized", body: strings.Repeat("x", provider.MaxResponseBodyBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldClient := provider.HTTPClient
			provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})}
			defer func() { provider.HTTPClient = oldClient }()
			_, err := NewBaidu("key", "secret").do(context.Background(), http.MethodGet, "/test", url.Values{}, nil)
			if err == nil {
				t.Fatal("do() succeeded")
			}
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
