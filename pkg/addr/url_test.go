package addr

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestURLFetchOrderedUsesConfigurationOrder(t *testing.T) {
	firstRelease := make(chan struct{})
	fetcher, err := NewUrlWithStrategy("https://first.example,https://second.example", "ordered")
	if err != nil {
		t.Fatal(err)
	}
	fetcher.client.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "first.example" {
			<-firstRelease
			return testHTTPResponse("1.1.1.1"), nil
		}
		close(firstRelease)
		return testHTTPResponse("2.2.2.2"), nil
	})

	got, err := fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("2.2.2.2")}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Fetch() = %v, want %v", got, want)
	}
}

func TestURLFetchFirstSuccessCancelsOtherRequests(t *testing.T) {
	canceled := make(chan struct{})
	var canceledCount atomic.Int32
	fetcher, err := NewUrlWithStrategy("https://slow.example,https://fast.example", "first-success")
	if err != nil {
		t.Fatal(err)
	}
	fetcher.client.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "fast.example" {
			return testHTTPResponse("8.8.8.8"), nil
		}
		<-request.Context().Done()
		if canceledCount.Add(1) == 1 {
			close(canceled)
		}
		return nil, request.Context().Err()
	})

	got, err := fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].String() != "8.8.8.8" {
		t.Fatalf("Fetch() = %v", got)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("slower request was not canceled")
	}
}

func TestURLFetchMajority(t *testing.T) {
	tests := []struct {
		name    string
		bodies  map[string]string
		want    string
		wantErr bool
	}{
		{name: "strict majority", bodies: map[string]string{"one.example": "8.8.8.8", "two.example": "8.8.8.8", "three.example": "1.1.1.1"}, want: "8.8.8.8"},
		{name: "no consensus", bodies: map[string]string{"one.example": "8.8.8.8", "two.example": "1.1.1.1"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fetcher, err := NewUrlWithStrategy("https://one.example,https://two.example,https://three.example", "majority")
			if test.name == "no consensus" {
				fetcher, err = NewUrlWithStrategy("https://one.example,https://two.example", "majority")
			}
			if err != nil {
				t.Fatal(err)
			}
			fetcher.client.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				return testHTTPResponse(test.bodies[request.URL.Host]), nil
			})
			got, err := fetcher.Fetch(context.Background())
			if (err != nil) != test.wantErr {
				t.Fatalf("Fetch() error = %v, want error %v", err, test.wantErr)
			}
			if !test.wantErr && (len(got) != 1 || got[0].String() != test.want) {
				t.Fatalf("Fetch() = %v, want %s", got, test.want)
			}
		})
	}
}

func TestURLFetcherValidation(t *testing.T) {
	if _, err := NewUrlWithStrategy("https://example.com", "random"); err == nil {
		t.Fatal("NewUrlWithStrategy() accepted an unknown strategy")
	}
	urls := strings.Repeat("https://example.com,", MaxURLSources) + "https://example.com"
	if err := ValidateURLs(urls); err == nil {
		t.Fatal("ValidateURLs() accepted too many URLs")
	}
}

func testHTTPResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
