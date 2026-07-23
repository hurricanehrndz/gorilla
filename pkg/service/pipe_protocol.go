package service

import (
	"encoding/json"
	"strconv"
	"time"
)

const (
	pipeProtocolVersion = "v1"

	messageTypeRequest  = "Request"
	messageTypeResponse = "Response"
	messageTypeEvent    = "Event"
	messageTypeError    = "Error"
)

type serviceEnvelope[T any] struct {
	Version      string `json:"version"`
	MessageType  string `json:"messageType"`
	Operation    string `json:"operation"`
	RequestID    string `json:"requestId"`
	OperationID  string `json:"operationId"`
	TimestampUTC string `json:"timestampUtc"`
	Payload      T      `json:"payload"`
}

type listOptionalInstallsRequest struct{}

type installItemRequest struct {
	ItemName string `json:"itemName"`
}

type removeItemRequest struct {
	ItemName string `json:"itemName"`
}

type streamOperationStatusRequest struct{}

type OptionalInstallItem struct {
	ItemName           string `json:"itemName"`
	DisplayName        string `json:"displayName"`
	Version            string `json:"version"`
	Catalog            string `json:"catalog"`
	InstallerType      string `json:"installerType"`
	InstallerPackageID string `json:"installerPackageId"`
	InstallerLocation  string `json:"installerLocation"`
	Description        string `json:"description,omitempty"`
	Category           string `json:"category,omitempty"`
	Developer          string `json:"developer,omitempty"`
	IconName           string `json:"iconName,omitempty"`
	RestartAction      string `json:"restartAction,omitempty"`
	IsManaged          bool   `json:"isManaged"`
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

// OperationStatusPayload is one wire status event. ProgressPercent is scoped to
// ItemName and may reset when the item changes; it is not aggregate operation
// progress. Only Succeeded, Failed, Deferred, and Canceled terminate a stream.
type OperationStatusPayload struct {
	ItemName        string `json:"itemName"`
	DisplayName     string `json:"displayName"`
	State           string `json:"state"`
	ProgressPercent int    `json:"progressPercent"`
	Message         string `json:"message"`
	ErrorCode       string `json:"errorCode,omitempty"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
	CanceledBy      string `json:"canceledBy,omitempty"`
}

// OperationStatus is the client-facing status record, including correlation
// and timing fields carried by the event envelope.
type OperationStatus struct {
	OperationID     string `json:"operationId"`
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

type errorResponsePayload struct {
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
}

func nowRFC3339UTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func newRequestID() string {
	return strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
}

func decodeEnvelopePayload[T any](raw json.RawMessage) (T, error) {
	var payload T
	if len(raw) == 0 || string(raw) == "null" {
		return payload, nil
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}
