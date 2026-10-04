//go:build windows

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/1dustindavis/gorilla/pkg/branding"
	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/gorillalog"
	"github.com/1dustindavis/gorilla/pkg/installer"
	"github.com/1dustindavis/gorilla/pkg/report"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

type queuedCommand struct {
	cmd    Command
	result chan queuedResult
}

type queuedResult struct {
	resp CommandResponse
	err  error
}

type serviceRunner struct {
	cfg          config.Configuration
	managedRun   func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error)
	queue        chan queuedCommand
	handlerSem   chan struct{}
	wg           sync.WaitGroup
	execMutex    sync.Mutex
	activeConnMu sync.Mutex
	activeConns  map[windows.Handle]struct{}
	operationsMu sync.Mutex
	operations   map[string]*trackedOperation
	// busyAction is the command the queue worker is executing, "" when idle;
	// stop logs it when it gives up waiting.
	busyAction atomic.Value
	// cancels lets CancelOperation withdraw an item from the run under way.
	cancels *installer.Cancels
}

var (
	flushNamedPipeBuffers = windows.FlushFileBuffers
	disconnectNamedPipe   = windows.DisconnectNamedPipe
	streamPollSleep       = 20 * time.Millisecond
)

const (
	maxConcurrentPipeHandlers    = 32
	trackedOperationsMaxCount    = 512
	trackedCompletedOperationTTL = 24 * time.Hour
)

type trackedOperation struct {
	events               []OperationStatusPayload
	requestedItemName    string
	requestedDisplayName string
	done                 bool
	lastUpdated          time.Time
	completedAt          time.Time

	// prior and requested are the self-serve selections before and after the
	// request, for CancelOperation to revert.
	prior, requested selection
}

func newServiceRunner(cfg config.Configuration, managedRun func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error)) *serviceRunner {
	return &serviceRunner{
		cfg:         cfg,
		managedRun:  managedRun,
		queue:       make(chan queuedCommand),
		handlerSem:  make(chan struct{}, maxConcurrentPipeHandlers),
		activeConns: make(map[windows.Handle]struct{}),
		operations:  make(map[string]*trackedOperation),
		cancels:     installer.NewCancels(),
	}
}

func (sr *serviceRunner) start(ctx context.Context) error {
	if err := gorillalog.NewLog(sr.cfg); err != nil {
		return fmt.Errorf("initialize logger: %w", err)
	}

	interval, err := time.ParseDuration(sr.cfg.ServiceInterval)
	if err != nil || interval <= 0 {
		return fmt.Errorf("invalid service interval %q: %w", sr.cfg.ServiceInterval, err)
	}

	sr.wg.Add(1)
	go func() {
		defer sr.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case queued := <-sr.queue:
				sr.busyAction.Store(queued.cmd.Action)
				sr.execMutex.Lock()
				resp, err := sr.executeCommandSafe(queued.cmd)
				sr.execMutex.Unlock()
				sr.busyAction.Store("")
				queued.result <- queuedResult{resp: resp, err: err}
			}
		}
	}()

	sr.wg.Add(1)
	go func() {
		defer sr.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		_, _ = sr.submit(ctx, Command{Action: "run"})
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = sr.submit(ctx, Command{Action: "run"})
			}
		}
	}()

	sr.wg.Add(1)
	go func() {
		defer sr.wg.Done()
		err := sr.serveNamedPipe(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("service named pipe endpoint failed", "err", err)
		}
	}()

	return nil
}

func (sr *serviceRunner) executeCommandSafe(cmd Command) (resp CommandResponse, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Warn(
				"panic during service command execution",
				"operation", cmd.Action,
				"recovered", recovered,
				"stack", string(debug.Stack()),
			)
			resp = CommandResponse{}
			err = fmt.Errorf("internal service panic while executing action %q", cmd.Action)
		}
	}()

	cmd.cancels = sr.cancels
	return executeCommand(sr.cfg, cmd, sr.managedRun)
}

