//go:build windows

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	"golang.org/x/sys/windows"
)

func TestFlushAndDisconnectNamedPipeStillDisconnectsWhenFlushReportsBrokenPipe(t *testing.T) {
	var calls []string

	originalFlush := flushNamedPipeBuffers
	originalDisconnect := disconnectNamedPipe
	t.Cleanup(func() {
		flushNamedPipeBuffers = originalFlush
		disconnectNamedPipe = originalDisconnect
	})

	flushNamedPipeBuffers = func(_ windows.Handle) error {
		calls = append(calls, "flush")
		return windows.ERROR_BROKEN_PIPE
	}
	disconnectNamedPipe = func(_ windows.Handle) error {
		calls = append(calls, "disconnect")
		return windows.ERROR_PIPE_NOT_CONNECTED
	}

	sr := &serviceRunner{}
	sr.flushAndDisconnectNamedPipe(windows.InvalidHandle)

	if len(calls) != 2 {
		t.Fatalf("expected exactly two pipe calls, got %d (%v)", len(calls), calls)
	}
	if calls[0] != "flush" || calls[1] != "disconnect" {
		t.Fatalf("expected call order flush -> disconnect, got %v", calls)
	}
}

func TestNamedPipeStreamStatusReliability(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Configuration{
		AppDataPath:     tempDir,
		ServicePipeName: fmt.Sprintf("gorilla-test-%d", time.Now().UnixNano()),
		ServiceInterval: "1h",
		ServiceMode:     true,
		ServiceName:     "gorilla-test",
	}

	stubOptional(t, "Slack")
	sr := newServiceRunner(cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())

	if err := sr.start(ctx); err != nil {
		t.Fatalf("service start failed: %v", err)
	}
	defer func() {
		cancel()
		bestEffortUnblockPipeListener(cfg)
		sr.stop(context.Background())
	}()

	iterations := namedPipeReliabilityIterations(t)
	for i := 0; i < iterations; i++ {
		operationID := mustInstallAndGetOperationID(t, cfg, i)
		mustStreamAndReceiveTerminalEvent(t, cfg, operationID, i)
	}
}

func TestStreamOperationStatusUnknownOperationIDReturnsError(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Configuration{
		AppDataPath:     tempDir,
		ServicePipeName: fmt.Sprintf("gorilla-test-%d", time.Now().UnixNano()),
		ServiceInterval: "1h",
		ServiceMode:     true,
		ServiceName:     "gorilla-test",
	}

	sr := newServiceRunner(cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())

	if err := sr.start(ctx); err != nil {
		t.Fatalf("service start failed: %v", err)
	}
	defer func() {
		cancel()
		bestEffortUnblockPipeListener(cfg)
		sr.stop(context.Background())
	}()

	conn, err := openPipe(servicePipePath(cfg.ServicePipeName), 5*time.Second)
	if err != nil {
		t.Fatalf("failed to open service pipe: %v", err)
	}
	defer func() { _ = conn.Close() }()

	request := serviceEnvelope[streamOperationStatusRequest]{
		Version:      pipeProtocolVersion,
		MessageType:  messageTypeRequest,
		Operation:    actionStreamOperationStatus,
		RequestID:    "req-stream-unknown",
		OperationID:  "does-not-exist",
		TimestampUTC: nowRFC3339UTC(),
		Payload:      streamOperationStatusRequest{},
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		t.Fatalf("failed to encode stream request: %v", err)
	}

	var resp serviceEnvelope[errorResponsePayload]
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("failed to decode stream error response: %v", err)
	}
	if resp.MessageType != messageTypeError {
		t.Fatalf("expected messageType=%s, got %s", messageTypeError, resp.MessageType)
	}
	if resp.Payload.ErrorCode != "invalid_request" {
		t.Fatalf("expected errorCode=invalid_request, got %s", resp.Payload.ErrorCode)
	}
}

func TestStreamOperationStatusFailedLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Configuration{
		AppDataPath:     tempDir,
		ServicePipeName: fmt.Sprintf("gorilla-test-%d", time.Now().UnixNano()),
		ServiceInterval: "1h",
		ServiceMode:     true,
		ServiceName:     "gorilla-test",
	}

	stubOptional(t, "Slack")
	sr := newServiceRunner(cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		return nil, errors.New("forced managed run failure")
	})
	ctx, cancel := context.WithCancel(context.Background())

	if err := sr.start(ctx); err != nil {
		t.Fatalf("service start failed: %v", err)
	}
	defer func() {
		cancel()
		bestEffortUnblockPipeListener(cfg)
		sr.stop(context.Background())
	}()

	operationID := mustInstallAndGetOperationID(t, cfg, 0)
	terminal := mustStreamAndReceiveTerminalState(t, cfg, operationID, 0)
	if terminal.State != "Failed" {
		t.Fatalf("expected terminal state Failed, got %s", terminal.State)
	}
	if terminal.ErrorCode != "managed_run_failed" {
		t.Fatalf("expected errorCode managed_run_failed, got %s", terminal.ErrorCode)
	}
}

