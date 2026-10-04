package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/installer"
	"github.com/1dustindavis/gorilla/pkg/report"
)

// These tests run the service core over the platform's real transport: the
// named pipe on Windows, a Unix domain socket elsewhere.

type managedRunFunc = func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error)

func noopRun(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
	return nil, nil
}

func testServiceConfig(t *testing.T) config.Configuration {
	t.Helper()
	return config.Configuration{
		AppDataPath:     t.TempDir(),
		ServicePipeName: testPipeName(t),
		ServiceInterval: "1h",
		ServiceMode:     true,
		ServiceName:     "gorilla-test",
	}
}

// startTestRunner starts a runner and stops it when the test ends.
func startTestRunner(t *testing.T, cfg config.Configuration, run managedRunFunc) (*serviceRunner, context.Context) {
	t.Helper()
	sr := newServiceRunner(cfg, run)
	ctx, cancel := context.WithCancel(context.Background())
	if err := sr.start(ctx); err != nil {
		cancel()
		t.Fatalf("service start failed: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		sr.stop(context.Background())
	})
	return sr, ctx
}

// rawExchange sends one raw request line and returns a reader for what the
// service sends back.
func rawExchange(t *testing.T, cfg config.Configuration, request string) *bufio.Reader {
	t.Helper()
	conn, err := dial(context.Background(), cfg.ServicePipeName, 5*time.Second)
	if err != nil {
		t.Fatalf("dial service: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := io.WriteString(conn, request+"\n"); err != nil {
		t.Fatalf("send request: %v", err)
	}
	return bufio.NewReader(conn)
}

func readResponse(t *testing.T, r *bufio.Reader) rpcResponse {
	t.Helper()
	var resp rpcResponse
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestServiceStreamStatusReliability(t *testing.T) {
	cfg := testServiceConfig(t)
	stubOptional(t, "Slack")
	_, ctx := startTestRunner(t, cfg, noopRun)

	for i := 0; i < reliabilityIterations(t); i++ {
		accepted, err := NewClient(cfg.ServicePipeName).InstallItem(ctx, "Slack")
		if err != nil {
			t.Fatalf("iteration %d: installItem: %v", i, err)
		}
		if terminal := mustStreamTerminal(t, cfg, accepted.OperationID); terminal.State != "Succeeded" {
			t.Fatalf("iteration %d: terminal state %s, want Succeeded", i, terminal.State)
		}
	}
}

func TestStreamOperationStatusUnknownOperationIDReturnsError(t *testing.T) {
	cfg := testServiceConfig(t)
	startTestRunner(t, cfg, noopRun)

	resp := readResponse(t, rawExchange(t, cfg, `{"jsonrpc":"2.0","id":"req-unknown","method":"streamOperationStatus","params":{"operationId":"does-not-exist"}}`))
	if resp.Error == nil || resp.Error.Code != codeUnknownOperation || resp.Error.Data.Code != "unknown_operation" || resp.Error.Data.OperationID != "does-not-exist" {
		t.Fatalf("response = %+v, want unknown_operation", resp)
	}
	if string(resp.ID) != `"req-unknown"` {
		t.Fatalf("response id = %s, want the request's", resp.ID)
	}
}

// Every malformed request gets the JSON-RPC error a generic client expects,
// with the stable data.code beside it.
func TestJSONRPCErrorResponses(t *testing.T) {
	cfg := testServiceConfig(t)
	stubOptional(t, "Slack")
	startTestRunner(t, cfg, noopRun)

	tests := []struct {
		name, request string
		code          int
		appCode, id   string
	}{
		{"parse error", `{"jsonrpc" "2.0"}`, codeParseError, "parse_error", "null"},
		{"batch", `[{"jsonrpc":"2.0","id":1,"method":"getServiceInfo"}]`, codeInvalidRequest, "invalid_request", "null"},
		{"wrong version", `{"jsonrpc":"1.0","id":7,"method":"getServiceInfo"}`, codeInvalidRequest, "invalid_request", "7"},
		{"object id", `{"jsonrpc":"2.0","id":{},"method":"getServiceInfo"}`, codeInvalidRequest, "invalid_request", "null"},
		{"unknown method", `{"jsonrpc":"2.0","id":"a","method":"InstallItem"}`, codeMethodNotFound, "method_not_found", `"a"`},
		{"missing params", `{"jsonrpc":"2.0","id":"b","method":"installItem"}`, codeInvalidParams, "invalid_params", `"b"`},
		{"wrong params", `{"jsonrpc":"2.0","id":"c","method":"cancelOperation","params":["op"]}`, codeInvalidParams, "invalid_params", `"c"`},
		{"not offered", `{"jsonrpc":"2.0","id":"d","method":"installItem","params":{"itemName":"NotOffered"}}`, codeItemNotAvailable, "item_not_available", `"d"`},
		{"not removable", `{"jsonrpc":"2.0","id":"e","method":"removeItem","params":{"itemName":"NotOffered"}}`, codeItemNotRemovable, "item_not_removable", `"e"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := readResponse(t, rawExchange(t, cfg, tt.request))
			if resp.JSONRPC != jsonrpcVersion || resp.Error == nil || resp.Error.Code != tt.code || resp.Error.Data.Code != tt.appCode || resp.Result != nil {
				t.Fatalf("response = %+v (error %+v), want %d/%s", resp, resp.Error, tt.code, tt.appCode)
			}
			if string(resp.ID) != tt.id {
				t.Fatalf("response id = %s, want %s", resp.ID, tt.id)
			}
		})
	}
}

// A request without an id is a notification: no response, and nothing done.
func TestNotificationGetsNoResponse(t *testing.T) {
	cfg := testServiceConfig(t)
	stubOptional(t, "Slack")
	startTestRunner(t, cfg, noopRun)

	r := rawExchange(t, cfg, `{"jsonrpc":"2.0","method":"installItem","params":{"itemName":"Slack"}}`)
	if line, err := r.ReadString('\n'); err == nil || line != "" {
		t.Fatalf("notification got a response: %q (err %v)", line, err)
	}
	if got := loadManifest(t, cfg).Installs; len(got) != 0 {
		t.Fatalf("a notification changed the selection: %v", got)
	}
}

// server_busy is sent before the request is read; the client must still read
// it as a service error (the v1 envelope here was unreadable).
func TestServerBusyIsReadableByClient(t *testing.T) {
	cfg := testServiceConfig(t)
	sr, ctx := startTestRunner(t, cfg, noopRun)
	for i := 0; i < cap(sr.handlerSem); i++ {
		sr.handlerSem <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(sr.handlerSem); i++ {
			<-sr.handlerSem
		}
	}()

	_, err := NewClient(cfg.ServicePipeName).GetServiceInfo(ctx)
	if !IsErrorCode(err, "server_busy") {
		t.Fatalf("GetServiceInfo with every handler busy = %v, want server_busy", err)
	}
}

// getServiceInfo skips the queue, so it answers while a run holds it, and
// reports that run.
func TestGetServiceInfoAnswersWhileRunIsBusy(t *testing.T) {
	cfg := testServiceConfig(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	_, ctx := startTestRunner(t, cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		once.Do(func() { close(started) })
		<-release
		return nil, nil
	})
	defer close(release)
	<-started

	info, err := NewClient(cfg.ServicePipeName).GetServiceInfo(ctx)
	if err != nil {
		t.Fatalf("GetServiceInfo: %v", err)
	}
	if !info.Busy || info.APIVersion != apiVersion || info.ProtocolVersion != "2.0" || !slices.Equal(info.Capabilities, capabilities) || info.UptimeSeconds < 0 {
		t.Fatalf("GetServiceInfo = %+v", info)
	}
}

func TestStreamOperationStatusFailedLifecycle(t *testing.T) {
	cfg := testServiceConfig(t)
	stubOptional(t, "Slack")
	_, ctx := startTestRunner(t, cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		return nil, errors.New("forced managed run failure")
	})

	accepted, err := NewClient(cfg.ServicePipeName).InstallItem(ctx, "Slack")
	if err != nil {
		t.Fatalf("installItem: %v", err)
	}
	terminal := mustStreamTerminal(t, cfg, accepted.OperationID)
	if terminal.State != "Failed" || terminal.ErrorCode != "managed_run_failed" {
		t.Fatalf("terminal = %+v, want Failed/managed_run_failed", terminal)
	}
}

func TestScheduleRunAfterMutationEmitsCanceledTerminalEvent(t *testing.T) {
	sr := newServiceRunner(config.Configuration{}, noopRun)
	operationID := "op-canceled"
	sr.registerTrackedOperation("Slack", CommandResponse{OperationID: operationID})

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	sr.scheduleRunAfterMutation(canceledCtx, actionInstallItem, "Slack", operationID)
	sr.wg.Wait()

	events, done, ok := sr.snapshotTrackedOperation(operationID)
	if !ok {
		t.Fatalf("expected tracked operation to exist")
	}
	if !done {
		t.Fatalf("expected tracked operation to be marked done")
	}
	last := events[len(events)-1]
	if last.State != "Canceled" {
		t.Fatalf("expected terminal state Canceled, got %s", last.State)
	}
	if last.CanceledBy != "service" {
		t.Fatalf("expected canceledBy=service, got %s", last.CanceledBy)
	}
}

// TestResolveTerminalEvent verifies the honest terminal event is derived from
// the run report by catalog name (R10): failed → Failed, deferred → Deferred,
// neither (and a nil report) → Succeeded.
func TestResolveTerminalEvent(t *testing.T) {
	rep := report.New()
	rep.FailedItems = append(rep.FailedItems, report.FailedItem{Name: "DemoFailing", Error: "boom"})
	rep.DeferredItems = append(rep.DeferredItems, report.DeferredItem{Name: "DemoBlocked", Reason: "blocking application(s) running: notepad"})

	if ev := resolveTerminalEvent("DemoFailing", rep); ev.State != "Failed" || ev.ErrorCode != "item_failed" || ev.ErrorMessage != "boom" {
		t.Errorf("failed mapping wrong: %#v", ev)
	}
	if ev := resolveTerminalEvent("DemoBlocked", rep); ev.State != "Deferred" || ev.ErrorCode != "blocked_by_running_app" {
		t.Errorf("deferred mapping wrong: %#v", ev)
	}
	if ev := resolveTerminalEvent("DemoOptional", rep); ev.State != "Succeeded" {
		t.Errorf("neither should be Succeeded, got %#v", ev)
	}
	if ev := resolveTerminalEvent("DemoOptional", nil); ev.State != "Succeeded" {
		t.Errorf("nil report should be Succeeded, got %#v", ev)
	}
}

func reliabilityIterations(t *testing.T) int {
	t.Helper()

	const (
		defaultIterations = 10
		shortIterations   = 2
		envKey            = "GORILLA_SERVICE_PIPE_RELIABILITY_ITERATIONS"
	)

	iterations := defaultIterations
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			t.Fatalf("invalid %s value %q: expected positive integer", envKey, value)
		}
		iterations = parsed
	}

	if testing.Short() && iterations > shortIterations {
		return shortIterations
	}

	return iterations
}

// mustStreamTerminal streams operationID over a raw connection, checks the
// ack and the notifications' framing and numbering, and returns the terminal
// record.
func mustStreamTerminal(t *testing.T, cfg config.Configuration, operationID string) OperationStatus {
	t.Helper()

	r := rawExchange(t, cfg, fmt.Sprintf(`{"jsonrpc":"2.0","id":"req-stream","method":"streamOperationStatus","params":{"operationId":%q}}`, operationID))
	dec := json.NewDecoder(r)
	var ack rpcResponse
	if err := dec.Decode(&ack); err != nil {
		t.Fatalf("decode stream ack: %v", err)
	}
	if ack.Error != nil || string(ack.ID) != `"req-stream"` || string(ack.Result) != `{"streamAccepted":true}` {
		t.Fatalf("stream ack = %+v (error %+v)", ack, ack.Error)
	}

	var records []OperationStatus
	for {
		var note struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  OperationStatus `json:"params"`
		}
		if err := dec.Decode(&note); err != nil {
			t.Fatalf("decode stream notification: %v", err)
		}
		if note.JSONRPC != jsonrpcVersion || note.ID != nil || note.Method != notificationOperationStatus || note.Params.OperationID != operationID {
			t.Fatalf("unexpected stream message %+v", note)
		}
		if note.Params.Seq != len(records)+1 {
			t.Fatalf("record seq %d, want %d", note.Params.Seq, len(records)+1)
		}
		if _, err := time.Parse(time.RFC3339Nano, note.Params.TimestampUTC); err != nil || !strings.Contains(note.Params.TimestampUTC, ".") {
			t.Fatalf("timestamp %q is not sub-second RFC 3339", note.Params.TimestampUTC)
		}
		records = append(records, note.Params)
		if IsTerminalOperationState(note.Params.State) {
			break
		}
	}
	if len(records) < 2 || records[0].State != "Queued" {
		t.Fatalf("expected Queued then a terminal record, got %+v", records)
	}
	return records[len(records)-1]
}

func TestOperationProgressUsesActualItemsAndItemScopedPercent(t *testing.T) {
	sr := newServiceRunner(config.Configuration{}, noopRun)
	const operationID = "op-progress"
	sr.registerTrackedOperation("DemoOptional", CommandResponse{OperationID: operationID})

	emit := sr.operationProgressCallback(operationID)
	emit(catalog.Item{Name: "DemoDependency", DisplayName: "Demo Dependency"}, "downloading", 0, "download")
	emit(catalog.Item{Name: "DemoDependency", DisplayName: "Demo Dependency"}, "installing", 50, "install")
	emit(catalog.Item{Name: "DemoDependency", DisplayName: "Demo Dependency"}, "done", 100, "")
	emit(catalog.Item{Name: "DemoOptional", DisplayName: "Demo Optional"}, "downloading", 0, "download")
	emit(catalog.Item{Name: "DemoOptional", DisplayName: "Demo Optional"}, "installing", 50, "install")
	emit(catalog.Item{Name: "DemoOptional", DisplayName: "Demo Optional"}, "done", 100, "")
	emit(catalog.Item{Name: "DemoUpdater", DisplayName: "Demo Updater"}, "downloading", 0, "download")
	emit(catalog.Item{Name: "DemoUpdater", DisplayName: "Demo Updater"}, "failed", 50, "boom")
	emit(catalog.Item{Name: "DemoOptional", DisplayName: "Demo Optional"}, "removing", 50, "remove")

	events, done, ok := sr.snapshotTrackedOperation(operationID)
	if !ok || done {
		t.Fatalf("ItemFailed must leave operation active: ok=%v done=%v", ok, done)
	}
	wantStates := []string{"Queued", "Downloading", "Installing", "ItemCompleted", "Downloading", "Installing", "ItemCompleted", "Downloading", "ItemFailed", "Removing"}
	for i, want := range wantStates {
		if events[i].State != want {
			t.Fatalf("event %d state=%q, want %q", i, events[i].State, want)
		}
		// Every record is numbered in the order it was recorded.
		if events[i].Seq != i+1 || events[i].OperationID != operationID {
			t.Fatalf("event %d seq=%d operationId=%q", i, events[i].Seq, events[i].OperationID)
		}
	}
	if events[3].ProgressPercent != 100 || events[4].ProgressPercent != 0 {
		t.Fatalf("expected percent reset at item boundary, got %d -> %d", events[3].ProgressPercent, events[4].ProgressPercent)
	}
	if events[1].ItemName != "DemoDependency" || events[1].DisplayName != "Demo Dependency" {
		t.Fatalf("dependency identity lost: %#v", events[1])
	}
	if events[7].ItemName != "DemoUpdater" || events[7].DisplayName != "Demo Updater" {
		t.Fatalf("updater identity lost: %#v", events[7])
	}

	sr.appendOperationEvent(operationID, resolveTerminalEvent("DemoOptional", nil))
	events, done, _ = sr.snapshotTrackedOperation(operationID)
	terminal := events[len(events)-1]
	// The terminal record names the item as its progress records did, not by
	// its catalog key (Activity read "GoogleChrome: Succeeded" otherwise).
	if !done || terminal.ItemName != "DemoOptional" || terminal.DisplayName != "Demo Optional" {
		t.Fatalf("terminal requested-item identity missing: done=%v event=%#v", done, terminal)
	}
	if events[0].ItemName != "DemoOptional" || events[0].DisplayName != "DemoOptional" {
		t.Fatalf("queued requested-item identity missing: %#v", events[0])
	}
}

func TestTrackedOperationPruningDropsOldCompletedEntries(t *testing.T) {
	sr := newServiceRunner(config.Configuration{}, noopRun)
	now := time.Now()

	sr.operationsMu.Lock()
	for i := 0; i < trackedOperationsMaxCount+50; i++ {
		id := fmt.Sprintf("done-%d", i)
		sr.operations[id] = &trackedOperation{
			events:      []OperationStatus{{State: "Succeeded", ProgressPercent: 100, Message: "done"}},
			done:        true,
			lastUpdated: now.Add(-time.Duration(i) * time.Minute),
			completedAt: now.Add(-time.Duration(i) * time.Minute),
		}
	}
	sr.operations["active-op"] = &trackedOperation{
		events:      []OperationStatus{{State: "Installing", ProgressPercent: 60, Message: "running"}},
		done:        false,
		lastUpdated: now,
	}
	sr.pruneTrackedOperationsLocked(now)
	_, activeStillTracked := sr.operations["active-op"]
	count := len(sr.operations)
	sr.operationsMu.Unlock()

	if !activeStillTracked {
		t.Fatalf("expected active operation to remain tracked after pruning")
	}
	if count > trackedOperationsMaxCount {
		t.Fatalf("expected tracked operations count <= %d, got %d", trackedOperationsMaxCount, count)
	}
}

// GetBranding must answer while a managed run holds the command queue: the UI
// asks for it before opening its window.
func TestGetBrandingAnswersWhileRunIsBusy(t *testing.T) {
	cfg := testServiceConfig(t)
	cfg.Branding = config.Branding{Title: "Acme Software Center", Accent: "#0B6E4F"}
	release := make(chan struct{})
	_, ctx := startTestRunner(t, cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		<-release
		return nil, nil
	})
	defer close(release)

	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
	defer callCancel()
	got, err := NewClient(cfg.ServicePipeName).GetBranding(callCtx)
	if err != nil {
		t.Fatalf("GetBranding failed: %v", err)
	}
	if got.Title != "Acme Software Center" || got.Accent != "#0b6e4f" {
		t.Fatalf("GetBranding = %#v", got)
	}
}

// An InstallItem resolves the catalog display name while authorizing, so even
// a run that never emits progress for the item names it properly at the end.
func TestTrackedOperationUsesRegisteredDisplayName(t *testing.T) {
	sr := newServiceRunner(config.Configuration{}, noopRun)
	sr.registerTrackedOperation("GoogleChrome", CommandResponse{OperationID: "op-named", displayName: "Google Chrome"})
	sr.appendOperationEvent("op-named", resolveTerminalEvent("GoogleChrome", nil))

	events, done, _ := sr.snapshotTrackedOperation("op-named")
	if !done || len(events) != 2 {
		t.Fatalf("expected queued and terminal records, done=%v events=%#v", done, events)
	}
	for _, event := range events {
		if event.ItemName != "GoogleChrome" || event.DisplayName != "Google Chrome" {
			t.Fatalf("record identity = %q/%q, want GoogleChrome/Google Chrome", event.ItemName, event.DisplayName)
		}
	}
}

// A managed run that is still busy must not hold a service stop past its
// deadline: Stop-Service once sat at "Waiting for service to stop" for over
// ten minutes behind a run. Nothing connects first, as with the real Service
// Control Manager, so the listener is blocked in Accept (on Windows, in
// ConnectNamedPipe, which only the self-connect in Close wakes).
func TestStopHonoursDeadlineWhileRunIsBusy(t *testing.T) {
	cfg := testServiceConfig(t)
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	var once sync.Once
	sr := newServiceRunner(cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		once.Do(func() { close(started) })
		<-release
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := sr.start(ctx); err != nil {
		t.Fatalf("service start failed: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the start-up run never began")
	}

	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stopCancel()
	begin := time.Now()
	sr.stop(stopCtx)
	if took := time.Since(begin); took > 2*time.Second {
		t.Fatalf("stop waited %v for a busy run; want it to return at its deadline", took)
	}
}

// CancelOperation is accepted while the operation's run has not reached its
// item, even though that run holds the command queue; it ends the operation
// with a user Canceled record and reverts the selection. A second cancel of
// the now finished operation, or one of an unknown id, is refused.
func TestCancelOperationAcceptedWhileQueuedThenRefused(t *testing.T) {
	cfg := testServiceConfig(t)
	stubOptional(t, "Slack")
	var runs atomic.Int32
	runStarted := make(chan struct{})
	release := make(chan struct{})
	sr, ctx := startTestRunner(t, cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		// The start-up run returns at once; the run InstallItem schedules
		// stays busy, before its item, until the test releases it.
		if runs.Add(1) == 2 {
			close(runStarted)
			<-release
		}
		return nil, nil
	})

	client := NewClient(cfg.ServicePipeName)
	accepted, err := client.InstallItem(ctx, "Slack")
	if err != nil {
		t.Fatalf("InstallItem failed: %v", err)
	}
	select {
	case <-runStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the scheduled run never started")
	}

	if err := client.CancelOperation(ctx, accepted.OperationID); err != nil {
		t.Fatalf("CancelOperation while queued failed: %v", err)
	}
	terminal := mustStreamTerminal(t, cfg, accepted.OperationID)
	if terminal.State != "Canceled" || terminal.CanceledBy != "user" || terminal.ItemName != "Slack" || terminal.DisplayName == "" {
		t.Fatalf("terminal record = %#v, want Canceled by user for Slack", terminal)
	}
	if got := loadManifest(t, cfg).Installs; len(got) != 0 {
		t.Fatalf("the cancel left the selection in place: %v", got)
	}

	// The run finishing afterwards must not add a second terminal record.
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for busy, _ := sr.busyAction.Load().(string); busy != ""; busy, _ = sr.busyAction.Load().(string) {
		if time.Now().After(deadline) {
			t.Fatal("the released run never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The scheduled goroutine appends its outcome just after the queue frees.
	time.Sleep(200 * time.Millisecond)
	events, _, _ := sr.snapshotTrackedOperation(accepted.OperationID)
	if last := events[len(events)-1]; last.State != "Canceled" {
		t.Fatalf("a record followed the user cancel: %#v", last)
	}

	for _, id := range []string{accepted.OperationID, "does-not-exist"} {
		err := client.CancelOperation(ctx, id)
		var rpcErr *Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != codeOperationNotCancelable || rpcErr.Data.OperationID != id ||
			!strings.HasPrefix(err.Error(), "operation_not_cancelable:") {
			t.Fatalf("CancelOperation(%s) error = %v, want operation_not_cancelable", id, err)
		}
	}
}

// InstallItem and RemoveItem answer while a run holds the command queue: they
// only write the selection. Waiting behind the run used to hit the client's
// 30 s timeout on long installs while the request still went through later.
func TestMutationReturnsWhileRunIsBusy(t *testing.T) {
	cfg := testServiceConfig(t)
	stubOptional(t, "Slack")
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	sr, ctx := startTestRunner(t, cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		once.Do(func() { close(started) })
		<-release
		return nil, nil
	})
	defer close(release)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the start-up run never began")
	}

	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
	defer callCancel()
	client := NewClient(cfg.ServicePipeName)
	accepted, err := client.InstallItem(callCtx, "Slack")
	if err != nil {
		t.Fatalf("InstallItem behind a busy run failed: %v", err)
	}
	if got := loadManifest(t, cfg).Installs; !slices.Equal(got, []string{"Slack"}) {
		t.Fatalf("the selection was not written at once: %v", got)
	}
	events, done, ok := sr.snapshotTrackedOperation(accepted.OperationID)
	if !ok || done || events[len(events)-1].State != "Queued" {
		t.Fatalf("operation should be open and Queued: ok=%v done=%v events=%#v", ok, done, events)
	}
	if _, err := client.RemoveItem(callCtx, "Slack"); err != nil {
		t.Fatalf("RemoveItem behind a busy run failed: %v", err)
	}
}

// `gorilla -S` prints what SendCommand returns, and the manual-test scripts
// parse it: one JSON line per item or status record, and the operationId of an
// accepted mutation.
func TestSendCommandOverTransport(t *testing.T) {
	cfg := testServiceConfig(t)
	stubOptional(t, "Slack")
	startTestRunner(t, cfg, noopRun)

	info, err := SendCommand(cfg, "GetServiceInfo")
	if err != nil || len(info.Items) != 1 || !strings.Contains(info.Items[0], `"protocolVersion":"2.0"`) {
		t.Fatalf("GetServiceInfo = %+v, %v", info, err)
	}
	accepted, err := SendCommand(cfg, "InstallItem:Slack")
	if err != nil || accepted.OperationID == "" {
		t.Fatalf("InstallItem = %+v, %v", accepted, err)
	}
	stream, err := SendCommand(cfg, "StreamOperationStatus:"+accepted.OperationID)
	if err != nil || len(stream.Items) < 2 {
		t.Fatalf("StreamOperationStatus = %+v, %v", stream, err)
	}
	var last OperationStatus
	if err := json.Unmarshal([]byte(stream.Items[len(stream.Items)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last.OperationID != accepted.OperationID || last.State != "Succeeded" || last.Seq != len(stream.Items) {
		t.Fatalf("last status line = %+v", last)
	}
	if _, err := SendCommand(cfg, "CancelOperation:"+accepted.OperationID); !IsErrorCode(err, "operation_not_cancelable") {
		t.Fatalf("CancelOperation of a finished operation = %v", err)
	}
}