// stop wakes the pipe listener and waits for in-flight work until ctx is done.
// Execute has already cancelled the service context, so the queue worker starts
// no new command and the listener accepts no new request once it wakes.
//
// Two things used to hold Stop-Service and Restart-Service at "Waiting for
// service to stop", for minutes or until the process was killed:
//   - Closing the listening pipe handle does not wake a ConnectNamedPipe
//     blocked on it; CloseHandle itself waits until a client connects. So stop
//     connects to its own pipe instead, and the listener returns on its own.
//   - A managed run already under way cannot be interrupted (it may be inside
//     msiexec), and stop waited for it.
//
// Every step therefore runs inside the wait, and stop gives up at ctx's
// deadline and lets the process exit. An installer child process outlives the
// service and finishes on its own; the next start's run converges the state.
func (sr *serviceRunner) stop(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		sr.wakeListener()
		sr.closeActiveConnections()
		sr.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		busy, _ := sr.busyAction.Load().(string)
		slog.Warn(
			"service stop deadline reached; abandoning in-progress work",
			"busyAction", busy,
			"openOperations", sr.openOperationCount(),
		)
	}
	gorillalog.Close()
}

// wakeListener connects to the service's own pipe and hangs up, so a listener
// waiting in ConnectNamedPipe returns and sees the cancelled context. A failed
// connection only means nobody is waiting, or stop's deadline covers it.
func (sr *serviceRunner) wakeListener() {
	conn, err := openPipe(servicePipePath(sr.cfg.ServicePipeName), time.Second)
	if err != nil {
		slog.Debug("could not wake the pipe listener", "err", err)
		return
	}
	_ = conn.Close()
}

// openOperationCount is how many tracked operations have no terminal record.
func (sr *serviceRunner) openOperationCount() int {
	sr.operationsMu.Lock()
	defer sr.operationsMu.Unlock()
	open := 0
	for _, op := range sr.operations {
		if !op.done {
			open++
		}
	}
	return open
}

func (sr *serviceRunner) submit(ctx context.Context, cmd Command) (CommandResponse, error) {
	result := make(chan queuedResult, 1)
	select {
	case <-ctx.Done():
		return CommandResponse{}, ctx.Err()
	case sr.queue <- queuedCommand{cmd: cmd, result: result}:
	}

	select {
	case <-ctx.Done():
		return CommandResponse{}, ctx.Err()
	case out := <-result:
		return out.resp, out.err
	}
}

func writeErrorEnvelope(file *os.File, requestID, operation, operationID, code, message string) {
	if err := json.NewEncoder(file).Encode(serviceEnvelope[errorResponsePayload]{
		Version:      pipeProtocolVersion,
		MessageType:  messageTypeError,
		Operation:    operation,
		RequestID:    requestID,
		OperationID:  operationID,
		TimestampUTC: nowRFC3339UTC(),
		Payload: errorResponsePayload{
			ErrorCode:    code,
			ErrorMessage: message,
		},
	}); err != nil {
		slog.Warn(
			"failed to write error envelope",
			"operation", operation,
			"requestId", requestID,
			"operationId", operationID,
			"err", err,
		)
	}
}

func (sr *serviceRunner) serveNamedPipe(ctx context.Context) error {
	pipePath := servicePipePath(sr.cfg.ServicePipeName)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		handle, err := createNamedPipe(pipePath)
		if err != nil {
			return fmt.Errorf("create pipe: %w", err)
		}

		err = windows.ConnectNamedPipe(handle, nil)
		if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			windows.CloseHandle(handle)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, windows.ERROR_NO_DATA) || errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
				continue
			}
			return fmt.Errorf("connect pipe: %w", err)
		}

		select {
		case sr.handlerSem <- struct{}{}:
			sr.trackActiveConnection(handle)
			sr.wg.Add(1)
			go func(connectedHandle windows.Handle) {
				defer sr.wg.Done()
				defer func() {
					sr.untrackActiveConnection(connectedHandle)
					<-sr.handlerSem
				}()
				file := os.NewFile(uintptr(connectedHandle), pipePath)
				sr.handlePipeCommand(ctx, file)
				sr.flushAndDisconnectNamedPipe(connectedHandle)
				_ = file.Close()
			}(handle)
		default:
			file := os.NewFile(uintptr(handle), pipePath)
			writeErrorEnvelope(file, "", actionStreamOperationStatus, "", "server_busy", "service is busy; retry shortly")
			sr.flushAndDisconnectNamedPipe(handle)
			_ = file.Close()
		}
	}
}

