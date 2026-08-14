package engine

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"ddns/pkg/config"
	"ddns/pkg/provider"
	"ddns/pkg/webhook"
)

type fakeOperator struct {
	getRecords []provider.Record
	getErr     error
	created    []provider.Record
	updated    []provider.Record
}

func (f *fakeOperator) GetAll(context.Context, string, provider.Version) ([]provider.Record, error) {
	return f.getRecords, f.getErr
}

func (f *fakeOperator) GetSub(context.Context, string, provider.Version) ([]provider.Record, error) {
	return f.getRecords, f.getErr
}

func (f *fakeOperator) Create(_ context.Context, record *provider.Record) (*provider.Record, error) {
	f.created = append(f.created, *record)
	return record, nil
}

func (f *fakeOperator) Update(_ context.Context, record *provider.Record) error {
	f.updated = append(f.updated, *record)
	return nil
}

func (f *fakeOperator) Delete(context.Context, string, string) error { return nil }

func TestSyncToProviderCreatesAndUpdates(t *testing.T) {
	tests := []struct {
		name       string
		getErr     error
		getRecords []provider.Record
		wantCreate int
		wantUpdate int
	}{
		{name: "create missing record", getErr: provider.ErrRecordNotFound, wantCreate: 1},
		{name: "update matching type", getRecords: []provider.Record{{RecordId: "a", DomainName: "example.com", RR: "nas", Type: "A", Value: "1.1.1.1"}}, wantUpdate: 1},
		{name: "ignore other type", getRecords: []provider.Record{{RecordId: "aaaa", DomainName: "example.com", RR: "nas", Type: "AAAA", Value: "::1"}}, wantUpdate: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operator := &fakeOperator{getErr: tt.getErr, getRecords: tt.getRecords}
			instance := &Provider{provider: &config.Provider{Name: "home", Provider: "aliyun"}, operator: operator}
			record := &config.Record{Name: "nas", IPVersion: provider.IPv4, TTL: 600}
			if err := instance.syncToProvider(context.Background(), "nas.example.com", record, netip.MustParseAddr("8.8.8.8")); err != nil {
				t.Fatal(err)
			}
			if len(operator.created) != tt.wantCreate || len(operator.updated) != tt.wantUpdate {
				t.Fatalf("created=%d updated=%d, want %d %d", len(operator.created), len(operator.updated), tt.wantCreate, tt.wantUpdate)
			}
		})
	}
}

func TestNotificationWorkerStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	instance := &Provider{
		provider: &config.Provider{Name: "home"},
		notifier: webhook.NewWebhook(&config.Webhook{URL: "http://invalid"}),
	}
	instance.startNotificationWorker(ctx)
	if cap(instance.notificationQueue) != 64 {
		t.Fatalf("queue capacity = %d, want 64", cap(instance.notificationQueue))
	}
	cancel()
	done := make(chan struct{})
	go func() {
		instance.notificationWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("notification worker did not stop")
	}
}
