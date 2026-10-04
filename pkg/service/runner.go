package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/gorillalog"
	"github.com/1dustindavis/gorilla/pkg/installer"
	"github.com/1dustindavis/gorilla/pkg/report"
	"github.com/1dustindavis/gorilla/pkg/version"
)

// listener is the per-platform transport: a named pipe on Windows
// (transport_windows.go), a Unix domain socket elsewhere (transport_unix.go).
// Everything else in the service is portable.
type listener interface {
	// Accept waits for the next client connection.
	Accept() (io.ReadWriteCloser, error)
	// Close makes a blocked Accept return. Connections already accepted stay
	// open.
	Close() error
}

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
	startedAt    time.Time
	ln           listener
	activeConnMu sync.Mutex
	activeConns  map[io.Closer]struct{}
	operationsMu sync.Mutex
	operations   map[string]*trackedOperation
	// busyAction is the command the queue worker is executing, "" when idle;
	// stop logs it when it gives up waiting.
	busyAction atomic.Value
	// cancels lets cancelOperation withdraw an item from the run under way.
	cancels *installer.Cancels
}

// CEILING: streams poll their operation's records every streamPollSleep
// rather than waiting on a signal. That costs little at one UI per machine;
// switch to a per-operation notify channel if many watchers appear.
var streamPollSleep = 20 * time.Millisecond

const (
	maxConcurrentHandlers        = 32
	trackedOperationsMaxCount    = 512
	trackedCompletedOperationTTL = 24 * time.Hour
)

type trackedOperation struct {
	// events are the operation's status records, with OperationID, Seq and
	// TimestampUTC filled in as they were recorded.
	events               []OperationStatus
	requestedItemName    string
	requestedDisplayName string
	done                 bool
	lastUpdated          time.Time
	completedAt          time.Time

	// prior and requested are the self-serve selections before and after the
	// request, for cancelOperation to revert.
	prior, requested selection
}

func newServiceRunner(cfg config.Configuration, managedRun func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error)) *serviceRunner {
	sr := &serviceRunner{
		cfg:         cfg,
		managedRun:  managedRun,
		queue:       make(chan queuedCommand),
		handlerSem:  make(chan struct{}, maxConcurrentHandlers),
		startedAt:   time.Now(),
		activeConns: make(map[io.Closer]struct{}),
		operations:  make(map[string]*trackedOperation),
		cancels:     installer.NewCancels(),
	}
	sr.busyAction.Store("")
	return sr
}

