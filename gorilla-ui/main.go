package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/1dustindavis/gorilla/pkg/service"
	"github.com/wailsapp/wails/v3/pkg/application"
)

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

	uiService := &UIService{
		client: service.NewClient(*pipeName),
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
		Title:           "Gorilla UI",
		URL:             "/",
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
