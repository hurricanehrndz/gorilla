package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestClientDefaults(t *testing.T) {
	client := NewClient("")
	if client.PipeName != DefaultPipeName || client.ConnectTimeout != defaultConnectTimeout || client.ResponseTimeout != defaultResponseTimeout {
		t.Fatalf("unexpected client defaults: %#v", client)
	}
}

func TestDecodeClientResponseValidatesEnvelope(t *testing.T) {
	req := newClientRequest(actionStreamOperationStatus, "", "op-1")
	valid := serviceEnvelope[any]{
		Version:      pipeProtocolVersion,
		MessageType:  messageTypeResponse,
		Operation:    req.Operation,
		RequestID:    req.RequestID,
		OperationID:  req.OperationID,
		TimestampUTC: nowRFC3339UTC(),
		Payload:      streamOperationStatusAckResponse{StreamAccepted: true},
	}

	tests := []struct {
		name   string
		mutate func(*serviceEnvelope[any])
	}{
		{"version", func(resp *serviceEnvelope[any]) { resp.Version = "v2" }},
		{"message type", func(resp *serviceEnvelope[any]) { resp.MessageType = messageTypeEvent }},
		{"operation", func(resp *serviceEnvelope[any]) { resp.Operation = actionInstallItem }},
		{"request ID", func(resp *serviceEnvelope[any]) { resp.RequestID = "wrong" }},
		{"operation ID", func(resp *serviceEnvelope[any]) { resp.OperationID = "wrong" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := valid
			tt.mutate(&resp)
			var decoded serviceEnvelope[json.RawMessage]
			if err := decodeClientResponse(json.NewDecoder(strings.NewReader(mustJSON(t, resp))), req, &decoded); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	var decoded serviceEnvelope[json.RawMessage]
	if err := decodeClientResponse(json.NewDecoder(strings.NewReader(mustJSON(t, valid))), req, &decoded); err != nil {
		t.Fatalf("valid response rejected: %v", err)
	}
}

func TestDecodeClientResponseRejectsErrorEnvelope(t *testing.T) {
	req := newClientRequest(actionListOptionalInstalls, "", "")
	resp := serviceEnvelope[errorResponsePayload]{
		Version:     pipeProtocolVersion,
		MessageType: messageTypeError,
		Operation:   req.Operation,
		RequestID:   req.RequestID,
		Payload: errorResponsePayload{
			ErrorCode:    "command_failed",
			ErrorMessage: "boom",
		},
	}
	var decoded serviceEnvelope[json.RawMessage]
	if err := decodeClientResponse(json.NewDecoder(strings.NewReader(mustJSON(t, resp))), req, &decoded); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected service error, got %v", err)
	}
}

func TestConsumeOperationStreamTerminalAndItemFailure(t *testing.T) {
	operationID := "op-" + t.Name()
	requestID := t.Name()
	events := []serviceEnvelope[OperationStatusPayload]{
		statusEnvelope(operationID, OperationStatusPayload{ItemName: "Dependency", DisplayName: "Dependency", State: "ItemFailed", ProgressPercent: 50}),
		statusEnvelope(operationID, OperationStatusPayload{ItemName: "Requested", DisplayName: "Requested", State: "Deferred", ProgressPercent: 100}),
	}
	var input strings.Builder
	for _, event := range events {
		input.WriteString(mustJSON(t, event))
		input.WriteByte('\n')
	}
	var got []OperationStatus
	if err := consumeOperationStream(json.NewDecoder(strings.NewReader(input.String())), requestID, operationID, func(status OperationStatus) error {
		got = append(got, status)
		return nil
	}); err != nil {
		t.Fatalf("consume stream: %v", err)
	}
	if len(got) != 2 || got[0].State != "ItemFailed" || got[1].State != "Deferred" {
		t.Fatalf("unexpected statuses: %#v", got)
	}
	if got[0].OperationID != operationID || got[0].TimestampUTC == "" {
		t.Fatalf("missing envelope fields: %#v", got[0])
	}
	if !IsTerminalOperationState("Deferred") || IsTerminalOperationState("ItemFailed") {
		t.Fatal("terminal state detection is incorrect")
	}
}

func TestConsumeOperationStreamErrors(t *testing.T) {
	operationID := "op-" + t.Name()
	requestID := t.Name()
	event := statusEnvelope(operationID, OperationStatusPayload{ItemName: "Item", DisplayName: "Item", State: "Installing", ProgressPercent: 50})
	input := mustJSON(t, event) + "\n"

	callbackErr := errors.New("stop")
	if err := consumeOperationStream(json.NewDecoder(strings.NewReader(input)), requestID, operationID, func(OperationStatus) error { return callbackErr }); !errors.Is(err, callbackErr) {
		t.Fatalf("expected callback error, got %v", err)
	}
	if err := consumeOperationStream(json.NewDecoder(strings.NewReader(input)), requestID, operationID, func(OperationStatus) error { return nil }); err == nil || !strings.Contains(err.Error(), "before a terminal") {
		t.Fatalf("expected premature EOF error, got %v", err)
	}

	malformed := statusEnvelope(operationID, OperationStatusPayload{State: "Installing", ProgressPercent: 50})
	if err := consumeOperationStream(json.NewDecoder(strings.NewReader(mustJSON(t, malformed))), requestID, operationID, func(OperationStatus) error { return nil }); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("expected malformed record error, got %v", err)
	}

	wrongOperation := statusEnvelope("wrong", OperationStatusPayload{ItemName: "Item", DisplayName: "Item", State: "Installing", ProgressPercent: 50})
	if err := consumeOperationStream(json.NewDecoder(strings.NewReader(mustJSON(t, wrongOperation))), requestID, operationID, func(OperationStatus) error { return nil }); err == nil || !strings.Contains(err.Error(), "operationId") {
		t.Fatalf("expected operationId correlation error, got %v", err)
	}
}

func statusEnvelope(operationID string, payload OperationStatusPayload) serviceEnvelope[OperationStatusPayload] {
	return serviceEnvelope[OperationStatusPayload]{
		Version:      pipeProtocolVersion,
		MessageType:  messageTypeEvent,
		Operation:    actionStreamOperationStatus,
		OperationID:  operationID,
		TimestampUTC: nowRFC3339UTC(),
		Payload:      payload,
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
