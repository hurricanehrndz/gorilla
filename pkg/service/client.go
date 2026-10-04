package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func (c *Client) ListOptionalInstalls(ctx context.Context) ([]OptionalInstallItem, error) {
	resp, err := c.doRequest(ctx, newClientRequest(actionListOptionalInstalls, "", ""), nil)
	if err != nil {
		return nil, err
	}
	payload, err := decodeEnvelopePayload[listOptionalInstallsResponse](resp.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ListOptionalInstalls payload: %w", err)
	}
	if payload.Items == nil {
		return nil, errors.New("malformed ListOptionalInstalls payload")
	}
	for _, item := range payload.Items {
		if strings.TrimSpace(item.ItemName) == "" || strings.TrimSpace(item.DisplayName) == "" {
			return nil, errors.New("malformed ListOptionalInstalls item identity")
		}
	}
	return payload.Items, nil
}

// GetBranding returns the organisation branding the service resolved from
// policy and config. Unset fields are empty strings.
func (c *Client) GetBranding(ctx context.Context) (branding.Branding, error) {
	resp, err := c.doRequest(ctx, newClientRequest(actionGetBranding, "", ""), nil)
	if err != nil {
		return branding.Branding{}, err
	}
	payload, err := decodeEnvelopePayload[branding.Branding](resp.Payload)
	if err != nil {
		return branding.Branding{}, fmt.Errorf("failed to decode GetBranding payload: %w", err)
	}
	return payload, nil
}

func (c *Client) InstallItem(ctx context.Context, itemName string) (AcceptedOperation, error) {
	return c.mutate(ctx, actionInstallItem, itemName)
}

func (c *Client) RemoveItem(ctx context.Context, itemName string) (AcceptedOperation, error) {
	return c.mutate(ctx, actionRemoveItem, itemName)
}

func (c *Client) mutate(ctx context.Context, action, itemName string) (AcceptedOperation, error) {
	itemName = strings.TrimSpace(itemName)
	if itemName == "" {
		return AcceptedOperation{}, fmt.Errorf("%s requires itemName", action)
	}
	resp, err := c.doRequest(ctx, newClientRequest(action, itemName, ""), nil)
	if err != nil {
		return AcceptedOperation{}, err
	}
	accepted, err := decodeEnvelopePayload[AcceptedOperation](resp.Payload)
	if err != nil {
		return AcceptedOperation{}, fmt.Errorf("failed to decode %s payload: %w", action, err)
	}
	if !accepted.Accepted {
		return AcceptedOperation{}, errors.New("service did not accept operation")
	}
	if strings.TrimSpace(accepted.QueuedAtUTC) == "" || (accepted.OperationID != "" && accepted.OperationID != resp.OperationID) {
		return AcceptedOperation{}, errors.New("malformed operation accepted payload")
	}
	accepted.OperationID = resp.OperationID
	return accepted, nil
}

func (c *Client) StreamOperationStatus(ctx context.Context, operationID string, callback func(OperationStatus) error) error {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return errors.New("StreamOperationStatus requires operationId")
	}
	if callback == nil {
		return errors.New("StreamOperationStatus requires callback")
	}
	_, err := c.doRequest(ctx, newClientRequest(actionStreamOperationStatus, "", operationID), callback)
	return err
}

// CancelOperation asks the service to cancel operationID. The service refuses
// with operation_not_cancelable once the item's install or removal has started,
// the operation has finished, or the operation is unknown.
func (c *Client) CancelOperation(ctx context.Context, operationID string) error {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return errors.New("CancelOperation requires operationId")
	}
	resp, err := c.doRequest(ctx, newClientRequest(actionCancelOperation, "", operationID), nil)
	if err != nil {
		return err
	}
	payload, err := decodeEnvelopePayload[cancelOperationResponse](resp.Payload)
	if err != nil {
		return fmt.Errorf("failed to decode CancelOperation payload: %w", err)
	}
	if !payload.Canceled {
		return errors.New("service did not cancel the operation")
	}
	return nil
}

func newClientRequest(action, itemName, operationID string) serviceEnvelope[any] {
	req := serviceEnvelope[any]{
		Version:      pipeProtocolVersion,
		MessageType:  messageTypeRequest,
		Operation:    action,
		RequestID:    newRequestID(),
		OperationID:  operationID,
		TimestampUTC: nowRFC3339UTC(),
		Payload:      listOptionalInstallsRequest{},
	}
	switch action {
	case actionInstallItem:
		req.Payload = installItemRequest{ItemName: itemName}
	case actionRemoveItem:
		req.Payload = removeItemRequest{ItemName: itemName}
	case actionStreamOperationStatus:
		req.Payload = streamOperationStatusRequest{}
	case actionCancelOperation:
		req.Payload = cancelOperationRequest{}
	}
	return req
}

