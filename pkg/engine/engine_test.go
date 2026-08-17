package engine

import (
	"context"
	"testing"
	"time"

	"ddns/pkg/config"
)

func TestEngineStartStopsWhenConfigurationIsUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		NewEngine(config.NewManager()).Start(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Engine.Start() did not return after cancellation")
	}
}

func TestProviderStartStopsWithoutRecords(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	provider := &Provider{provider: &config.Provider{Name: "home"}}
	done := make(chan struct{})
	go func() {
		provider.Start(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Provider.Start() did not wait for shutdown")
	}
}
