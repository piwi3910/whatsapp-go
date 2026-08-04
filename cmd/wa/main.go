// cmd/wa/main.go
package main

import (
	"bytes"
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/piwi3910/whatsapp-go/internal/config"
)

func main() {
	setupLogging()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// setupLogging installs the process-wide slog handler.
//
// Format defaults to JSON in a container (where logs are scraped by a
// collector that wants structure) and to text elsewhere (where a human is
// reading them). WA_LOG_FORMAT and WA_LOG_LEVEL override.
func setupLogging() {
	level := slog.LevelInfo
	if v := os.Getenv("WA_LOG_LEVEL"); v != "" {
		// Unparseable levels fall back to info rather than failing startup.
		_ = level.UnmarshalText([]byte(strings.ToUpper(v)))
	}
	opts := &slog.HandlerOptions{Level: level}

	jsonOut := config.InContainer()
	switch strings.ToLower(os.Getenv("WA_LOG_FORMAT")) {
	case "json":
		jsonOut = true
	case "text":
		jsonOut = false
	}

	var h slog.Handler
	if jsonOut {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))

	// Packages still on the standard logger (HTTP middleware, whatsmeow
	// glue) would otherwise emit unstructured lines alongside the structured
	// ones. Funnel them through slog so a log collector sees one format.
	// Those packages are owned elsewhere; this needs no change to them.
	log.SetFlags(0)
	log.SetPrefix("")
	log.SetOutput(slogWriter{})
}

// slogWriter adapts io.Writer to slog, one record per line written.
type slogWriter struct{}

func (slogWriter) Write(p []byte) (int, error) {
	msg := string(bytes.TrimRight(p, "\n"))
	slog.Info(msg, "via", "stdlib-log")
	return len(p), nil
}
