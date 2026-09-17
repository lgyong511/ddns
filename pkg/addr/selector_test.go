package addr

import (
	"bytes"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

func TestNewSelector(t *testing.T) {
	addrs := []netip.Addr{
		netip.MustParseAddr("2001:db8:1::2"),
		netip.MustParseAddr("2001:db8:2::3"),
	}
	tests := []struct {
		name string
		rule string
		want string
	}{
		{name: "default", want: "2001:db8:1::2"},
		{name: "first", rule: "first", want: "2001:db8:1::2"},
		{name: "index", rule: "index@2", want: "2001:db8:2::3"},
		{name: "contain", rule: "contain@2::", want: "2001:db8:2::3"},
		{name: "prefix", rule: "prefix@2001:db8:2::/64", want: "2001:db8:2::3"},
		{name: "splice", rule: "splice@2@::1", want: "2001:db8:2::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector, err := NewSelector(tt.rule)
			if err != nil {
				t.Fatal(err)
			}
			if got := selector.Select(addrs).String(); got != tt.want {
				t.Fatalf("NewSelector(%q).Select() = %q, want %q", tt.rule, got, tt.want)
			}
		})
	}
}

func TestNewSelectorRejectsInvalidRules(t *testing.T) {
	rules := []string{
		"unexpected",
		"index@0",
		"index@invalid",
		"contain@",
		"prefix@invalid",
		"splice@1",
		"splice@0@::1",
		"splice@1@invalid",
		"splice@1@192.0.2.1",
	}
	for _, rule := range rules {
		t.Run(rule, func(t *testing.T) {
			if _, err := NewSelector(rule); err == nil {
				t.Fatalf("NewSelector(%q) accepted invalid rule", rule)
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

func TestNewFetcherWarnsOnceForDeprecatedCommand(t *testing.T) {
	previousLogger := slog.Default()
	defer slog.SetDefault(previousLogger)
	defer func() { commandDeprecationOnce = sync.Once{} }()

	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	commandDeprecationOnce = sync.Once{}
	for range 2 {
		if _, err := NewFetcher("cmd", "echo 127.0.0.1"); err != nil {
			t.Fatal(err)
		}
	}
	if count := strings.Count(output.String(), "cmd 地址获取方式已弃用"); count != 1 {
		t.Fatalf("deprecation warning count = %d, want 1; output: %s", count, output.String())
	}
}
