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

func TestDoRejectsNonSuccessAndAPIError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "non success", status: http.StatusForbidden, body: "denied"},
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
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
