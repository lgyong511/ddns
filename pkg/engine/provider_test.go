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
		{name: "create target type when only other type exists", getRecords: []provider.Record{{RecordId: "aaaa", DomainName: "example.com", RR: "nas", Type: "AAAA", Value: "::1"}}, wantCreate: 1},
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

func TestSendNotificationDropsWhenQueueFull(t *testing.T) {
	instance := &Provider{
		provider:          &config.Provider{Name: "home"},
		notifier:          webhook.NewWebhook(&config.Webhook{URL: "https://example.com"}),
		notificationQueue: make(chan webhook.WebhookData, 1),
	}
	instance.notificationQueue <- webhook.WebhookData{Domain: "first.example.com"}
	instance.sendNotification(context.Background(), &webhook.WebhookData{Domain: "second.example.com"})
	if len(instance.notificationQueue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(instance.notificationQueue))
	}
	if got := (<-instance.notificationQueue).Domain; got != "first.example.com" {
		t.Fatalf("queued notification = %q, want first notification", got)
	}
}

func TestNotificationWorkerSendsQueuedNotification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notifier := &fakeNotificationSender{sent: make(chan webhook.WebhookData, 1)}
	instance := &Provider{
		provider: &config.Provider{Name: "home"},
		notifier: notifier,
	}
	instance.startNotificationWorker(ctx)
	instance.sendNotification(ctx, &webhook.WebhookData{Domain: "nas.example.com"})

	select {
	case data := <-notifier.sent:
		if data.Domain != "nas.example.com" {
			t.Fatalf("sent domain = %q, want nas.example.com", data.Domain)
		}
	case <-time.After(time.Second):
		t.Fatal("notification was not sent")
	}
	cancel()
	waitNotificationWorker(t, instance)
}

func TestNotificationWorkerCancelsInFlightNotification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	notifier := &fakeNotificationSender{started: make(chan struct{}), waitForContext: true}
	instance := &Provider{
		provider: &config.Provider{Name: "home"},
		notifier: notifier,
	}
	instance.startNotificationWorker(ctx)
	instance.sendNotification(ctx, &webhook.WebhookData{Domain: "nas.example.com"})

	select {
	case <-notifier.started:
	case <-time.After(time.Second):
		cancel()
		waitNotificationWorker(t, instance)
		t.Fatal("notification request did not start")
	}
	cancel()
	waitNotificationWorker(t, instance)
}

func waitNotificationWorker(t *testing.T, instance *Provider) {
	t.Helper()
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

type fakeNotificationSender struct {
	sent           chan webhook.WebhookData
	started        chan struct{}
	waitForContext bool
}

func (f *fakeNotificationSender) Send(ctx context.Context, data *webhook.WebhookData) error {
	if f.started != nil {
		close(f.started)
	}
	if f.sent != nil {
		f.sent <- *data
	}
	if f.waitForContext {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
