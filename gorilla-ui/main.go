package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/1dustindavis/gorilla/pkg/branding"
	"github.com/1dustindavis/gorilla/pkg/service"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const defaultWindowTitle = "Gorilla UI"

func main() {
	pipeName := flag.String("pipe-name", service.DefaultPipeName, "Gorilla service pipe name")
	flag.Parse()

	// ponytail: startup diagnostics go to stderr, which is invisible in the
	// -H windowsgui build; surface them through a message box or the event log
	// if they ever need to reach the user.
	logger, logWriter, logErr := setupLogger()
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "Gorilla UI diagnostics unavailable: %v\n", logErr)
	}
	if logWriter != nil {
		defer func() { _ = logWriter.Close() }()
	}

	client := service.NewClient(*pipeName)
	uiService := &UIService{
		client: client,
		logger: logger,
	}
	app := application.New(application.Options{
		Name:     "Gorilla UI",
		Logger:   logger,
		Services: []application.Service{application.NewService(uiService)},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(bundledAssets()),
		},
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:            "Gorilla UI",
		Title:           windowTitle(client.GetBranding, logger),
		URL:             "/",
		Width:           1100,
		Height:          760,
		DevToolsEnabled: false,
	})

	logger.Debug("application starting", "result", "start")
	if err := app.Run(); err != nil {
		logger.Debug("application stopped", "result", "error", "error", err)
		fmt.Fprintf(os.Stderr, "Gorilla UI failed: %v\n", err)
		os.Exit(1)
	}
	logger.Debug("application stopped", "result", "ok")
}

// brandingTimeout bounds the one branding lookup made before the window opens.
// It is shorter than the pipe client's five-second connect timeout, so a
// stopped service costs startup at most this long.
const brandingTimeout = 2 * time.Second

// windowTitle is the branded title, or "Gorilla UI" when branding is unset or
// the service cannot be reached in time. The frontend applies the rest of the
// branding itself.
func windowTitle(get func(context.Context) (branding.Branding, error), logger *slog.Logger) string {
	ctx, cancel := context.WithTimeout(context.Background(), brandingTimeout)
	defer cancel()
	b, err := get(ctx)
	if err != nil {
		logger.Debug("branding unavailable for the window title", "error", err)
		return defaultWindowTitle
	}
	if b.Title == "" {
		return defaultWindowTitle
	}
	return b.Title
}
