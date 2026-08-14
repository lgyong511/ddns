package aliyun

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"ddns/pkg/provider"
)

func TestDoRejectsNonSuccessResponse(t *testing.T) {
	oldClient := provider.HTTPClient
	provider.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{"Code":"Invalid","Message":"bad"}`)), Header: make(http.Header)}, nil
	})}
	defer func() { provider.HTTPClient = oldClient }()
	_, err := NewAliyun("key", "secret").do(context.Background(), newRequest(http.MethodGet, "DescribeDomainRecords"))
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("do() error = %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
