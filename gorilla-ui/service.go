package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/1dustindavis/gorilla/pkg/branding"
	gorillaservice "github.com/1dustindavis/gorilla/pkg/service"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const operationStatusEvent = "gorilla:operation-status"

type serviceClient interface {
	ListOptionalInstalls(context.Context) ([]gorillaservice.OptionalInstallItem, error)
	GetBranding(context.Context) (branding.Branding, error)
	InstallItem(context.Context, string) (gorillaservice.AcceptedOperation, error)
	RemoveItem(context.Context, string) (gorillaservice.AcceptedOperation, error)
	StreamOperationStatus(context.Context, string, func(gorillaservice.OperationStatus) error) error
	CancelOperation(context.Context, string) error
}

// UIService is the complete Wails-bound backend surface.
type UIService struct {
	client serviceClient
	logger *slog.Logger
	ctx    context.Context
	app    *application.App
}

//nolint:unparam // Wails ServiceStartup contract requires the error result
func (s *UIService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.ctx = ctx
	s.app = application.Get()
	s.logger.Debug("service started", "result", "ok")
	return nil
}

// callContext returns the Wails application context, refusing calls that arrive
// before startup so a nil context never reaches the pipe client.
func (s *UIService) callContext() (context.Context, error) {
	if s.ctx == nil {
		return nil, errors.New("UI service is not running")
	}
	return s.ctx, nil
}

func (s *UIService) ListOptionalInstalls() ([]gorillaservice.OptionalInstallItem, error) {
	started := time.Now()
	ctx, err := s.callContext()
	if err != nil {
		s.logResult("ListOptionalInstalls", "", err, started)
		return nil, err
	}
	items, err := s.client.ListOptionalInstalls(ctx)
	s.logResult("ListOptionalInstalls", "", err, started)
	return items, err
}

// GetBranding returns the organisation branding the service resolved from policy
// and config. The UI never reads either source itself.
func (s *UIService) GetBranding() (branding.Branding, error) {
	started := time.Now()
	ctx, err := s.callContext()
	if err != nil {
		s.logResult("GetBranding", "", err, started)
		return branding.Branding{}, err
	}
	b, err := s.client.GetBranding(ctx)
	s.logResult("GetBranding", "", err, started)
	return b, err
}

func (s *UIService) InstallItem(itemName string) (gorillaservice.AcceptedOperation, error) {
	return s.mutate("InstallItem", itemName, s.client.InstallItem)
}

func (s *UIService) RemoveItem(itemName string) (gorillaservice.AcceptedOperation, error) {
	return s.mutate("RemoveItem", itemName, s.client.RemoveItem)
}

func (s *UIService) mutate(operation, itemName string, call func(context.Context, string) (gorillaservice.AcceptedOperation, error)) (gorillaservice.AcceptedOperation, error) {
	started := time.Now()
	itemName = strings.TrimSpace(itemName)
	if itemName == "" {
		err := errors.New("itemName is required")
		s.logResult(operation, "", err, started)
		return gorillaservice.AcceptedOperation{}, err
	}
	ctx, err := s.callContext()
	if err != nil {
		s.logResult(operation, "", err, started)
		return gorillaservice.AcceptedOperation{}, err
	}
	accepted, err := call(ctx, itemName)
	s.logResult(operation, accepted.OperationID, err, started)
	return accepted, err
}

func (s *UIService) WatchOperation(operationID string) error {
	started := time.Now()
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		err := errors.New("operationId is required")
		s.logResult("WatchOperation", "", err, started)
		return err
	}
	ctx, err := s.callContext()
	if err == nil && s.app == nil {
		err = errors.New("UI service is not running")
	}
	if err != nil {
		s.logResult("WatchOperation", operationID, err, started)
		return err
	}

	err = s.client.StreamOperationStatus(ctx, operationID, func(status gorillaservice.OperationStatus) error {
		s.logger.Debug(
			"operation status",
			"operation", "WatchOperation",
			"operationId", status.OperationID,
			"state", status.State,
		)
		s.app.Event.Emit(operationStatusEvent, status)
		return nil
	})
	s.logResult("WatchOperation", operationID, err, started)
	return err
}

// CancelOperation asks the service to cancel an operation whose item has not
// been acted on yet. A refusal comes back as an error carrying the service's
// operation_not_cancelable message.
func (s *UIService) CancelOperation(operationID string) error {
	started := time.Now()
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		err := errors.New("operationId is required")
		s.logResult("CancelOperation", "", err, started)
		return err
	}
	ctx, err := s.callContext()
	if err == nil {
		err = s.client.CancelOperation(ctx, operationID)
	}
	s.logResult("CancelOperation", operationID, err, started)
	return err
}

func (s *UIService) logResult(operation, operationID string, err error, started time.Time) {
	result := "ok"
	if errors.Is(err, context.Canceled) {
		result = "canceled"
	} else if err != nil {
		result = "error"
	}
	args := []any{
		"operation", operation,
		"result", result,
		"durationMs", time.Since(started).Milliseconds(),
	}
	if operationID != "" {
		args = append(args, "operationId", operationID)
	}
	if err != nil {
		args = append(args, "error", err)
	}
	s.logger.Debug("binding call completed", args...)
}