func (sr *serviceRunner) flushAndDisconnectNamedPipe(handle windows.Handle) {
	if err := flushNamedPipeBuffers(handle); err != nil &&
		!errors.Is(err, windows.ERROR_BROKEN_PIPE) &&
		!errors.Is(err, windows.ERROR_NO_DATA) {
		slog.Warn("failed to flush named pipe buffers", "err", err)
	}

	if err := disconnectNamedPipe(handle); err != nil &&
		!errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED) &&
		!errors.Is(err, windows.ERROR_BROKEN_PIPE) &&
		!errors.Is(err, windows.ERROR_NO_DATA) {
		slog.Warn("failed to disconnect named pipe", "err", err)
	}
}

func (sr *serviceRunner) handlePipeCommand(ctx context.Context, file *os.File) {
	startedAt := time.Now()
	result := "error"
	var req serviceEnvelope[json.RawMessage]
	defer func() {
		if recovered := recover(); recovered != nil {
			result = "error"
			slog.Warn(
				"panic while handling named pipe request",
				"operation", req.Operation,
				"requestId", req.RequestID,
				"operationId", req.OperationID,
				"recovered", recovered,
				"stack", string(debug.Stack()),
			)
			writeErrorEnvelope(file, req.RequestID, req.Operation, req.OperationID, "internal_error", "internal service error")
		}
		slog.Debug(
			"named pipe lifecycle",
			"operation", req.Operation,
			"requestId", req.RequestID,
			"operationId", req.OperationID,
			"state", "completed",
			"result", result,
			"durationMs", time.Since(startedAt).Milliseconds(),
		)
	}()

	if err := json.NewDecoder(file).Decode(&req); err != nil {
		result = "error"
		slog.Warn("failed to decode named pipe request", "err", err)
		writeErrorEnvelope(file, "", "", "", "invalid_request", "invalid JSON request body")
		return
	}

	// Per-request logger carries the correlation keys so request/operation flow
	// can be joined with UI diagnostics.
	logger := slog.With("operation", req.Operation, "requestId", req.RequestID, "operationId", req.OperationID)
	logger.Debug("named pipe request", "state", "received")

	if req.Version != pipeProtocolVersion {
		result = "error"
		writeErrorEnvelope(file, req.RequestID, req.Operation, req.OperationID, "unsupported_version", "unsupported protocol version")
		return
	}

	cmd, err := commandFromRequestEnvelope(req)
	if err != nil {
		result = "error"
		logger.Warn("failed to map request envelope to command", "err", err)
		writeErrorEnvelope(file, req.RequestID, req.Operation, req.OperationID, "invalid_request", err.Error())
		return
	}

	if err := validateCommand(cmd); err != nil {
		result = "error"
		logger.Warn("command validation failed", "err", err)
		writeErrorEnvelope(file, req.RequestID, req.Operation, req.OperationID, "invalid_request", err.Error())
		return
	}

	if cmd.Action == actionStreamOperationStatus {
		if err := sr.writeStreamOperationStatusSequence(file, req, cmd.Items[0]); err != nil {
			result = "error"
			logger.Warn("failed to write stream response envelope", "err", err)
			return
		}
		result = "ok"
		return
	}

	var resp CommandResponse
	switch cmd.Action {
	case actionGetBranding:
		// Branding is a read-only lookup the UI makes before opening its window,
		// so it skips the command queue rather than wait behind a managed run.
		resp, err = sr.executeCommandSafe(cmd)
	case actionCancelOperation:
		// A cancel must not wait in the queue behind the run it is meant to stop.
		resp, err = CommandResponse{Status: "ok", OperationID: cmd.Items[0]}, sr.cancelOperation(cmd.Items[0])
	default:
		resp, err = sr.submit(ctx, cmd)
	}
	if err != nil {
		code := "command_failed"
		switch {
		case errors.Is(err, context.Canceled):
			result = "canceled"
		case errors.Is(err, errNotCancelable):
			result, code = "refused", "operation_not_cancelable"
		default:
			result = "error"
		}
		logger.Warn("command execution failed", "err", err)
		writeErrorEnvelope(file, req.RequestID, req.Operation, req.OperationID, code, err.Error())
		return
	}
	if cmd.Action == actionInstallItem || cmd.Action == actionRemoveItem {
		sr.registerTrackedOperation(cmd.Items[0], resp)
	}

	if err := sr.writeSuccessEnvelope(file, req, cmd, resp); err != nil {
		result = "error"
		logger.Warn("failed to write success envelope", "err", err)
	} else {
		result = "ok"
		logger.Debug("named pipe response sent", "state", "responded")
	}
	var itemName string
	if len(cmd.Items) > 0 {
		itemName = cmd.Items[0]
	}
	sr.scheduleRunAfterMutation(ctx, cmd.Action, itemName, resp.OperationID)
}

