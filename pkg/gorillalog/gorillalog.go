package gorillalog

import (
	"context"
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
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				return err
			}
		}
	}
	return nil
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

	handlers := []slog.Handler{
		slog.NewTextHandler(consoleOut, &slog.HandlerOptions{Level: consoleLevel}),
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
	slog.SetDefault(slog.New(slog.NewTextHandler(consoleOut, nil)))
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