func decodeClientResponse(dec *json.Decoder, req serviceEnvelope[any], resp *serviceEnvelope[json.RawMessage]) error {
	if err := dec.Decode(resp); err != nil {
		return fmt.Errorf("failed to decode service response: %w", err)
	}
	if resp.Version != pipeProtocolVersion {
		return fmt.Errorf("unexpected protocol version %q", resp.Version)
	}
	if resp.Operation != req.Operation {
		return fmt.Errorf("unexpected response operation %q", resp.Operation)
	}
	if resp.RequestID != req.RequestID {
		return fmt.Errorf("unexpected response requestId %q", resp.RequestID)
	}

	switch resp.MessageType {
	case messageTypeError:
		if resp.OperationID != req.OperationID {
			return fmt.Errorf("unexpected response operationId %q", resp.OperationID)
		}
		payload, err := decodeEnvelopePayload[errorResponsePayload](resp.Payload)
		if err != nil {
			return fmt.Errorf("failed to decode service error payload: %w", err)
		}
		if strings.TrimSpace(payload.ErrorCode) == "" || strings.TrimSpace(payload.ErrorMessage) == "" {
			return errors.New("malformed service error payload")
		}
		return fmt.Errorf("%s: %s", payload.ErrorCode, payload.ErrorMessage)
	case messageTypeResponse:
	default:
		return fmt.Errorf("unexpected response messageType %q", resp.MessageType)
	}

	switch req.Operation {
	case actionListOptionalInstalls, actionGetBranding:
		if resp.OperationID != "" {
			return fmt.Errorf("unexpected response operationId %q", resp.OperationID)
		}
	case actionInstallItem, actionRemoveItem:
		if strings.TrimSpace(resp.OperationID) == "" {
			return errors.New("response is missing operationId")
		}
	case actionStreamOperationStatus, actionCancelOperation:
		if resp.OperationID != req.OperationID {
			return fmt.Errorf("unexpected response operationId %q", resp.OperationID)
		}
	default:
		return fmt.Errorf("unsupported response operation %q", req.Operation)
	}
	return nil
}

func consumeOperationStream(dec *json.Decoder, requestID, operationID string, callback func(OperationStatus) error) error {
	for {
		var envelope serviceEnvelope[json.RawMessage]
		if err := dec.Decode(&envelope); err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("operation stream ended before a terminal event")
			}
			return fmt.Errorf("failed to decode operation event: %w", err)
		}
		if envelope.Version != pipeProtocolVersion {
			return fmt.Errorf("unexpected protocol version %q", envelope.Version)
		}
		if envelope.Operation != actionStreamOperationStatus {
			return fmt.Errorf("unexpected stream operation %q", envelope.Operation)
		}
		if envelope.OperationID != operationID {
			return fmt.Errorf("unexpected stream operationId %q", envelope.OperationID)
		}
		switch envelope.MessageType {
		case messageTypeError:
			if envelope.RequestID != requestID {
				return fmt.Errorf("unexpected stream requestId %q", envelope.RequestID)
			}
			payload, err := decodeEnvelopePayload[errorResponsePayload](envelope.Payload)
			if err != nil {
				return fmt.Errorf("failed to decode service error payload: %w", err)
			}
			if strings.TrimSpace(payload.ErrorCode) == "" || strings.TrimSpace(payload.ErrorMessage) == "" {
				return errors.New("malformed service error payload")
			}
			return fmt.Errorf("%s: %s", payload.ErrorCode, payload.ErrorMessage)
		case messageTypeEvent:
			if envelope.RequestID != "" {
				return fmt.Errorf("unexpected stream requestId %q", envelope.RequestID)
			}
		default:
			return fmt.Errorf("unexpected stream messageType %q", envelope.MessageType)
		}
		if strings.TrimSpace(envelope.TimestampUTC) == "" {
			return errors.New("malformed operation event: empty timestampUtc")
		}
		payload, err := decodeEnvelopePayload[OperationStatusPayload](envelope.Payload)
		if err != nil {
			return fmt.Errorf("failed to decode operation event payload: %w", err)
		}
		if !validOperationState(payload.State) || strings.TrimSpace(payload.ItemName) == "" || strings.TrimSpace(payload.DisplayName) == "" || payload.ProgressPercent < 0 || payload.ProgressPercent > 100 {
			return errors.New("malformed operation event payload")
		}
		record := OperationStatus{
			OperationID:     envelope.OperationID,
			TimestampUTC:    envelope.TimestampUTC,
			ItemName:        payload.ItemName,
			DisplayName:     payload.DisplayName,
			State:           payload.State,
			ProgressPercent: payload.ProgressPercent,
			Message:         payload.Message,
			ErrorCode:       payload.ErrorCode,
			ErrorMessage:    payload.ErrorMessage,
			CanceledBy:      payload.CanceledBy,
		}
		if err := callback(record); err != nil {
			return err
		}
		if IsTerminalOperationState(payload.State) {
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
