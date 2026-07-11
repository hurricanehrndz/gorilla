package gorillalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/1dustindavis/gorilla/pkg/config"
)

// setConsole redirects the console sink to a buffer and restores state on
// cleanup so tests can assert what reaches stdout.
func setConsole(t *testing.T) *bytes.Buffer {
	t.Helper()
	orig := consoleOut
	buf := &bytes.Buffer{}
	consoleOut = buf
	t.Cleanup(func() {
		Close()
		consoleOut = orig
	})
	return buf
}

func readLog(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "gorilla.log"))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	return data
}

// TestNewLogCreatesDirAndFile verifies the log directory and file materialize
// under AppDataPath.
func TestNewLogCreatesDirAndFile(t *testing.T) {
	setConsole(t)
	dir := filepath.Join(t.TempDir(), "nested")

	if err := NewLog(config.Configuration{AppDataPath: dir}); err != nil {
		t.Fatalf("NewLog failed: %v", err)
	}
	Info("seed") // lumberjack creates the file on first write

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("log directory not created: %s", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "gorilla.log")); os.IsNotExist(err) {
		t.Errorf("log file not created under %s", dir)
	}
}

// TestFanoutWritesToConsoleAndFile proves one call reaches every active sink.
func TestFanoutWritesToConsoleAndFile(t *testing.T) {
	console := setConsole(t)
	dir := t.TempDir()

	if err := NewLog(config.Configuration{AppDataPath: dir, Verbose: true}); err != nil {
		t.Fatalf("NewLog failed: %v", err)
	}
	Warn("fanout-message")

	if !strings.Contains(console.String(), "fanout-message") {
		t.Errorf("console sink missing message: %q", console.String())
	}
	if !strings.Contains(string(readLog(t, dir)), "fanout-message") {
		t.Errorf("file sink missing message")
	}
}

// failingHandler always errors on Handle, like a console TextHandler writing
// to an invalid stdout handle under a Windows service.
type failingHandler struct{}

func (failingHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (failingHandler) Handle(context.Context, slog.Record) error { return errors.New("bad handle") }
func (failingHandler) WithAttrs([]slog.Attr) slog.Handler        { return failingHandler{} }
func (failingHandler) WithGroup(string) slog.Handler             { return failingHandler{} }

// TestFanoutSurvivesFailingChild encodes the Windows-service case: stdout is
// an invalid handle so the console handler errors on every write; the file
// sink must still receive the record (sinks are independent).
func TestFanoutSurvivesFailingChild(t *testing.T) {
	buf := &bytes.Buffer{}
	fan := fanoutHandler{handlers: []slog.Handler{
		failingHandler{},
		slog.NewTextHandler(buf, nil),
	}}

	rec := slog.NewRecord(time.Now(), slog.LevelWarn, "still-delivered", 0)
	err := fan.Handle(context.Background(), rec)

	if !strings.Contains(buf.String(), "still-delivered") {
		t.Errorf("second sink missing record after first sink failed: %q", buf.String())
	}
	if err == nil {
		t.Errorf("expected the failing child's error to be surfaced")
	}
}

// TestFileEncodingJSONDefault asserts the file sink emits structured JSON.
func TestFileEncodingJSONDefault(t *testing.T) {
	setConsole(t)
	dir := t.TempDir()

	if err := NewLog(config.Configuration{AppDataPath: dir}); err != nil {
		t.Fatalf("NewLog failed: %v", err)
	}
	Warn("json-line")

	line := bytes.TrimSpace(readLog(t, dir))
	var rec map[string]any
	if err := json.Unmarshal(line, &rec); err != nil {
		t.Fatalf("file line is not JSON: %v (%q)", err, line)
	}
	if rec["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", rec["level"])
	}
	if rec["msg"] != "json-line" {
		t.Errorf("msg = %v, want json-line", rec["msg"])
	}
}

// TestFileEncodingPlain asserts LogFilePlain swaps the file sink to text.
func TestFileEncodingPlain(t *testing.T) {
	setConsole(t)
	dir := t.TempDir()

	if err := NewLog(config.Configuration{AppDataPath: dir, LogFilePlain: true}); err != nil {
		t.Fatalf("NewLog failed: %v", err)
	}
	Warn("plain-line")

	line := bytes.TrimSpace(readLog(t, dir))
	if err := json.Unmarshal(line, &map[string]any{}); err == nil {
		t.Errorf("expected non-JSON text line, got JSON: %q", line)
	}
	if !strings.Contains(string(line), "plain-line") {
		t.Errorf("plain line missing message: %q", line)
	}
}

// TestConsoleLevelGating checks verbose/debug drive which levels reach console.
func TestConsoleLevelGating(t *testing.T) {
	tests := []struct {
		name           string
		verbose, debug bool
		infoVisible    bool
		debugVisible   bool
	}{
		{"default", false, false, false, false},
		{"verbose", true, false, true, false},
		{"debug", false, true, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			console := setConsole(t)
			dir := t.TempDir()
			if err := NewLog(config.Configuration{AppDataPath: dir, Verbose: tc.verbose, Debug: tc.debug}); err != nil {
				t.Fatalf("NewLog failed: %v", err)
			}

			Debug("dbg-msg")
			Info("info-msg")
			Warn("warn-msg")
			out := console.String()

			if got := strings.Contains(out, "info-msg"); got != tc.infoVisible {
				t.Errorf("info visible = %v, want %v: %q", got, tc.infoVisible, out)
			}
			if got := strings.Contains(out, "dbg-msg"); got != tc.debugVisible {
				t.Errorf("debug visible = %v, want %v: %q", got, tc.debugVisible, out)
			}
			if !strings.Contains(out, "warn-msg") {
				t.Errorf("warn must always reach console: %q", out)
			}
		})
	}
}

// TestCheckOnlyNoFileAndErrorNoPanic confirms checkonly suppresses the file
// sink and neuters Error's panic.
func TestCheckOnlyNoFileAndErrorNoPanic(t *testing.T) {
	setConsole(t)
	dir := t.TempDir()

	if err := NewLog(config.Configuration{AppDataPath: dir, CheckOnly: true}); err != nil {
		t.Fatalf("NewLog failed: %v", err)
	}
	if logWriter != nil {
		t.Errorf("checkonly should not open a file writer")
	}

	Warn("checkonly-warn")
	if _, err := os.Stat(filepath.Join(dir, "gorilla.log")); !os.IsNotExist(err) {
		t.Errorf("checkonly must not create a log file, stat err = %v", err)
	}

	// Must not panic under checkonly.
	Error("should-not-panic")
}

// TestErrorPanicsAndLogs confirms Error logs to both sinks then panics, and
// that the file sink is a lumberjack writer under AppDataPath.
func TestErrorPanicsAndLogs(t *testing.T) {
	console := setConsole(t)
	dir := t.TempDir()

	if err := NewLog(config.Configuration{AppDataPath: dir}); err != nil {
		t.Fatalf("NewLog failed: %v", err)
	}
	if logWriter == nil {
		t.Fatal("expected a lumberjack file writer")
	}
	if want := filepath.Join(dir, "gorilla.log"); logWriter.Filename != want {
		t.Errorf("writer filename = %q, want %q", logWriter.Filename, want)
	}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("Error did not panic")
			}
		}()
		Error("boom")
	}()

	if !strings.Contains(console.String(), "boom") {
		t.Errorf("Error missing from console: %q", console.String())
	}
	if !strings.Contains(string(readLog(t, dir)), "boom") {
		t.Errorf("Error missing from file")
	}
}
