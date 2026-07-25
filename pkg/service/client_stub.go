//go:build !windows

package service

import (
	"context"
	"encoding/json"
	"errors"
)

var errServiceCommandsUnsupported = errors.New("service commands are only supported on Windows")

func (c *Client) doRequest(_ context.Context, _ serviceEnvelope[any], _ func(OperationStatus) error) (serviceEnvelope[json.RawMessage], error) {
	return serviceEnvelope[json.RawMessage]{}, errServiceCommandsUnsupported
}
