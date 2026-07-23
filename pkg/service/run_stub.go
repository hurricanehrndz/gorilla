//go:build !windows

package service

import (
	"errors"

	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/installer"
	"github.com/1dustindavis/gorilla/pkg/report"
)

func Run(_ config.Configuration, _ func(config.Configuration, installer.ProgressFn) (*report.Report, error)) error {
	return errors.New("service mode is only supported on Windows")
}
