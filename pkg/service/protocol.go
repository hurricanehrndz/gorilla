package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The service speaks JSON-RPC 2.0 (https://www.jsonrpc.org/specification):
// one newline-delimited JSON request per connection and one response, except
// streamOperationStatus, which follows its response with operationStatus
// notifications until the operation ends. gorilla-ui/ARCHITECTURE.md
// ("Protocol") is the contract.
const (
	jsonrpcVersion = "2.0"
	// apiVersion is bumped only for breaking changes to the methods below;
	// additions are announced through getServiceInfo's capabilities.
	apiVersion = 1

	methodGetServiceInfo        = "getServiceInfo"
	methodListOptionalInstalls  = "listOptionalInstalls"
	methodGetBranding           = "getBranding"
	methodInstallItem           = "installItem"
	methodRemoveItem            = "removeItem"
	methodCancelOperation       = "cancelOperation"
	methodStreamOperationStatus = "streamOperationStatus"

	// notificationOperationStatus carries one status record on a stream.
	notificationOperationStatus = "operationStatus"
)

// capabilities is every method the service answers, for getServiceInfo.
var capabilities = []string{
	methodGetServiceInfo,
	methodListOptionalInstalls,
	methodGetBranding,
	methodInstallItem,
	methodRemoveItem,
	methodCancelOperation,
	methodStreamOperationStatus,
}

// JSON-RPC error codes. The standard ones come from the specification; the
// application ones sit in the reserved server range -32000..-32099. Every
// error also carries a stable string in data.code, which is what clients
// branch on.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603

	codeServerBusy             = -32000
	codeCommandFailed          = -32001
	codeOperationNotCancelable = -32002
	codeUnknownOperation       = -32003
	codeItemNotAvailable       = -32004
	codeItemNotRemovable       = -32005
)

// appCodes maps each JSON-RPC error code to its data.code string.
var appCodes = map[int]string{
	codeParseError:             "parse_error",
	codeInvalidRequest:         "invalid_request",
	codeMethodNotFound:         "method_not_found",
	codeInvalidParams:          "invalid_params",
	codeInternalError:          "internal_error",
	codeServerBusy:             "server_busy",
	codeCommandFailed:          "command_failed",
	codeOperationNotCancelable: "operation_not_cancelable",
	codeUnknownOperation:       "unknown_operation",
	codeItemNotAvailable:       "item_not_available",
	codeItemNotRemovable:       "item_not_removable",
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type rpcNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// Error is a JSON-RPC error object from the service. Data.Code is the stable
// application code (operation_not_cancelable, server_busy, ...).
type Error struct {
	Code    int       `json:"code"`
	Message string    `json:"message"`
	Data    ErrorData `json:"data"`
}

type ErrorData struct {
	Code        string `json:"code"`
	OperationID string `json:"operationId,omitempty"`
}

// Error reads "<data.code>: <message>", the form the CLI prints and the UI
// strips the code from.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Data.Code, e.Message)
}

func newError(code int, message, operationID string) *Error {
	return &Error{Code: code, Message: message, Data: ErrorData{Code: appCodes[code], OperationID: operationID}}
}

// IsErrorCode reports whether err is a service error with data.code appCode.
func IsErrorCode(err error, appCode string) bool {
	var rpcErr *Error
	return errors.As(err, &rpcErr) && rpcErr.Data.Code == appCode
}

type itemParams struct {
	ItemName string `json:"itemName"`
}

type operationParams struct {
	OperationID string `json:"operationId"`
}

// ServiceInfo is getServiceInfo's result. It skips the command queue, so it
// doubles as a cheap health check.
type ServiceInfo struct {
	Version         string   `json:"version"`
	ProtocolVersion string   `json:"protocolVersion"`
	APIVersion      int      `json:"apiVersion"`
	Capabilities    []string `json:"capabilities"`
	Busy            bool     `json:"busy"`
	UptimeSeconds   int64    `json:"uptimeSeconds"`
}

// OptionalInstallItem is one offered self-service item. IsRequired is whether
// an admin manifest also lists it in managed_installs: it is installed for
// everyone and cannot be removed through self-service.
type OptionalInstallItem struct {
	ItemName           string `json:"itemName"`
	DisplayName        string `json:"displayName"`
	Version            string `json:"version"`
	Catalog            string `json:"catalog"`
	Description        string `json:"description,omitempty"`
	Category           string `json:"category,omitempty"`
	Developer          string `json:"developer,omitempty"`
	IconName           string `json:"iconName,omitempty"`
	RestartAction      string `json:"restartAction,omitempty"`
	IsManaged          bool   `json:"isManaged"`
	IsRequired         bool   `json:"isRequired"`
	IsInstalled        bool   `json:"isInstalled"`
	Status             string `json:"status"`
	StatusUpdatedAtUTC string `json:"statusUpdatedAtUtc"`
	LastOperationID    string `json:"lastOperationId,omitempty"`
}

type listOptionalInstallsResponse struct {
	Items []OptionalInstallItem `json:"items"`
}

type AcceptedOperation struct {
	OperationID string `json:"operationId,omitempty"`
	Accepted    bool   `json:"accepted"`
	QueuedAtUTC string `json:"queuedAtUtc"`
}

type streamOperationStatusAckResponse struct {
	StreamAccepted bool `json:"streamAccepted"`
}

type cancelOperationResponse struct {
	Canceled bool `json:"canceled"`
}

// OperationStatus is one status record, the params of an operationStatus
// notification. Seq numbers an operation's records from 1 in the order the
// service recorded them, so a client can order records that arrive out of
// order. ProgressPercent is scoped to ItemName and may reset when the item
// changes; it is not aggregate operation progress. Only Succeeded, Failed,
// Deferred, and Canceled end an operation.
type OperationStatus struct {
	OperationID     string `json:"operationId"`
	Seq             int    `json:"seq"`
	TimestampUTC    string `json:"timestampUtc"`
	ItemName        string `json:"itemName"`
	DisplayName     string `json:"displayName"`
	State           string `json:"state"`
	ProgressPercent int    `json:"progressPercent"`
	Message         string `json:"message"`
	ErrorCode       string `json:"errorCode,omitempty"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
	CanceledBy      string `json:"canceledBy,omitempty"`
}

func IsTerminalOperationState(state string) bool {
	switch state {
	case "Succeeded", "Failed", "Deferred", "Canceled":
		return true
	default:
		return false
	}
}

// timestampLayout is RFC 3339 in UTC with fixed millisecond precision, the
// same shape as JavaScript's Date.toISOString, so records sort as strings.
const timestampLayout = "2006-01-02T15:04:05.000Z07:00"

func nowRFC3339UTC() string {
	return time.Now().UTC().Format(timestampLayout)
}
