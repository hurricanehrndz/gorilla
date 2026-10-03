package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/1dustindavis/gorilla/pkg/branding"
	gorillaservice "github.com/1dustindavis/gorilla/pkg/service"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type fakeServiceClient struct {
	items       []gorillaservice.OptionalInstallItem
	branding    branding.Branding
	accepted    gorillaservice.AcceptedOperation
	listCalls   int
	installName string
	removeName  string
	streamID    string
	stream      func(context.Context, func(gorillaservice.OperationStatus) error) error
}

func (f *fakeServiceClient) ListOptionalInstalls(context.Context) ([]gorillaservice.OptionalInstallItem, error) {
	f.listCalls++
	return f.items, nil
}

func (f *fakeServiceClient) GetBranding(context.Context) (branding.Branding, error) {
	return f.branding, nil
}

func (f *fakeServiceClient) InstallItem(_ context.Context, itemName string) (gorillaservice.AcceptedOperation, error) {
	f.installName = itemName
	return f.accepted, nil
}

func (f *fakeServiceClient) RemoveItem(_ context.Context, itemName string) (gorillaservice.AcceptedOperation, error) {
	f.removeName = itemName
	return f.accepted, nil
}

func (f *fakeServiceClient) StreamOperationStatus(ctx context.Context, operationID string, callback func(gorillaservice.OperationStatus) error) error {
	f.streamID = operationID
	return f.stream(ctx, callback)
}

func TestUIServiceValidationAndForwarding(t *testing.T) {
	client := &fakeServiceClient{
		items:    []gorillaservice.OptionalInstallItem{{ItemName: "demo", DisplayName: "Demo"}},
		accepted: gorillaservice.AcceptedOperation{OperationID: "op-1", Accepted: true},
		branding: branding.Branding{Title: "Acme"},
	}
	service := &UIService{client: client, logger: discardLogger(), ctx: context.Background()}

	if b, err := service.GetBranding(); err != nil || b.Title != "Acme" {
		t.Fatalf("branding forwarding failed: %#v %v", b, err)
	}

	items, err := service.ListOptionalInstalls()
	if err != nil || len(items) != 1 || client.listCalls != 1 {
		t.Fatalf("list forwarding failed: items=%#v calls=%d err=%v", items, client.listCalls, err)
	}
	if accepted, err := service.InstallItem(" demo "); err != nil || accepted.OperationID != "op-1" || client.installName != "demo" {
		t.Fatalf("install forwarding failed: accepted=%#v name=%q err=%v", accepted, client.installName, err)
	}
	if accepted, err := service.RemoveItem(" demo "); err != nil || accepted.OperationID != "op-1" || client.removeName != "demo" {
		t.Fatalf("remove forwarding failed: accepted=%#v name=%q err=%v", accepted, client.removeName, err)
	}

	client.installName = ""
	client.removeName = ""
	if _, err := service.InstallItem(" \t"); err == nil || client.installName != "" {
		t.Fatal("blank install item was forwarded")
	}
	if _, err := service.RemoveItem(""); err == nil || client.removeName != "" {
		t.Fatal("blank remove item was forwarded")
	}
	if err := service.WatchOperation(" "); err == nil || client.streamID != "" {
		t.Fatal("blank operation ID was forwarded")
	}
}

// A bound call before ServiceStartup must error rather than pass a nil context
// to the pipe client, which panics on Windows.
func TestUIServiceWithoutStartupRejectsCalls(t *testing.T) {
	client := &fakeServiceClient{stream: func(context.Context, func(gorillaservice.OperationStatus) error) error {
		t.Fatal("stream called without a running service")
		return nil
	}}
	service := &UIService{client: client, logger: discardLogger()}

	if _, err := service.ListOptionalInstalls(); err == nil || client.listCalls != 0 {
		t.Fatalf("list forwarded without startup: calls=%d err=%v", client.listCalls, err)
	}
	if _, err := service.InstallItem("demo"); err == nil || client.installName != "" {
		t.Fatalf("install forwarded without startup: name=%q err=%v", client.installName, err)
	}
	if _, err := service.RemoveItem("demo"); err == nil || client.removeName != "" {
		t.Fatalf("remove forwarded without startup: name=%q err=%v", client.removeName, err)
	}
	if err := service.WatchOperation("op-1"); err == nil || client.streamID != "" {
		t.Fatalf("watch forwarded without startup: id=%q err=%v", client.streamID, err)
	}
	if _, err := service.GetBranding(); err == nil {
		t.Fatal("branding forwarded without startup")
	}
}

func TestUIServiceWatchEmitsOperationStatus(t *testing.T) {
	app := testApplication()
	status := gorillaservice.OperationStatus{OperationID: "op-2", ItemName: "demo", DisplayName: "Demo", State: "Succeeded"}
	client := &fakeServiceClient{stream: func(_ context.Context, callback func(gorillaservice.OperationStatus) error) error {
		return callback(status)
	}}
	service := &UIService{client: client, logger: discardLogger()}
	if err := service.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}

	events := make(chan gorillaservice.OperationStatus, 1)
	removeListener := app.Event.On(operationStatusEvent, func(event *application.CustomEvent) {
		events <- event.Data.(gorillaservice.OperationStatus)
	})
	defer removeListener()

	if err := service.WatchOperation(" op-2 "); err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	select {
	case got := <-events:
		if got.OperationID != "op-2" || got.State != "Succeeded" || client.streamID != "op-2" {
			t.Fatalf("unexpected emitted status: %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for operation status event")
	}
}

