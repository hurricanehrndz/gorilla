package gorillalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/1dustindavis/gorilla/pkg/config"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

var (
	logMu     sync.Mutex
	checkonly bool
	logWriter *lumberjack.Logger

	// consoleOut is the console sink; overridable in tests.
	consoleOut io.Writer = os.Stdout
)

// SetOutput redirects the console sink; intended for tests. It takes effect
// on the next NewLog call.
func SetOutput(w io.Writer) {
	logMu.Lock()
	defer logMu.Unlock()
	consoleOut = w
}

// consoleHandler returns a text handler for consoleOut, or nil when consoleOut
// is a real file with no usable handle — e.g. the NULL stdout of an
// SCM-started Windows service. Non-file writers (test buffers) skip the probe.
func consoleHandler(opts *slog.HandlerOptions) slog.Handler {
	if f, ok := consoleOut.(*os.File); ok {
		if _, err := f.Stat(); err != nil {
			return nil
		}
	}
	return slog.NewTextHandler(consoleOut, opts)
}

// fanoutHandler forwards each record to every child handler that has the
// record's level enabled.
type fanoutHandler struct {
	handlers []slog.Handler
}

func (f fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	// Sinks are independent: one failing child (e.g. an invalid stdout handle
	// when running as a Windows service) must not block the others.
	var errs []error
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (f fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithAttrs(attrs)
	}
	return fanoutHandler{handlers: hs}
}

func (f fanoutHandler) WithGroup(name string) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithGroup(name)
	}
	return fanoutHandler{handlers: hs}
}

// NewLog builds the fan-out logger: a text console sink plus, unless checkonly
// is active, a rotated file sink (structured JSON by default, plain text when
// cfg.LogFilePlain). The result is installed as slog's default logger.
func NewLog(cfg config.Configuration) error {
	logMu.Lock()
	defer logMu.Unlock()

	checkonly = cfg.CheckOnly

	consoleLevel := slog.LevelWarn
	if cfg.Verbose {
		consoleLevel = slog.LevelInfo
	}
	if cfg.Debug {
		consoleLevel = slog.LevelDebug
	}

	// UI text sink extension point: Workstream D adds a third child handler
	// here (alongside the console and file sinks) that renders records for the
	// UI; D owns wiring that handler's transport. The fan-out handler already
	// forwards every record to each enabled child, so no other change is needed.
	var handlers []slog.Handler
	if h := consoleHandler(&slog.HandlerOptions{Level: consoleLevel}); h != nil {
		handlers = append(handlers, h)
	}

	if !checkonly {
		logPath := filepath.Join(cfg.AppDataPath, "gorilla.log")
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return fmt.Errorf("unable to create log directory %s: %w", filepath.Dir(logPath), err)
		}

		if logWriter != nil {
			_ = logWriter.Close()
		}
		logWriter = &lumberjack.Logger{
			Filename:   logPath,
			MaxSize:    10,
			MaxBackups: 3,
			MaxAge:     28,
			Compress:   true,
		}

		fileLevel := slog.LevelInfo
		if cfg.Debug {
			fileLevel = slog.LevelDebug
		}
		fileOpts := &slog.HandlerOptions{Level: fileLevel}

		var fileHandler slog.Handler
		if cfg.LogFilePlain {
			fileHandler = slog.NewTextHandler(logWriter, fileOpts)
		} else {
			fileHandler = slog.NewJSONHandler(logWriter, fileOpts)
		}
		handlers = append(handlers, fileHandler)
	}

	if len(handlers) == 0 {
		// checkonly with no usable stdout: nowhere to log, but slog.New
		// requires a handler.
		handlers = append(handlers, slog.DiscardHandler)
	}

	slog.SetDefault(slog.New(fanoutHandler{handlers: handlers}))
	return nil
}

// Close releases the active log file writer and resets logging state.
func Close() {
	logMu.Lock()
	defer logMu.Unlock()

	if logWriter != nil {
		_ = logWriter.Close()
		logWriter = nil
	}
	checkonly = false
	h := consoleHandler(nil)
	if h == nil {
		h = slog.DiscardHandler
	}
	slog.SetDefault(slog.New(h))
}

// join renders variadic args into a single message with space separation,
// matching the historic log.Println formatting the call sites rely on.
func join(args []interface{}) string {
	return strings.TrimSuffix(fmt.Sprintln(args...), "\n")
}

// Debug logs at DEBUG level. Handler levels gate whether it is emitted.
func Debug(logStrings ...interface{}) {
	slog.Default().Debug(join(logStrings))
}

// Info logs at INFO level.
func Info(logStrings ...interface{}) {
	slog.Default().Info(join(logStrings))
}

// Warn logs at WARN level.
func Warn(logStrings ...interface{}) {
	slog.Default().Warn(join(logStrings))
}

// Error logs at ERROR level and then panics (recoverable). It is a no-op when
// checkonly is active.
func Error(logStrings ...interface{}) {
	if checkonly {
		return
	}
	msg := join(logStrings)
	slog.Default().Error(msg)
	panic(msg)
}
