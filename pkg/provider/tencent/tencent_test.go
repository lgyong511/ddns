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
		{name: "non success", status: http.StatusTooManyRequests, body: `busy`, wantErr: true},
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
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
