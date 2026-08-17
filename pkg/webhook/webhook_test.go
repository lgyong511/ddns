package webhook

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"ddns/pkg/config"
)

func TestSendGETAndPOST(t *testing.T) {
	tests := []struct {
		name    string
		webhook config.Webhook
		check   func(*testing.T, *http.Request, string)
	}{
		{
			name: "get escapes query", webhook: config.Webhook{URL: "http://placeholder/?domain={{Domain}}&state={{State}}"},
			check: func(t *testing.T, request *http.Request, _ string) {
				if request.Method != http.MethodGet || request.URL.Query().Get("domain") != "nas example.com" || request.URL.Query().Get("state") != `a"b` {
					t.Fatalf("unexpected request: %s", request.URL.String())
				}
			},
		},
		{
			name: "post escapes JSON and headers", webhook: config.Webhook{URL: "http://placeholder/", Body: `{"state":"{{State}}"}`, Headers: []string{"X-Test: value"}},
			check: func(t *testing.T, request *http.Request, body string) {
				if request.Method != http.MethodPost || request.Header.Get("X-Test") != "value" || body != `{"state":"a\"b"}` {
					t.Fatalf("unexpected post request: header=%q body=%q", request.Header.Get("X-Test"), body)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalClient := httpClient
			httpClient = http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				var body []byte
				if request.Body != nil {
					body, _ = io.ReadAll(request.Body)
				}
				tt.check(t, request, string(body))
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
			})}
			defer func() { httpClient = originalClient }()
			cfg := tt.webhook
			cfg.URL = strings.Replace(cfg.URL, "http://placeholder", "https://example.com", 1)
			if err := NewWebhook(&cfg).Send(context.Background(), &WebhookData{Domain: "nas example.com", State: `a"b`}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSendRejectsNonSuccessOversizeAndCanceledContext(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		canceled bool
	}{
		{name: "non success", status: http.StatusBadGateway, body: "failed"},
		{name: "oversize", status: http.StatusOK, body: strings.Repeat("x", maxResponseBodyBytes+1)},
		{name: "canceled", status: http.StatusOK, canceled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalClient := httpClient
			httpClient = http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				if tt.canceled {
					return nil, request.Context().Err()
				}
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})}
			defer func() { httpClient = originalClient }()
			ctx := context.Background()
			if tt.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := NewWebhook(&config.Webhook{URL: "https://example.com"}).Send(ctx, &WebhookData{})
			if err == nil {
				t.Fatal("Send() succeeded")
			}
		})
	}
}

func TestSendCancelsInFlightRequest(t *testing.T) {
	originalClient := httpClient
	defer func() { httpClient = originalClient }()
	started := make(chan struct{}, 1)
	httpClient = http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- NewWebhook(&config.Webhook{URL: "https://example.com"}).Send(ctx, &WebhookData{})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not reach the HTTP transport")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Send() succeeded after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("Send() did not return after cancellation")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