func (sr *serviceRunner) scheduleRunAfterMutation(ctx context.Context, action, itemName, operationID string) {
	if action != actionInstallItem && action != actionRemoveItem {
		return
	}

	sr.wg.Add(1)
	go func() {
		defer sr.wg.Done()
		resp, err := sr.submit(ctx, Command{Action: actionRun, progress: sr.operationProgressCallback(operationID)})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				sr.appendOperationEvent(operationID, OperationStatusPayload{
					State:      "Canceled",
					Message:    "Operation canceled",
					CanceledBy: "service",
				})
				return
			}
			slog.Warn(
				"failed to run managed action after service mutation",
				"operation", action,
				"operationId", operationID,
				"err", err,
			)
			sr.appendOperationEvent(operationID, OperationStatusPayload{
				State:           "Failed",
				ProgressPercent: 100,
				Message:         "Operation failed",
				ErrorCode:       "managed_run_failed",
				ErrorMessage:    err.Error(),
			})
			return
		}
		sr.appendOperationEvent(operationID, resolveTerminalEvent(itemName, resp.report))
	}()
}

func (sr *serviceRunner) operationProgressCallback(operationID string) installer.ProgressFn {
	return func(item catalog.Item, state string, percent int, message string) {
		mappedState := ""
		switch state {
		case "downloading":
			mappedState = "Downloading"
		case "installing":
			mappedState = "Installing"
		case "removing":
			mappedState = "Removing"
		case "done":
			mappedState = "ItemCompleted"
		case "failed":
			mappedState = "ItemFailed"
		default:
			return
		}
		sr.appendOperationEvent(operationID, OperationStatusPayload{
			ItemName:        item.Name,
			DisplayName:     orDefault(item.DisplayName, item.Name),
			State:           mappedState,
			ProgressPercent: percent,
			Message:         message,
		})
	}
}

// resolveTerminalEvent reads the mutated item's real outcome from the run report
// (keyed by catalog name, R13) and returns the honest terminal event (R10). A
// nil report (unexpected, but a nil error means the run succeeded) resolves to
// Succeeded. ItemName and DisplayName stay empty: appendOperationEvent fills both
// from the operation, which by then knows the item's catalog display name.
func resolveTerminalEvent(itemName string, rep *report.Report) OperationStatusPayload {
	if rep != nil {
		for _, failed := range rep.FailedItems {
			if failed.Name == itemName {
				return OperationStatusPayload{
					State:           "Failed",
					ProgressPercent: 100,
					Message:         "Operation failed",
					ErrorCode:       "item_failed",
					ErrorMessage:    failed.Error,
				}
			}
		}
		for _, deferred := range rep.DeferredItems {
			if deferred.Name == itemName {
				return OperationStatusPayload{
					State:           "Deferred",
					ProgressPercent: 100,
					Message:         deferred.Reason,
					ErrorCode:       "blocked_by_running_app",
					ErrorMessage:    deferred.Reason,
				}
			}
		}
	}
	return OperationStatusPayload{
		State:           "Succeeded",
		ProgressPercent: 100,
		Message:         "Operation completed",
	}
}