// Every streamed record must reach the frontend carrying its own operation ID
// and item identity, including the non-terminal dependency records.
func TestUIServiceWatchEmitsEveryRecordWithOperationID(t *testing.T) {
	app := testApplication()
	records := []gorillaservice.OperationStatus{
		{OperationID: "op-many", ItemName: "DemoOptional", DisplayName: "Demo Optional", State: "Queued"},
		{OperationID: "op-many", ItemName: "DemoUpdater", DisplayName: "Demo Updater", State: "Installing", ProgressPercent: 10},
		{OperationID: "op-many", ItemName: "DemoUpdater", DisplayName: "Demo Updater", State: "ItemFailed"},
		{OperationID: "op-many", ItemName: "DemoOptional", DisplayName: "Demo Optional", State: "Installing", ProgressPercent: 5},
		{OperationID: "op-many", ItemName: "DemoOptional", DisplayName: "Demo Optional", State: "Succeeded"},
	}
	client := &fakeServiceClient{stream: func(_ context.Context, callback func(gorillaservice.OperationStatus) error) error {
		for _, record := range records {
			if err := callback(record); err != nil {
				return err
			}
		}
		return nil
	}}
	service := &UIService{client: client, logger: discardLogger(), ctx: context.Background(), app: app}

	events := make(chan gorillaservice.OperationStatus, len(records))
	removeListener := app.Event.On(operationStatusEvent, func(event *application.CustomEvent) {
		events <- event.Data.(gorillaservice.OperationStatus)
	})
	defer removeListener()

	if err := service.WatchOperation("op-many"); err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	// Wails dispatches each listener callback in its own goroutine, so delivery
	// order is not guaranteed; assert that every record arrives instead.
	emitted := map[gorillaservice.OperationStatus]bool{}
	for range records {
		select {
		case got := <-events:
			emitted[got] = true
		case <-time.After(time.Second):
			t.Fatalf("timed out after %d of %d records", len(emitted), len(records))
		}
	}
	for _, want := range records {
		if !emitted[want] {
			t.Fatalf("record was never emitted: %#v", want)
		}
	}
}

// A stream failure must surface as an error; the UI decides what to display and
// must never receive a fabricated terminal record.
func TestUIServiceWatchErrorEmitsNoTerminalEvent(t *testing.T) {
	app := testApplication()
	progress := gorillaservice.OperationStatus{OperationID: "op-broken", ItemName: "DemoOptional", DisplayName: "Demo Optional", State: "Downloading", ProgressPercent: 40}
	want := errors.New("operation stream ended before a terminal event")
	client := &fakeServiceClient{stream: func(_ context.Context, callback func(gorillaservice.OperationStatus) error) error {
		if err := callback(progress); err != nil {
			return err
		}
		return want
	}}
	service := &UIService{client: client, logger: discardLogger(), ctx: context.Background(), app: app}

	events := make(chan gorillaservice.OperationStatus, 4)
	removeListener := app.Event.On(operationStatusEvent, func(event *application.CustomEvent) {
		events <- event.Data.(gorillaservice.OperationStatus)
	})
	defer removeListener()

	if err := service.WatchOperation("op-broken"); !errors.Is(err, want) {
		t.Fatalf("expected the stream error, got %v", err)
	}
	select {
	case got := <-events:
		if got != progress {
			t.Fatalf("emitted %#v, want the single progress record %#v", got, progress)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the progress record")
	}
	select {
	case got := <-events:
		t.Fatalf("a failed stream emitted an extra event: %#v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestUIServiceWatchCancellationAndError(t *testing.T) {
	app := testApplication()

	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		started := make(chan struct{})
		client := &fakeServiceClient{stream: func(ctx context.Context, _ func(gorillaservice.OperationStatus) error) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}}
		service := &UIService{client: client, logger: discardLogger()}
		if err := service.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- service.WatchOperation("op-cancel") }()
		<-started
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	})

	t.Run("client error", func(t *testing.T) {
		want := errors.New("stream failed")
		client := &fakeServiceClient{stream: func(context.Context, func(gorillaservice.OperationStatus) error) error { return want }}
		service := &UIService{client: client, logger: discardLogger(), ctx: context.Background(), app: app}
		if err := service.WatchOperation("op-error"); !errors.Is(err, want) {
			t.Fatalf("expected stream error, got %v", err)
		}
	})
}

var (
	appOnce sync.Once
	testApp *application.App
)

func testApplication() *application.App {
	appOnce.Do(func() {
		testApp = application.New(application.Options{
			Logger: discardLogger(),
			Assets: application.AssetOptions{Handler: http.NotFoundHandler()},
		})
	})
	return testApp
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The window opens with the branded title, and falls back to the default when
// the service is down or nothing is configured.
func TestWindowTitle(t *testing.T) {
	get := func(b branding.Branding, err error) func(context.Context) (branding.Branding, error) {
		return func(context.Context) (branding.Branding, error) { return b, err }
	}
	for _, tt := range []struct {
		get  func(context.Context) (branding.Branding, error)
		want string
	}{
		{get(branding.Branding{Title: "Acme Software Center"}, nil), "Acme Software Center"},
		{get(branding.Branding{}, nil), defaultWindowTitle},
		{get(branding.Branding{Title: "ignored"}, errors.New("pipe down")), defaultWindowTitle},
	} {
		if got := windowTitle(tt.get, discardLogger()); got != tt.want {
			t.Errorf("windowTitle = %q, want %q", got, tt.want)
		}
	}
}
