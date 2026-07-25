//go:build windows

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func (c *Client) doRequest(ctx context.Context, req serviceEnvelope[any], callback func(OperationStatus) error) (serviceEnvelope[json.RawMessage], error) {
	conn, err := openPipeContext(ctx, servicePipePath(c.pipeName()), c.connectTimeout())
	if err != nil {
		return serviceEnvelope[json.RawMessage]{}, fmt.Errorf("failed to connect to service pipe %s: %w", servicePipePath(c.pipeName()), err)
	}
	defer func() { _ = conn.Close() }()

	responseCtx, cancelResponse := context.WithTimeout(ctx, c.responseTimeout())
	defer cancelResponse()
	stopResponseClose := closeOnContextDone(responseCtx, conn)
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		stopResponseClose()
		return serviceEnvelope[json.RawMessage]{}, clientIOError(responseCtx, "failed to send service command", err)
	}
	dec := json.NewDecoder(conn)
	var resp serviceEnvelope[json.RawMessage]
	if err := decodeClientResponse(dec, req, &resp); err != nil {
		stopResponseClose()
		return serviceEnvelope[json.RawMessage]{}, clientIOError(responseCtx, "", err)
	}
	if req.Operation == actionStreamOperationStatus {
		ack, decodeErr := decodeEnvelopePayload[streamOperationStatusAckResponse](resp.Payload)
		if decodeErr != nil {
			stopResponseClose()
			return serviceEnvelope[json.RawMessage]{}, clientIOError(responseCtx, "failed to decode stream ack payload", decodeErr)
		}
		if !ack.StreamAccepted {
			stopResponseClose()
			return serviceEnvelope[json.RawMessage]{}, clientIOError(responseCtx, "", errors.New("service rejected stream request"))
		}
	}
	stopResponseClose()
	cancelResponse()

	if req.Operation != actionStreamOperationStatus {
		return resp, nil
	}

	stopStreamClose := closeOnContextDone(ctx, conn)
	err = consumeOperationStream(dec, req.RequestID, req.OperationID, callback)
	stopStreamClose()
	if err != nil {
		return serviceEnvelope[json.RawMessage]{}, clientIOError(ctx, "", err)
	}
	return resp, nil
}

func (c *Client) pipeName() string {
	if strings.TrimSpace(c.PipeName) == "" {
		return DefaultPipeName
	}
	return c.PipeName
}

func (c *Client) connectTimeout() time.Duration {
	if c.ConnectTimeout <= 0 {
		return defaultConnectTimeout
	}
	return c.ConnectTimeout
}

func (c *Client) responseTimeout() time.Duration {
	if c.ResponseTimeout <= 0 {
		return defaultResponseTimeout
	}
	return c.ResponseTimeout
}

func closeOnContextDone(ctx context.Context, conn *os.File) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func clientIOError(ctx context.Context, prefix string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if prefix == "" {
		return err
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func servicePipePath(pipeName string) string {
	if strings.HasPrefix(pipeName, `\\.\pipe\`) {
		return pipeName
	}
	return `\\.\pipe\` + strings.TrimSpace(pipeName)
}

func openPipe(pipePath string, timeout time.Duration) (*os.File, error) {
	return openPipeContext(context.Background(), pipePath, timeout)
}

func openPipeContext(ctx context.Context, pipePath string, timeout time.Duration) (*os.File, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		pathPtr, err := windows.UTF16PtrFromString(pipePath)
		if err != nil {
			return nil, err
		}
		handle, err := windows.CreateFile(
			pathPtr,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0,
			nil,
			windows.OPEN_EXISTING,
			windows.FILE_ATTRIBUTE_NORMAL,
			0,
		)
		if err == nil {
			return os.NewFile(uintptr(handle), pipePath), nil
		}
		lastErr = err
		if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
