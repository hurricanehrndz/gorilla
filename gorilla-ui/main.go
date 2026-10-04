package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/1dustindavis/gorilla/pkg/branding"
	"github.com/1dustindavis/gorilla/pkg/service"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const defaultWindowTitle = "Gorilla UI"

// defaultIcon is the window and taskbar icon when no branding logo is set.
// The executable carries no icon resource, so without this Windows shows the
// generic program icon.
//
//go:embed icon.png
var defaultIcon []byte

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
	// The window chrome is branded here, once, before it opens: title, icon
	// and caption colour. The frontend applies the rest of the branding.
	brand := startupBranding(client.GetBranding, logger)
	app := application.New(application.Options{
		Name:     "Gorilla UI",
		Logger:   logger,
		Services: []application.Service{application.NewService(uiService)},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(bundledAssets()),
		},
		Icon: windowIcon(brand),
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:            "Gorilla UI",
		Title:           windowTitle(brand),
		URL:             "/",
		Width:           1100,
		Height:          760,
		DevToolsEnabled: false,
		Windows: application.WindowsWindow{
			CustomTheme: captionTheme(brand.Accent),
		},
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

// startupBranding is the branding the window chrome is built from, or the zero
// value when the service cannot be reached in time: the window then opens with
// the defaults and the frontend re-applies branding from its cache.
func startupBranding(get func(context.Context) (branding.Branding, error), logger *slog.Logger) branding.Branding {
	ctx, cancel := context.WithTimeout(context.Background(), brandingTimeout)
	defer cancel()
	b, err := get(ctx)
	if err != nil {
		logger.Debug("branding unavailable for the window chrome", "error", err)
		return branding.Branding{}
	}
	return b
}

// windowTitle is the branded title, or "Gorilla UI" when none is set.
func windowTitle(b branding.Branding) string {
	if b.Title == "" {
		return defaultWindowTitle
	}
	return b.Title
}

// windowIcon is the branding logo when it is a PNG, which is what the Windows
// icon loader accepts, else the embedded default. It is the title bar,
// taskbar and Alt+Tab icon.
func windowIcon(b branding.Branding) []byte {
	if b.LogoMime != "image/png" {
		return defaultIcon
	}
	logo, err := base64.StdEncoding.DecodeString(b.LogoBase64)
	if err != nil || len(logo) == 0 {
		return defaultIcon
	}
	return logo
}

var accentPattern = regexp.MustCompile(`^#([0-9a-fA-F]{6})$`)

// captionTheme colours the native title bar with the branding accent so it
// reads as one band with the banner below it: Windows 11 honours these DWM
// caption colours, Windows 10 ignores them and keeps its default caption. The
// title text is black or white, whichever contrasts with the accent. An unset
// or invalid accent leaves the system theme alone.
func captionTheme(accent string) application.ThemeSettings {
	m := accentPattern.FindStringSubmatch(accent)
	if m == nil {
		return application.ThemeSettings{}
	}
	rgb, err := strconv.ParseUint(m[1], 16, 32)
	if err != nil {
		return application.ThemeSettings{}
	}
	r, g, b := uint32(rgb>>16)&0xff, uint32(rgb>>8)&0xff, uint32(rgb)&0xff
	// DWM takes COLORREF, which is 0x00BBGGRR.
	caption := r | g<<8 | b<<16
	// Perceived brightness (ITU-R BT.601 luma); 150 of 255 splits the hues
	// where white text stops reading well.
	text := uint32(0xffffff)
	if (299*r+587*g+114*b)/1000 >= 150 {
		text = 0x000000
	}
	theme := &application.WindowTheme{
		BorderColour:    &caption,
		TitleBarColour:  &caption,
		TitleTextColour: &text,
	}
	return application.ThemeSettings{
		DarkModeActive:    theme,
		DarkModeInactive:  theme,
		LightModeActive:   theme,
		LightModeInactive: theme,
	}
}