func commandFromRequestEnvelope(req serviceEnvelope[json.RawMessage]) (Command, error) {
	canonicalAction, ok := canonicalizeAction(req.Operation)
	if !ok {
		return Command{}, fmt.Errorf("unsupported service action %q", req.Operation)
	}

	cmd := Command{Action: canonicalAction}
	switch canonicalAction {
	case actionListOptionalInstalls, actionGetBranding:
		return cmd, nil
	case actionInstallItem:
		payload, err := decodeEnvelopePayload[installItemRequest](req.Payload)
		if err != nil {
			return Command{}, fmt.Errorf("invalid InstallItem payload: %w", err)
		}
		itemName := strings.TrimSpace(payload.ItemName)
		if itemName == "" {
			return Command{}, errors.New("InstallItem requires itemName")
		}
		cmd.Items = []string{itemName}
		return cmd, nil
	case actionRemoveItem:
		payload, err := decodeEnvelopePayload[removeItemRequest](req.Payload)
		if err != nil {
			return Command{}, fmt.Errorf("invalid RemoveItem payload: %w", err)
		}
		itemName := strings.TrimSpace(payload.ItemName)
		if itemName == "" {
			return Command{}, errors.New("RemoveItem requires itemName")
		}
		cmd.Items = []string{itemName}
		return cmd, nil
	case actionStreamOperationStatus, actionCancelOperation:
		operationID := strings.TrimSpace(req.OperationID)
		if operationID == "" {
			return Command{}, fmt.Errorf("%s requires operationId", canonicalAction)
		}
		cmd.Items = []string{operationID}
		return cmd, nil
	default:
		return Command{}, fmt.Errorf("unsupported service action %q", req.Operation)
	}
}

func (sr *serviceRunner) writeSuccessEnvelope(file *os.File, req serviceEnvelope[json.RawMessage], cmd Command, resp CommandResponse) error {
	switch cmd.Action {
	case actionListOptionalInstalls:
		if err := json.NewEncoder(file).Encode(serviceEnvelope[listOptionalInstallsResponse]{
			Version:      pipeProtocolVersion,
			MessageType:  messageTypeResponse,
			Operation:    actionListOptionalInstalls,
			RequestID:    req.RequestID,
			OperationID:  "",
			TimestampUTC: nowRFC3339UTC(),
			Payload:      listOptionalInstallsResponse{Items: resp.OptionalItems},
		}); err != nil {
			return err
		}
		return nil
	case actionGetBranding:
		var payload branding.Branding
		if resp.Branding != nil {
			payload = *resp.Branding
		}
		return json.NewEncoder(file).Encode(serviceEnvelope[branding.Branding]{
			Version:      pipeProtocolVersion,
			MessageType:  messageTypeResponse,
			Operation:    actionGetBranding,
			RequestID:    req.RequestID,
			TimestampUTC: nowRFC3339UTC(),
			Payload:      payload,
		})
	case actionInstallItem, actionRemoveItem:
		if err := json.NewEncoder(file).Encode(serviceEnvelope[AcceptedOperation]{
			Version:      pipeProtocolVersion,
			MessageType:  messageTypeResponse,
			Operation:    cmd.Action,
			RequestID:    req.RequestID,
			OperationID:  resp.OperationID,
			TimestampUTC: nowRFC3339UTC(),
			Payload: AcceptedOperation{
				Accepted:    true,
				QueuedAtUTC: nowRFC3339UTC(),
			},
		}); err != nil {
			return err
		}
		return nil
	case actionCancelOperation:
		return json.NewEncoder(file).Encode(serviceEnvelope[cancelOperationResponse]{
			Version:      pipeProtocolVersion,
			MessageType:  messageTypeResponse,
			Operation:    actionCancelOperation,
			RequestID:    req.RequestID,
			OperationID:  resp.OperationID,
			TimestampUTC: nowRFC3339UTC(),
			Payload:      cancelOperationResponse{Canceled: true},
		})
	default:
		writeErrorEnvelope(file, req.RequestID, req.Operation, req.OperationID, "unsupported_action", "unsupported service action")
		return nil
	}
}

func (sr *serviceRunner) writeStreamOperationStatusSequence(file *os.File, req serviceEnvelope[json.RawMessage], operationID string) error {
	if !sr.hasTrackedOperation(operationID) {
		writeErrorEnvelope(file, req.RequestID, req.Operation, req.OperationID, "invalid_request", "unknown operationId")
		return nil
	}

	if err := json.NewEncoder(file).Encode(serviceEnvelope[streamOperationStatusAckResponse]{
		Version:      pipeProtocolVersion,
		MessageType:  messageTypeResponse,
		Operation:    actionStreamOperationStatus,
		RequestID:    req.RequestID,
		OperationID:  operationID,
		TimestampUTC: nowRFC3339UTC(),
		Payload: streamOperationStatusAckResponse{
			StreamAccepted: true,
		},
	}); err != nil {
		return err
	}
	slog.Debug(
		"stream ack sent",
		"operation", actionStreamOperationStatus,
		"requestId", req.RequestID,
		"operationId", operationID,
	)

	sent := 0
	for {
		events, done, ok := sr.snapshotTrackedOperation(operationID)
		if !ok {
			writeErrorEnvelope(file, req.RequestID, req.Operation, req.OperationID, "invalid_request", "unknown operationId")
			return nil
		}
		for sent < len(events) {
			if err := json.NewEncoder(file).Encode(serviceEnvelope[OperationStatusPayload]{
				Version:      pipeProtocolVersion,
				MessageType:  messageTypeEvent,
				Operation:    actionStreamOperationStatus,
				RequestID:    "",
				OperationID:  operationID,
				TimestampUTC: nowRFC3339UTC(),
				Payload:      events[sent],
			}); err != nil {
				return err
			}
			sent++
		}
		if done {
			return nil
		}
		time.Sleep(streamPollSleep)
	}
}

