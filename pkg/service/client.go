package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/1dustindavis/gorilla/pkg/branding"
)

const DefaultPipeName = "gorilla-service"

const (
	defaultConnectTimeout  = 5 * time.Second
	defaultResponseTimeout = 30 * time.Second
)

// Client is the typed Gorilla service client shared by command-line and UI
// callers. Timeouts default to five seconds for connecting and 30 seconds for
// ordinary responses and stream acknowledgements.
type Client struct {
	PipeName        string
	ConnectTimeout  time.Duration
	ResponseTimeout time.Duration
}

func NewClient(pipeName string) *Client {
	if strings.TrimSpace(pipeName) == "" {
		pipeName = DefaultPipeName
	}
	return &Client{
		PipeName:        pipeName,
		ConnectTimeout:  defaultConnectTimeout,
		ResponseTimeout: defaultResponseTimeout,
	}
}

// GetServiceInfo returns the service's version, protocol and capabilities,
// and whether a managed run is under way.
func (c *Client) GetServiceInfo(ctx context.Context) (ServiceInfo, error) {
	var info ServiceInfo
	if err := c.call(ctx, methodGetServiceInfo, nil, &info, nil); err != nil {
		return ServiceInfo{}, err
	}
	if info.ProtocolVersion != jsonrpcVersion || info.APIVersion <= 0 {
		return ServiceInfo{}, errors.New("malformed getServiceInfo result")
	}
	return info, nil
}

func (c *Client) ListOptionalInstalls(ctx context.Context) ([]OptionalInstallItem, error) {
	var result listOptionalInstallsResponse
	if err := c.call(ctx, methodListOptionalInstalls, nil, &result, nil); err != nil {
		return nil, err
	}
	if result.Items == nil {
		return nil, errors.New("malformed listOptionalInstalls result")
	}
	for _, item := range result.Items {
		if strings.TrimSpace(item.ItemName) == "" || strings.TrimSpace(item.DisplayName) == "" {
			return nil, errors.New("malformed listOptionalInstalls item identity")
		}
	}
	return result.Items, nil
}

// GetBranding returns the organisation branding the service resolved from
// policy and config. Unset fields are empty strings.
func (c *Client) GetBranding(ctx context.Context) (branding.Branding, error) {
	var result branding.Branding
	if err := c.call(ctx, methodGetBranding, nil, &result, nil); err != nil {
		return branding.Branding{}, err
	}
	return result, nil
}

func (c *Client) InstallItem(ctx context.Context, itemName string) (AcceptedOperation, error) {
	return c.mutate(ctx, methodInstallItem, itemName)
}

func (c *Client) RemoveItem(ctx context.Context, itemName string) (AcceptedOperation, error) {
	return c.mutate(ctx, methodRemoveItem, itemName)
}

func (c *Client) mutate(ctx context.Context, method, itemName string) (AcceptedOperation, error) {
	itemName = strings.TrimSpace(itemName)
	if itemName == "" {
		return AcceptedOperation{}, fmt.Errorf("%s requires itemName", method)
	}
	var accepted AcceptedOperation
	if err := c.call(ctx, method, itemParams{ItemName: itemName}, &accepted, nil); err != nil {
		return AcceptedOperation{}, err
	}
	if !accepted.Accepted {
		return AcceptedOperation{}, errors.New("service did not accept operation")
	}
	if strings.TrimSpace(accepted.OperationID) == "" || strings.TrimSpace(accepted.QueuedAtUTC) == "" {
		return AcceptedOperation{}, fmt.Errorf("malformed %s result", method)
	}
	return accepted, nil
}

// StreamOperationStatus calls callback with each of operationID's status
// records, from the first, until the terminal one.
func (c *Client) StreamOperationStatus(ctx context.Context, operationID string, callback func(OperationStatus) error) error {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return errors.New("streamOperationStatus requires operationId")
	}
	if callback == nil {
		return errors.New("streamOperationStatus requires callback")
	}
	var ack streamOperationStatusAckResponse
	err := c.call(ctx, methodStreamOperationStatus, operationParams{OperationID: operationID}, &ack, func(dec *json.Decoder) error {
		if !ack.StreamAccepted {
			return errors.New("service rejected stream request")
		}
		return consumeOperationStream(dec, operationID, callback)
	})
	return err
}

// CancelOperation asks the service to cancel operationID. The service refuses
// with operation_not_cancelable once the item's install or removal has started,
// the operation has finished, or the operation is unknown.
func (c *Client) CancelOperation(ctx context.Context, operationID string) error {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return errors.New("cancelOperation requires operationId")
	}
	var result cancelOperationResponse
	if err := c.call(ctx, methodCancelOperation, operationParams{OperationID: operationID}, &result, nil); err != nil {
		return err
	}
	if !result.Canceled {
		return errors.New("service did not cancel the operation")
	}
	return nil
}