func (sr *serviceRunner) start(ctx context.Context) error {
	if err := gorillalog.NewLog(sr.cfg); err != nil {
		return fmt.Errorf("initialize logger: %w", err)
	}

	interval, err := time.ParseDuration(sr.cfg.ServiceInterval)
	if err != nil || interval <= 0 {
		return fmt.Errorf("invalid service interval %q: %w", sr.cfg.ServiceInterval, err)
	}

	ln, err := listen(sr.cfg.ServicePipeName)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	sr.ln = ln

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
		err := sr.serve(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("service endpoint failed", "err", err)
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

// stop closes the listener and waits for in-flight work until ctx is done.
// The caller has already cancelled the service context, so the queue worker
// starts no new command and the listener accepts no new request.
//
// A managed run already under way cannot be interrupted (it may be inside
// msiexec), and on Windows closing the listener connects to the service's own
// pipe, which can also wait. Every step therefore runs inside the wait, and
// stop gives up at ctx's deadline and lets the process exit. An installer
// child process outlives the service and finishes on its own; the next
// start's run converges the state.
func (sr *serviceRunner) stop(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		if sr.ln != nil {
			_ = sr.ln.Close()
		}
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

// serve accepts connections until the listener closes, handling each on its
// own goroutine. A client beyond the handler limit gets server_busy at once.
func (sr *serviceRunner) serve(ctx context.Context) error {
	for {
		conn, err := sr.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if ctx.Err() != nil {
			_ = conn.Close()
			return ctx.Err()
		}

		select {
		case sr.handlerSem <- struct{}{}:
			sr.trackActiveConnection(conn)
			sr.wg.Add(1)
			go func() {
				defer sr.wg.Done()
				defer func() {
					sr.untrackActiveConnection(conn)
					<-sr.handlerSem
				}()
				sr.handleConn(ctx, conn)
				_ = conn.Close()
			}()
		default:
			// The request is not read, so its id is unknown: JSON-RPC answers
			// with a null id, which clients accept for an error.
			writeError(conn, nil, newError(codeServerBusy, "service is busy; retry shortly", ""))
			_ = conn.Close()
		}
	}
}

func writeMessage(w io.Writer, msg any) error {
	return json.NewEncoder(w).Encode(msg)
}

func writeResult(w io.Writer, id json.RawMessage, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return writeMessage(w, rpcResponse{JSONRPC: jsonrpcVersion, ID: id, Result: raw})
}

func writeError(w io.Writer, id json.RawMessage, rpcErr *Error) {
	if err := writeMessage(w, rpcResponse{JSONRPC: jsonrpcVersion, ID: id, Error: rpcErr}); err != nil {
		slog.Warn("failed to write error response", "id", string(id), "code", rpcErr.Data.Code, "err", err)
	}
}

// validRequestID is whether id is a JSON-RPC request id: a string, a number
// or null. An absent id makes the request a notification.
func validRequestID(id json.RawMessage) bool {
	if len(id) == 0 || string(id) == "null" {
		return true
	}
	switch c := id[0]; {
	case c == '"', c == '-', c >= '0' && c <= '9':
		return true
	default:
		return false
	}
}

func decodeParams[T any](raw json.RawMessage) (T, error) {
	var params T
	if len(raw) == 0 || string(raw) == "null" {
		return params, nil
	}
	err := json.Unmarshal(raw, &params)
	return params, err
}

// handleConn reads one request from conn and answers it.
func (sr *serviceRunner) handleConn(ctx context.Context, conn io.ReadWriter) {
	startedAt := time.Now()
	result := "error"
	var req rpcRequest
	defer func() {
		if recovered := recover(); recovered != nil {
			result = "error"
			slog.Warn(
				"panic while handling service request",
				"method", req.Method,
				"id", string(req.ID),
				"recovered", recovered,
				"stack", string(debug.Stack()),
			)
			writeError(conn, req.ID, newError(codeInternalError, "internal service error", ""))
		}
		slog.Debug(
			"service request lifecycle",
			"method", req.Method,
			"id", string(req.ID),
			"state", "completed",
			"result", result,
			"durationMs", time.Since(startedAt).Milliseconds(),
		)
	}()

	var raw json.RawMessage
	if err := json.NewDecoder(conn).Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			// The client hung up without a request.
			result = "empty"
			return
		}
		slog.Warn("failed to decode service request", "err", err)
		writeError(conn, nil, newError(codeParseError, "invalid JSON", ""))
		return
	}
	if err := json.Unmarshal(raw, &req); err != nil || req.JSONRPC != jsonrpcVersion || strings.TrimSpace(req.Method) == "" || !validRequestID(req.ID) {
		id := req.ID
		if !validRequestID(id) || len(id) == 0 {
			id = nil
		}
		req = rpcRequest{}
		writeError(conn, id, newError(codeInvalidRequest, "not a JSON-RPC 2.0 request", ""))
		return
	}
	if len(req.ID) == 0 {
		// A notification asks for no response, and the service has no method
		// that is useful without one, so it is not acted on.
		slog.Debug("ignoring JSON-RPC notification", "method", req.Method)
		result = "ignored"
		return
	}

	logger := slog.With("method", req.Method, "id", string(req.ID))
	logger.Debug("service request", "state", "received")

	value, rpcErr := sr.dispatch(ctx, conn, req, logger)
	switch {
	case rpcErr != nil:
		result = rpcErr.Data.Code
		logger.Warn("service request failed", "code", rpcErr.Data.Code, "err", rpcErr.Message)
		writeError(conn, req.ID, rpcErr)
	case value == nil:
		// dispatch wrote the response itself (a stream).
		result = "ok"
	default:
		if err := writeResult(conn, req.ID, value); err != nil {
			logger.Warn("failed to write response", "err", err)
			return
		}
		result = "ok"
		logger.Debug("service response sent", "state", "responded")
	}
}

// dispatch runs one request. It returns the result to send, or the error to
// send, or neither when it has written the response itself.
func (sr *serviceRunner) dispatch(ctx context.Context, conn io.Writer, req rpcRequest, logger *slog.Logger) (any, *Error) {
	switch req.Method {
	case methodGetServiceInfo:
		busy, _ := sr.busyAction.Load().(string)
		return ServiceInfo{
			Version:         version.Version().Version,
			ProtocolVersion: jsonrpcVersion,
			APIVersion:      apiVersion,
			Capabilities:    capabilities,
			Busy:            busy != "",
			UptimeSeconds:   int64(time.Since(sr.startedAt).Seconds()),
		}, nil

	case methodListOptionalInstalls:
		resp, err := sr.submit(ctx, Command{Action: actionListOptionalInstalls})
		if err != nil {
			return nil, commandError(err, "")
		}
		return listOptionalInstallsResponse{Items: resp.OptionalItems}, nil

	case methodGetBranding:
		// Branding is a read-only lookup the UI makes before opening its window,
		// so it skips the command queue rather than wait behind a managed run.
		resp, err := sr.executeCommandSafe(Command{Action: actionGetBranding})
		if err != nil {
			return nil, commandError(err, "")
		}
		return resp.Branding, nil

	case methodInstallItem, methodRemoveItem:
		params, err := decodeParams[itemParams](req.Params)
		itemName := strings.TrimSpace(params.ItemName)
		if err != nil || itemName == "" {
			return nil, newError(codeInvalidParams, req.Method+" requires params.itemName", "")
		}
		action := actionInstallItem
		if req.Method == methodRemoveItem {
			action = actionRemoveItem
		}
		// A mutation only writes the self-serve selection, under
		// manifest.UpdateSelfServe's lock, so it answers at once instead of
		// waiting behind a busy run; only the run it schedules below goes
		// through the queue. A mutation that lands mid-run does not change that
		// run: it keeps the plan it loaded, and the queued run reconciles the
		// newer selection afterwards. The run's own self-serve writes reload
		// under the same lock, so neither side loses the other's change.
		resp, err := sr.executeCommandSafe(Command{Action: action, Items: []string{itemName}})
		if err != nil {
			return nil, commandError(err, "")
		}
		sr.registerTrackedOperation(itemName, resp)
		accepted := AcceptedOperation{OperationID: resp.OperationID, Accepted: true, QueuedAtUTC: nowRFC3339UTC()}
		if err := writeResult(conn, req.ID, accepted); err != nil {
			logger.Warn("failed to write response", "err", err)
		}
		sr.scheduleRunAfterMutation(ctx, action, itemName, resp.OperationID)
		return nil, nil

	case methodCancelOperation:
		params, err := decodeParams[operationParams](req.Params)
		operationID := strings.TrimSpace(params.OperationID)
		if err != nil || operationID == "" {
			return nil, newError(codeInvalidParams, req.Method+" requires params.operationId", "")
		}
		// A cancel must not wait in the queue behind the run it is meant to stop.
		if err := sr.cancelOperation(operationID); err != nil {
			return nil, commandError(err, operationID)
		}
		return cancelOperationResponse{Canceled: true}, nil

	case methodStreamOperationStatus:
		params, err := decodeParams[operationParams](req.Params)
		operationID := strings.TrimSpace(params.OperationID)
		if err != nil || operationID == "" {
			return nil, newError(codeInvalidParams, req.Method+" requires params.operationId", "")
		}
		if !sr.hasTrackedOperation(operationID) {
			return nil, newError(codeUnknownOperation, "unknown operationId", operationID)
		}
		if err := sr.streamOperationStatus(conn, req.ID, operationID); err != nil {
			logger.Warn("operation stream ended early", "operationId", operationID, "err", err)
		}
		return nil, nil

	default:
		return nil, newError(codeMethodNotFound, fmt.Sprintf("method %q not found", req.Method), "")
	}
}

// commandError maps a command's failure to its JSON-RPC error.
func commandError(err error, operationID string) *Error {
	switch {
	case errors.Is(err, errNotCancelable):
		return newError(codeOperationNotCancelable, err.Error(), operationID)
	case errors.Is(err, errItemNotAvailable):
		return newError(codeItemNotAvailable, err.Error(), operationID)
	default:
		return newError(codeCommandFailed, err.Error(), operationID)
	}
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
				sr.appendOperationEvent(operationID, OperationStatus{
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
			sr.appendOperationEvent(operationID, OperationStatus{
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
		sr.appendOperationEvent(operationID, OperationStatus{
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
func resolveTerminalEvent(itemName string, rep *report.Report) OperationStatus {
	if rep != nil {
		for _, failed := range rep.FailedItems {
			if failed.Name == itemName {
				return OperationStatus{
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
				return OperationStatus{
					State:           "Deferred",
					ProgressPercent: 100,
					Message:         deferred.Reason,
					ErrorCode:       "blocked_by_running_app",
					ErrorMessage:    deferred.Reason,
				}
			}
		}
	}
	return OperationStatus{
		State:           "Succeeded",
		ProgressPercent: 100,
		Message:         "Operation completed",
	}
}

// streamOperationStatus acknowledges the stream, then sends every record of
// operationID as an operationStatus notification, from the first, until the
// terminal one.
func (sr *serviceRunner) streamOperationStatus(w io.Writer, id json.RawMessage, operationID string) error {
	if err := writeResult(w, id, streamOperationStatusAckResponse{StreamAccepted: true}); err != nil {
		return err
	}
	slog.Debug("stream ack sent", "operationId", operationID)

	sent := 0
	for {
		events, done, ok := sr.snapshotTrackedOperation(operationID)
		if !ok {
			// Only finished operations are pruned, and their terminal record
			// has gone out already, so this is not expected.
			return errors.New("operation is no longer tracked")
		}
		for ; sent < len(events); sent++ {
			params, err := json.Marshal(events[sent])
			if err != nil {
				return err
			}
			if err := writeMessage(w, rpcNotification{JSONRPC: jsonrpcVersion, Method: notificationOperationStatus, Params: params}); err != nil {
				return err
			}
		}
		if done {
			return nil
		}
		time.Sleep(streamPollSleep)
	}
}

// registerTrackedOperation starts the record of the installItem or removeItem
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
		requestedItemName:    itemName,
		requestedDisplayName: displayName,
		prior:                resp.prior,
		requested:            resp.requested,
	}
	sr.appendOperationEventLocked(operationID, OperationStatus{
		State:   "Queued",
		Message: "Operation queued",
	})
}

func (sr *serviceRunner) appendOperationEvent(operationID string, event OperationStatus) {
	if strings.TrimSpace(operationID) == "" {
		return
	}
	sr.operationsMu.Lock()
	defer sr.operationsMu.Unlock()
	sr.appendOperationEventLocked(operationID, event)
}

// appendOperationEventLocked is appendOperationEvent with operationsMu held. It
// stamps the record with the operation, its sequence number and the time. An
// operation that already has its terminal record takes no more: a user cancel
// ends it while the run it was waiting for still reports.
func (sr *serviceRunner) appendOperationEventLocked(operationID string, event OperationStatus) {
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
	event.OperationID = operationID
	event.Seq = len(op.events) + 1
	event.TimestampUTC = now.UTC().Format(timestampLayout)
	op.events = append(op.events, event)
	op.lastUpdated = now
	// Single choke point for tracked-operation transitions; log the state with
	// the operationId the service already has in hand.
	slog.Debug(
		"operation status event",
		"operationId", operationID,
		"seq", event.Seq,
		"state", event.State,
		"progressPercent", event.ProgressPercent,
	)
	if IsTerminalOperationState(event.State) {
		op.done = true
		op.completedAt = now
	}
	sr.pruneTrackedOperationsLocked(now)
}

// cancelOperation is cancelOperation. It accepts only while the operation is
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
	sr.appendOperationEventLocked(operationID, OperationStatus{
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

func (sr *serviceRunner) snapshotTrackedOperation(operationID string) ([]OperationStatus, bool, bool) {
	sr.operationsMu.Lock()
	defer sr.operationsMu.Unlock()
	op, ok := sr.operations[operationID]
	if !ok {
		return nil, false, false
	}
	out := make([]OperationStatus, len(op.events))
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

func (sr *serviceRunner) trackActiveConnection(conn io.Closer) {
	sr.activeConnMu.Lock()
	defer sr.activeConnMu.Unlock()
	sr.activeConns[conn] = struct{}{}
}

func (sr *serviceRunner) untrackActiveConnection(conn io.Closer) {
	sr.activeConnMu.Lock()
	defer sr.activeConnMu.Unlock()
	delete(sr.activeConns, conn)
}

// closeActiveConnections closes every connection still being handled, so a
// handler blocked reading a request or writing a stream returns.
func (sr *serviceRunner) closeActiveConnections() {
	sr.activeConnMu.Lock()
	conns := make([]io.Closer, 0, len(sr.activeConns))
	for conn := range sr.activeConns {
		conns = append(conns, conn)
	}
	sr.activeConnMu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}