// registerTrackedOperation starts the record of the InstallItem or RemoveItem
// operation resp accepted for itemName. resp.displayName is the catalog display
// name when the mutation knew it; otherwise the item name stands in until a
// progress event for the item carries the catalog name.
func (sr *serviceRunner) registerTrackedOperation(itemName string, resp CommandResponse) {
	operationID := resp.OperationID
	if strings.TrimSpace(operationID) == "" || strings.TrimSpace(itemName) == "" {
		return
	}
	displayName := orDefault(strings.TrimSpace(resp.displayName), itemName)
	sr.operationsMu.Lock()
	defer sr.operationsMu.Unlock()
	sr.pruneTrackedOperationsLocked(time.Now())
	sr.operations[operationID] = &trackedOperation{
		events: []OperationStatusPayload{
			{
				ItemName:        itemName,
				DisplayName:     displayName,
				State:           "Queued",
				ProgressPercent: 0,
				Message:         "Operation queued",
			},
		},
		requestedItemName:    itemName,
		requestedDisplayName: displayName,
		prior:                resp.prior,
		requested:            resp.requested,
		lastUpdated:          time.Now(),
	}
}

func (sr *serviceRunner) appendOperationEvent(operationID string, event OperationStatusPayload) {
	if strings.TrimSpace(operationID) == "" {
		return
	}
	sr.operationsMu.Lock()
	defer sr.operationsMu.Unlock()
	sr.appendOperationEventLocked(operationID, event)
}

// appendOperationEventLocked is appendOperationEvent with operationsMu held. An
// operation that already has its terminal record takes no more: a user cancel
// ends it while the run it was waiting for still reports.
func (sr *serviceRunner) appendOperationEventLocked(operationID string, event OperationStatusPayload) {
	op, ok := sr.operations[operationID]
	if !ok || op.done {
		return
	}
	if strings.TrimSpace(event.ItemName) == "" {
		event.ItemName = op.requestedItemName
	}
	// Progress events carry the catalog display name. Remember it for the
	// requested item so the records filled in below (the terminal one from the
	// run report, a service cancel or failure) name the item the same way.
	if event.ItemName == op.requestedItemName && strings.TrimSpace(event.DisplayName) != "" {
		op.requestedDisplayName = event.DisplayName
	}
	if strings.TrimSpace(event.DisplayName) == "" {
		event.DisplayName = op.requestedDisplayName
	}
	now := time.Now()
	op.events = append(op.events, event)
	op.lastUpdated = now
	// Single choke point for tracked-operation transitions; log the state with
	// the operationId the service already has in hand.
	slog.Debug(
		"operation status event",
		"operationId", operationID,
		"state", event.State,
		"progressPercent", event.ProgressPercent,
	)
	if IsTerminalOperationState(event.State) {
		op.done = true
		op.completedAt = now
	}
	sr.pruneTrackedOperationsLocked(now)
}

// cancelOperation is CancelOperation. It accepts only while the operation is
// open and no run has started its item's install or uninstall command; it then
// reverts the request's self-serve selection, withdraws the item from the run
// under way (aborting its download), and ends the operation with a Canceled
// record from the user. Every refusal wraps errNotCancelable.
func (sr *serviceRunner) cancelOperation(operationID string) error {
	sr.operationsMu.Lock()
	defer sr.operationsMu.Unlock()
	op, ok := sr.operations[operationID]
	switch {
	case !ok:
		return fmt.Errorf("%w: unknown operationId", errNotCancelable)
	case op.done:
		return fmt.Errorf("%w: it has already finished", errNotCancelable)
	}
	err := withdrawItem(sr.cfg, sr.cancels, op.requestedItemName, op.prior, op.requested)
	if errors.Is(err, errNotCancelable) {
		return fmt.Errorf("%w: work on %s has already started", errNotCancelable, op.requestedDisplayName)
	}
	if err != nil {
		return err
	}
	sr.appendOperationEventLocked(operationID, OperationStatusPayload{
		State:      "Canceled",
		Message:    "Canceled by user",
		CanceledBy: "user",
	})
	return nil
}

