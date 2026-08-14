package addr

import (
	"net/netip"
	"testing"
)

func TestFilterAddrsFiltersAndDeduplicates(t *testing.T) {
	addrs := []netip.Addr{
		netip.MustParseAddr("192.168.1.1"),
		netip.MustParseAddr("8.8.8.8"),
		netip.MustParseAddr("8.8.8.8"),
		netip.MustParseAddr("100.64.0.1"),
		netip.MustParseAddr("2001:4860:4860::8888"),
	}
	got := FilterAddrs(addrs, IsPublic)
	if len(got) != 2 || got[0].String() != "8.8.8.8" || got[1].String() != "2001:4860:4860::8888" {
		t.Fatalf("FilterAddrs() = %v", got)
	}
}

func TestSpliceSelectDoesNotMutateIndex(t *testing.T) {
	selector := NewSplice(3, "::1")
	got := selector.Select([]netip.Addr{netip.MustParseAddr("2001:db8::2")})
	if got.String() != "2001:db8::1" {
		t.Fatalf("Select() = %s", got)
	}
	if selector.Index != 3 {
		t.Fatalf("selector index mutated to %d", selector.Index)
	}
}
