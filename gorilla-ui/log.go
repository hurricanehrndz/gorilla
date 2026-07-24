package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

func setupLogger() (*slog.Logger, *lumberjack.Logger, error) {
	if os.Getenv("GORILLA_UI_DEBUG") != "1" && os.Getenv("GORILLA_DEBUG") != "1" {
		return slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, nil
	}

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, errors.New("LOCALAPPDATA is not set")
	}

	directory := filepath.Join(localAppData, "gorilla")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, err
	}

	writer := &lumberjack.Logger{
		Filename:   filepath.Join(directory, "ui-client.log"),
		MaxSize:    10,
		MaxBackups: 1,
	}
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug})), writer, nil
}
