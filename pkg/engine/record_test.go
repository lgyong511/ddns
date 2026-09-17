package engine

import (
	"net/netip"
	"testing"
	"time"

	"ddns/pkg/config"
	"ddns/pkg/provider"
)

func TestNewRecordStateRejectsInvalidSelector(t *testing.T) {
	record := &config.Record{
		IPVersion: provider.IPv4,
		GetType:   "url",
		GetValue:  "https://example.com",
		Rule:      "unexpected",
	}
	if _, err := NewRecordState(record); err == nil {
		t.Fatal("NewRecordState() accepted an invalid selector")
	}
}

func TestRecordStateCacheAndRetry(t *testing.T) {
	state := &RecordState{cacheSubDomain: map[string]SubDomainInfo{}}
	address := netip.MustParseAddr("8.8.8.8")
	if need, _ := state.ShouldSync("nas.example.com", address); !need {
		t.Fatal("first sync was not requested")
	}
	state.UpdateCache("nas.example.com", address, 5)
	if need, _ := state.ShouldSync("nas.example.com", address); need {
		t.Fatal("unchanged address was unexpectedly synced")
	}
	failures, gap := state.IncFailCount("nas.example.com", 5)
	if failures != 1 || gap != 30*time.Second {
		t.Fatalf("IncFailCount() = (%d, %v)", failures, gap)
	}
	if need, _ := state.ShouldSync("nas.example.com", address); need {
		t.Fatal("retry backoff was ignored")
	}
}