// call sends one request on a new connection and decodes its result into
// result. The response must arrive within the response timeout; stream, when
// set, then reads the rest of the connection bounded only by ctx.
func (c *Client) call(ctx context.Context, method string, params, result any, stream func(*json.Decoder) error) error {
	req, err := newRPCRequest(method, params)
	if err != nil {
		return err
	}
	conn, err := dial(ctx, c.pipeName(), c.connectTimeout())
	if err != nil {
		return fmt.Errorf("failed to connect to service %s: %w", c.pipeName(), err)
	}
	defer func() { _ = conn.Close() }()

	responseCtx, cancelResponse := context.WithTimeout(ctx, c.responseTimeout())
	defer cancelResponse()
	stopResponseClose := closeOnContextDone(responseCtx, conn)
	dec := json.NewDecoder(conn)
	sendErr := json.NewEncoder(conn).Encode(req)
	err = decodeResponse(dec, req.ID, result)
	stopResponseClose()
	if sendErr != nil {
		// The service answers server_busy without reading the request and
		// hangs up, which can fail the send; its answer is still readable.
		var rpcErr *Error
		if errors.As(err, &rpcErr) {
			return rpcErr
		}
		return clientIOError(responseCtx, "failed to send service request", sendErr)
	}
	if err != nil {
		return clientIOError(responseCtx, "", err)
	}
	if stream == nil {
		return nil
	}

	stopStreamClose := closeOnContextDone(ctx, conn)
	err = stream(dec)
	stopStreamClose()
	if err != nil {
		return clientIOError(ctx, "", err)
	}
	return nil
}

func newRPCRequest(method string, params any) (rpcRequest, error) {
	req := rpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      json.RawMessage(strconv.Quote(strconv.FormatInt(time.Now().UnixNano(), 10))),
		Method:  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return rpcRequest{}, fmt.Errorf("failed to encode %s params: %w", method, err)
		}
		req.Params = raw
	}
	return req, nil
}

// decodeResponse reads the response to the request with id and decodes its
// result. An error response is returned as *Error; its id may also be null,
// which the service sends when it answers before reading the request
// (server_busy) or cannot read it.
func decodeResponse(dec *json.Decoder, id json.RawMessage, result any) error {
	var resp rpcResponse
	if err := dec.Decode(&resp); err != nil {
		return fmt.Errorf("failed to decode service response: %w", err)
	}
	if resp.JSONRPC != jsonrpcVersion {
		return fmt.Errorf("unexpected jsonrpc version %q", resp.JSONRPC)
	}
	if resp.Error != nil {
		if !bytes.Equal(resp.ID, id) && string(resp.ID) != "null" {
			return fmt.Errorf("unexpected response id %s", resp.ID)
		}
		if strings.TrimSpace(resp.Error.Data.Code) == "" || strings.TrimSpace(resp.Error.Message) == "" {
			return errors.New("malformed service error")
		}
		return resp.Error
	}
	if !bytes.Equal(resp.ID, id) {
		return fmt.Errorf("unexpected response id %s", resp.ID)
	}
	if len(resp.Result) == 0 {
		return errors.New("service response has neither result nor error")
	}
	if err := json.Unmarshal(resp.Result, result); err != nil {
		return fmt.Errorf("failed to decode service result: %w", err)
	}
	return nil
}

func consumeOperationStream(dec *json.Decoder, operationID string, callback func(OperationStatus) error) error {
	lastSeq := 0
	for {
		var note rpcNotification
		if err := dec.Decode(&note); err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("operation stream ended before a terminal event")
			}
			return fmt.Errorf("failed to decode operation event: %w", err)
		}
		if note.JSONRPC != jsonrpcVersion || note.Method != notificationOperationStatus {
			return fmt.Errorf("unexpected stream message %q", note.Method)
		}
		var record OperationStatus
		if err := json.Unmarshal(note.Params, &record); err != nil {
			return fmt.Errorf("failed to decode operation event: %w", err)
		}
		if record.OperationID != operationID {
			return fmt.Errorf("unexpected stream operationId %q", record.OperationID)
		}
		if !validOperationState(record.State) || strings.TrimSpace(record.ItemName) == "" || strings.TrimSpace(record.DisplayName) == "" ||
			record.ProgressPercent < 0 || record.ProgressPercent > 100 || strings.TrimSpace(record.TimestampUTC) == "" || record.Seq != lastSeq+1 {
			return errors.New("malformed operation event")
		}
		lastSeq = record.Seq
		if err := callback(record); err != nil {
			return err
		}
		if IsTerminalOperationState(record.State) {
			return nil
		}
	}
}

func validOperationState(state string) bool {
	switch state {
	case "Queued", "Downloading", "Installing", "Removing", "ItemCompleted", "ItemFailed", "Succeeded", "Failed", "Deferred", "Canceled":
		return true
	default:
		return false
	}
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

// closeOnContextDone closes conn when ctx ends, which unblocks a read or
// write in progress on either transport. The returned func stops watching.
func closeOnContextDone(ctx context.Context, conn io.Closer) func() {
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