func TestScheduleRunAfterMutationEmitsCanceledTerminalEvent(t *testing.T) {
	sr := newServiceRunner(config.Configuration{}, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		return nil, nil
	})
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

func namedPipeReliabilityIterations(t *testing.T) int {
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

func mustInstallAndGetOperationID(t *testing.T, cfg config.Configuration, seq int) string {
	t.Helper()

	request := serviceEnvelope[installItemRequest]{
		Version:      pipeProtocolVersion,
		MessageType:  messageTypeRequest,
		Operation:    actionInstallItem,
		RequestID:    fmt.Sprintf("req-install-%d", seq),
		OperationID:  "",
		TimestampUTC: nowRFC3339UTC(),
		Payload: installItemRequest{
			ItemName: "Slack",
		},
	}

	response := sendOneRequest(t, cfg, request)
	if response.MessageType != messageTypeResponse {
		t.Fatalf("expected %s message type, got %s", messageTypeResponse, response.MessageType)
	}
	if response.Operation != actionInstallItem {
		t.Fatalf("expected operation %s, got %s", actionInstallItem, response.Operation)
	}
	if strings.TrimSpace(response.OperationID) == "" {
		t.Fatalf("expected non-empty operationId from install response")
	}

	return response.OperationID
}

func mustStreamAndReceiveTerminalEvent(t *testing.T, cfg config.Configuration, operationID string, seq int) {
	t.Helper()

	terminal := mustStreamAndReceiveTerminalState(t, cfg, operationID, seq)
	if terminal.State != "Succeeded" {
		t.Fatalf("expected terminal state Succeeded, got %s", terminal.State)
	}
}

func mustStreamAndReceiveTerminalState(t *testing.T, cfg config.Configuration, operationID string, seq int) OperationStatusPayload {
	t.Helper()

	conn, err := openPipe(servicePipePath(cfg.ServicePipeName), 5*time.Second)
	if err != nil {
		t.Fatalf("failed to open service pipe: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	request := serviceEnvelope[streamOperationStatusRequest]{
		Version:      pipeProtocolVersion,
		MessageType:  messageTypeRequest,
		Operation:    actionStreamOperationStatus,
		RequestID:    fmt.Sprintf("req-stream-%d", seq),
		OperationID:  operationID,
		TimestampUTC: nowRFC3339UTC(),
		Payload:      streamOperationStatusRequest{},
	}

	if err := json.NewEncoder(conn).Encode(request); err != nil {
		t.Fatalf("failed to encode stream request: %v", err)
	}
	decoder := json.NewDecoder(conn)

	var ack serviceEnvelope[json.RawMessage]
	if err := decoder.Decode(&ack); err != nil {
		t.Fatalf("failed to decode stream ack: %v", err)
	}
	if ack.MessageType != messageTypeResponse {
		t.Fatalf("expected stream ack messageType=%s, got %s", messageTypeResponse, ack.MessageType)
	}
	if ack.Operation != actionStreamOperationStatus {
		t.Fatalf("expected stream ack operation=%s, got %s", actionStreamOperationStatus, ack.Operation)
	}
	if ack.OperationID != operationID {
		t.Fatalf("expected stream ack operationId=%s, got %s", operationID, ack.OperationID)
	}

	states := make([]string, 0, 4)
	var terminal OperationStatusPayload
	for {
		var event serviceEnvelope[OperationStatusPayload]
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("failed to decode stream event: %v", err)
		}
		if event.MessageType != messageTypeEvent {
			t.Fatalf("expected stream event messageType=%s, got %s", messageTypeEvent, event.MessageType)
		}
		if event.Operation != actionStreamOperationStatus {
			t.Fatalf("expected stream event operation=%s, got %s", actionStreamOperationStatus, event.Operation)
		}
		if event.OperationID != operationID {
			t.Fatalf("expected stream event operationId=%s, got %s", operationID, event.OperationID)
		}
		states = append(states, event.Payload.State)
		if IsTerminalOperationState(event.Payload.State) {
			terminal = event.Payload
			break
		}
	}

	if len(states) < 2 {
		t.Fatalf("expected queued and terminal lifecycle states, got %v", states)
	}
	if states[0] != "Queued" {
		t.Fatalf("expected first state Queued, got %s (%v)", states[0], states)
	}
	return terminal
}

func sendOneRequest[T any](t *testing.T, cfg config.Configuration, req serviceEnvelope[T]) serviceEnvelope[json.RawMessage] {
	t.Helper()

	conn, err := openPipe(servicePipePath(cfg.ServicePipeName), 5*time.Second)
	if err != nil {
		t.Fatalf("failed to open service pipe: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("failed to encode request: %v", err)
	}

	var resp serviceEnvelope[json.RawMessage]
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

func bestEffortUnblockPipeListener(cfg config.Configuration) {
	conn, err := openPipe(servicePipePath(cfg.ServicePipeName), 250*time.Millisecond)
	if err != nil {
		return
	}
	_ = conn.Close()
}

func TestOperationProgressUsesActualItemsAndItemScopedPercent(t *testing.T) {
	sr := newServiceRunner(config.Configuration{}, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		return nil, nil
	})
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
	sr := newServiceRunner(config.Configuration{}, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		return nil, nil
	})
	now := time.Now()

	sr.operationsMu.Lock()
	for i := 0; i < trackedOperationsMaxCount+50; i++ {
		id := fmt.Sprintf("done-%d", i)
		sr.operations[id] = &trackedOperation{
			events:      []OperationStatusPayload{{State: "Succeeded", ProgressPercent: 100, Message: "done"}},
			done:        true,
			lastUpdated: now.Add(-time.Duration(i) * time.Minute),
			completedAt: now.Add(-time.Duration(i) * time.Minute),
		}
	}
	sr.operations["active-op"] = &trackedOperation{
		events:      []OperationStatusPayload{{State: "Installing", ProgressPercent: 60, Message: "running"}},
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
	cfg := config.Configuration{
		AppDataPath:     t.TempDir(),
		ServicePipeName: fmt.Sprintf("gorilla-test-%d", time.Now().UnixNano()),
		ServiceInterval: "1h",
		ServiceMode:     true,
		ServiceName:     "gorilla-test",
		Branding:        config.Branding{Title: "Acme Software Center", Accent: "#0B6E4F"},
	}
	release := make(chan struct{})
	sr := newServiceRunner(cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		<-release
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := sr.start(ctx); err != nil {
		t.Fatalf("service start failed: %v", err)
	}
	defer func() {
		close(release)
		cancel()
		bestEffortUnblockPipeListener(cfg)
		sr.stop(context.Background())
	}()

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
	sr := newServiceRunner(config.Configuration{}, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		return nil, nil
	})
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
// ten minutes behind a run.
func TestStopHonoursDeadlineWhileRunIsBusy(t *testing.T) {
	cfg := config.Configuration{
		AppDataPath:     t.TempDir(),
		ServicePipeName: fmt.Sprintf("gorilla-test-%d", time.Now().UnixNano()),
		ServiceInterval: "1h",
		ServiceMode:     true,
		ServiceName:     "gorilla-test",
	}
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
	bestEffortUnblockPipeListener(cfg)
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
	cfg := config.Configuration{
		AppDataPath:     t.TempDir(),
		ServicePipeName: fmt.Sprintf("gorilla-test-%d", time.Now().UnixNano()),
		ServiceInterval: "1h",
		ServiceMode:     true,
		ServiceName:     "gorilla-test",
	}
	stubOptional(t, "Slack")
	var runs atomic.Int32
	runStarted := make(chan struct{})
	release := make(chan struct{})
	sr := newServiceRunner(cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		// The start-up run returns at once; the run InstallItem schedules
		// stays busy, before its item, until the test releases it.
		if runs.Add(1) == 2 {
			close(runStarted)
			<-release
		}
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := sr.start(ctx); err != nil {
		t.Fatalf("service start failed: %v", err)
	}
	defer func() {
		cancel()
		bestEffortUnblockPipeListener(cfg)
		sr.stop(context.Background())
	}()

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
	terminal := mustStreamAndReceiveTerminalState(t, cfg, accepted.OperationID, 0)
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
		if err == nil || !strings.HasPrefix(err.Error(), "operation_not_cancelable:") {
			t.Fatalf("CancelOperation(%s) error = %v, want operation_not_cancelable", id, err)
		}
	}
}
