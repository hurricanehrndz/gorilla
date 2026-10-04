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

func TestDecodeResponseValidatesResponse(t *testing.T) {
	id := json.RawMessage(`"req-1"`)
	valid := `{"jsonrpc":"2.0","id":"req-1","result":{"streamAccepted":true}}`

	for name, input := range map[string]string{
		"version":     `{"jsonrpc":"1.0","id":"req-1","result":{}}`,
		"id":          `{"jsonrpc":"2.0","id":"other","result":{}}`,
		"null id":     `{"jsonrpc":"2.0","id":null,"result":{}}`,
		"no result":   `{"jsonrpc":"2.0","id":"req-1"}`,
		"bad result":  `{"jsonrpc":"2.0","id":"req-1","result":{"streamAccepted":"yes"}}`,
		"error id":    `{"jsonrpc":"2.0","id":"other","error":{"code":-32001,"message":"boom","data":{"code":"command_failed"}}}`,
		"error shape": `{"jsonrpc":"2.0","id":"req-1","error":{"code":-32001,"message":"boom"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var ack streamOperationStatusAckResponse
			if err := decodeResponse(json.NewDecoder(strings.NewReader(input)), id, &ack); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	var ack streamOperationStatusAckResponse
	if err := decodeResponse(json.NewDecoder(strings.NewReader(valid)), id, &ack); err != nil || !ack.StreamAccepted {
		t.Fatalf("valid response rejected: ack=%#v err=%v", ack, err)
	}
}

// An error the service sends before reading the request (server_busy) has a
// null id. The v1 client could not read it at all.
func TestDecodeResponseReturnsServiceError(t *testing.T) {
	for _, id := range []string{`"req-1"`, `null`} {
		input := `{"jsonrpc":"2.0","id":` + id + `,"error":{"code":-32000,"message":"service is busy; retry shortly","data":{"code":"server_busy"}}}`
		var result struct{}
		err := decodeResponse(json.NewDecoder(strings.NewReader(input)), json.RawMessage(`"req-1"`), &result)
		var rpcErr *Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != codeServerBusy || !IsErrorCode(err, "server_busy") {
			t.Fatalf("id %s: expected a server_busy *Error, got %v", id, err)
		}
		if err.Error() != "server_busy: service is busy; retry shortly" {
			t.Fatalf("id %s: error text = %q", id, err.Error())
		}
	}
}

func TestConsumeOperationStreamTerminalAndItemFailure(t *testing.T) {
	operationID := "op-" + t.Name()
	input := statusNotification(t, OperationStatus{OperationID: operationID, Seq: 1, ItemName: "Dependency", DisplayName: "Dependency", State: "ItemFailed", ProgressPercent: 50}) +
		statusNotification(t, OperationStatus{OperationID: operationID, Seq: 2, ItemName: "Requested", DisplayName: "Requested", State: "Deferred", ProgressPercent: 100})
	var got []OperationStatus
	if err := consumeOperationStream(json.NewDecoder(strings.NewReader(input)), operationID, func(status OperationStatus) error {
		got = append(got, status)
		return nil
	}); err != nil {
		t.Fatalf("consume stream: %v", err)
	}
	if len(got) != 2 || got[0].State != "ItemFailed" || got[1].State != "Deferred" || got[1].Seq != 2 {
		t.Fatalf("unexpected statuses: %#v", got)
	}
	if !IsTerminalOperationState("Deferred") || IsTerminalOperationState("ItemFailed") {
		t.Fatal("terminal state detection is incorrect")
	}
}

func TestConsumeOperationStreamErrors(t *testing.T) {
	operationID := "op-" + t.Name()
	record := OperationStatus{OperationID: operationID, Seq: 1, ItemName: "Item", DisplayName: "Item", State: "Installing", ProgressPercent: 50}
	input := statusNotification(t, record)

	callbackErr := errors.New("stop")
	if err := consumeOperationStream(json.NewDecoder(strings.NewReader(input)), operationID, func(OperationStatus) error { return callbackErr }); !errors.Is(err, callbackErr) {
		t.Fatalf("expected callback error, got %v", err)
	}
	if err := consumeOperationStream(json.NewDecoder(strings.NewReader(input)), operationID, func(OperationStatus) error { return nil }); err == nil || !strings.Contains(err.Error(), "before a terminal") {
		t.Fatalf("expected premature EOF error, got %v", err)
	}

	malformed := record
	malformed.ItemName = ""
	skipped := record
	skipped.Seq = 2
	for name, bad := range map[string]OperationStatus{"identity": malformed, "seq gap": skipped} {
		if err := consumeOperationStream(json.NewDecoder(strings.NewReader(statusNotification(t, bad))), operationID, func(OperationStatus) error { return nil }); err == nil || !strings.Contains(err.Error(), "malformed") {
			t.Fatalf("%s: expected malformed record error, got %v", name, err)
		}
	}

	wrongOperation := record
	wrongOperation.OperationID = "wrong"
	if err := consumeOperationStream(json.NewDecoder(strings.NewReader(statusNotification(t, wrongOperation))), operationID, func(OperationStatus) error { return nil }); err == nil || !strings.Contains(err.Error(), "operationId") {
		t.Fatalf("expected operationId correlation error, got %v", err)
	}
}

func statusNotification(t *testing.T, record OperationStatus) string {
	t.Helper()
	if record.TimestampUTC == "" {
		record.TimestampUTC = nowRFC3339UTC()
	}
	return mustJSON(t, rpcNotification{JSONRPC: jsonrpcVersion, Method: notificationOperationStatus, Params: json.RawMessage(mustJSON(t, record))}) + "\n"
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