func (sr *serviceRunner) hasTrackedOperation(operationID string) bool {
	sr.operationsMu.Lock()
	defer sr.operationsMu.Unlock()
	_, ok := sr.operations[operationID]
	return ok
}

func (sr *serviceRunner) snapshotTrackedOperation(operationID string) ([]OperationStatusPayload, bool, bool) {
	sr.operationsMu.Lock()
	defer sr.operationsMu.Unlock()
	op, ok := sr.operations[operationID]
	if !ok {
		return nil, false, false
	}
	out := make([]OperationStatusPayload, len(op.events))
	copy(out, op.events)
	return out, op.done, true
}

func (sr *serviceRunner) pruneTrackedOperationsLocked(now time.Time) {
	for id, op := range sr.operations {
		if op.done && !op.completedAt.IsZero() && now.Sub(op.completedAt) > trackedCompletedOperationTTL {
			delete(sr.operations, id)
		}
	}

	if len(sr.operations) <= trackedOperationsMaxCount {
		return
	}

	type doneOp struct {
		id          string
		completedAt time.Time
	}
	done := make([]doneOp, 0, len(sr.operations))
	for id, op := range sr.operations {
		if !op.done {
			continue
		}
		done = append(done, doneOp{id: id, completedAt: op.completedAt})
	}
	sort.Slice(done, func(i, j int) bool {
		return done[i].completedAt.Before(done[j].completedAt)
	})
	for _, candidate := range done {
		if len(sr.operations) <= trackedOperationsMaxCount {
			return
		}
		delete(sr.operations, candidate.id)
	}
}

func createNamedPipe(pipePath string) (windows.Handle, error) {
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;AU)")
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("security descriptor: %w", err)
	}

	sa := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
		InheritHandle:      0,
	}

	name, err := windows.UTF16PtrFromString(pipePath)
	if err != nil {
		return windows.InvalidHandle, err
	}

	return windows.CreateNamedPipe(
		name,
		windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_MESSAGE|windows.PIPE_READMODE_MESSAGE|windows.PIPE_WAIT,
		windows.PIPE_UNLIMITED_INSTANCES,
		64*1024,
		64*1024,
		0,
		&sa,
	)
}

func (sr *serviceRunner) trackActiveConnection(handle windows.Handle) {
	sr.activeConnMu.Lock()
	defer sr.activeConnMu.Unlock()
	sr.activeConns[handle] = struct{}{}
}

func (sr *serviceRunner) untrackActiveConnection(handle windows.Handle) {
	sr.activeConnMu.Lock()
	defer sr.activeConnMu.Unlock()
	delete(sr.activeConns, handle)
}

func (sr *serviceRunner) closeActiveConnections() {
	sr.activeConnMu.Lock()
	defer sr.activeConnMu.Unlock()
	for handle := range sr.activeConns {
		_ = windows.CloseHandle(handle)
	}
}

type gorillaWindowsService struct {
	cfg        config.Configuration
	managedRun func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error)
}

func (g *gorillaWindowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := newServiceRunner(g.cfg, g.managedRun)
	if err := runner.start(ctx); err != nil {
		slog.Warn("failed to start service runner", "err", err)
		return false, 1
	}

	changes <- svc.Status{State: svc.Running, Accepts: accepted}

	for req := range requests {
		switch req.Cmd {
		case svc.Interrogate:
			changes <- req.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			cancel()
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
			runner.stop(stopCtx)
			stopCancel()
			return false, 0
		default:
		}
	}

	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	runner.stop(stopCtx)
	stopCancel()
	return false, 0
}

func Run(cfg config.Configuration, managedRun func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error)) error {
	return svc.Run(cfg.ServiceName, &gorillaWindowsService{cfg: cfg, managedRun: managedRun})
}
