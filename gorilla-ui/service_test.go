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

	gorillaservice "github.com/1dustindavis/gorilla/pkg/service"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type fakeServiceClient struct {
	items       []gorillaservice.OptionalInstallItem
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
	}
	service := &UIService{client: client, logger: discardLogger(), ctx: context.Background()}

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
