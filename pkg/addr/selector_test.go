package addr

import (
	"net/netip"
	"testing"
)

func TestNewSelector(t *testing.T) {
	addrs := []netip.Addr{netip.MustParseAddr("2001:db8::2"), netip.MustParseAddr("2001:db8::3")}
	tests := []struct {
		name string
		rule string
		want string
	}{
		{name: "default", want: "2001:db8::2"},
		{name: "index", rule: "index@2", want: "2001:db8::3"},
		{name: "invalid index", rule: "index@0", want: "2001:db8::2"},
		{name: "contain", rule: "contain@::3", want: "2001:db8::3"},
		{name: "splice", rule: "splice@2@::1", want: "2001:db8::1"},
		{name: "unknown", rule: "unexpected", want: "2001:db8::2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewSelector(tt.rule).Select(addrs).String(); got != tt.want {
				t.Fatalf("NewSelector(%q).Select() = %q, want %q", tt.rule, got, tt.want)
			}
		})
	}
}

func TestNewFetcherRejectsUnsupportedType(t *testing.T) {
	if _, err := NewFetcher("unknown", "value"); err == nil {
		t.Fatal("NewFetcher() accepted unsupported type")
	}
}

func TestNewFetcherCreatesSupportedTypes(t *testing.T) {
	tests := []struct {
		getType  string
		getValue string
	}{
		{getType: "cmd", getValue: "echo 127.0.0.1"},
		{getType: "duid", getValue: "duid"},
		{getType: "nic", getValue: "lo"},
		{getType: "url", getValue: "https://example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.getType, func(t *testing.T) {
			fetcher, err := NewFetcher(tt.getType, tt.getValue)
			if err != nil || fetcher == nil {
				t.Fatalf("NewFetcher(%q) = %T, %v", tt.getType, fetcher, err)
			}
		})
	}
}
