package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/czarnik/msk-account-cli/internal/telemetry"
)

var (
	// L is the global logger used across the application.
	L       *slog.Logger
	logFile *os.File
	once    sync.Once
)

// Setup initializes slog to write JSON logs into the given directory.
// Log file name pattern: msk-account-cli-YYYYMMDD.log
func Setup(dir string) error {
	var err error
	once.Do(func() {
		if err = os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		fname := fmt.Sprintf("msk-account-cli-%s.log", time.Now().Format("20060102"))
		path := filepath.Join(dir, fname)
		logFile, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
		if err != nil {
			return
		}
		fileHandler := slog.NewJSONHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo})

		// Optionally add an OTel logs handler (if telemetry is initialized)
		var handlers []slog.Handler
		handlers = append(handlers, fileHandler)
		if telemetry.LogProviderReady() {
			if h := telemetry.OtelSlogHandler("github.com/czarnik/msk-account-cli/logging"); h != nil {
				handlers = append(handlers, h)
			}
		}
		L = slog.New(Multi(handlers...))
	})
	return err
}

// Close closes the current log file if open.
func Close() error {
	if logFile != nil {
		return logFile.Close()
	}
	return nil
}

// SanitizeArgs masks secrets in CLI arguments (e.g., --password, --sasl-password).
func SanitizeArgs(args []string) []string {
	out := make([]string, 0, len(args))
	maskNext := false
	for _, a := range args {
		if maskNext {
			out = append(out, "********")
			maskNext = false
			continue
		}
		// handle --flag=value
		if strings.HasPrefix(a, "--") && strings.Contains(a, "=") {
			parts := strings.SplitN(a, "=", 2)
			key := strings.ToLower(parts[0])
			val := parts[1]
			if isSensitiveKey(key) {
				out = append(out, parts[0]+"="+maskString(val))
			} else {
				out = append(out, a)
			}
			continue
		}
		// handle --flag value
		if strings.HasPrefix(a, "--") {
			key := strings.ToLower(a)
			out = append(out, a)
			if isSensitiveKey(key) {
				maskNext = true
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

// SanitizeKV returns a copy of key/value pairs with sensitive values masked.
// It expects pairs like ("key", value, "key2", value2, ...).
func SanitizeKV(kv ...any) []any {
	if len(kv) == 0 {
		return kv
	}
	out := make([]any, len(kv))
	copy(out, kv)
	for i := 0; i+1 < len(out); i += 2 {
		key, ok := out[i].(string)
		if !ok {
			continue
		}
		if isSensitiveKey(key) {
			out[i+1] = maskAny(out[i+1])
		}
	}
	return out
}

func isSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	return strings.Contains(k, "password") || strings.Contains(k, "sasl-password") || strings.Contains(k, "passwd") || strings.Contains(k, "secret") || strings.Contains(k, "token") || strings.HasSuffix(k, "_password")
}

func maskAny(v any) any {
	switch t := v.(type) {
	case string:
		return maskString(t)
	default:
		return "********"
	}
}

func maskString(s string) string {
	if s == "" {
		return ""
	}
	// Show at most last 2 characters to help debugging which secret was used.
	if len(s) <= 2 {
		return "**"
	}
	return strings.Repeat("*", len(s)-2) + s[len(s)-2:]
}

// Multi returns a slog.Handler that fans out records to all provided handlers.
func Multi(hs ...slog.Handler) slog.Handler {
	switch len(hs) {
	case 0:
		return slog.NewTextHandler(os.Stdout, nil)
	case 1:
		return hs[0]
	default:
		return &multiHandler{hs: hs}
	}
}

type multiHandler struct{ hs []slog.Handler }

func (m *multiHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	for _, h := range m.hs {
		if h.Enabled(ctx, lvl) {
			return true
		}
	}
	return false
}
func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, h := range m.hs {
		if err := h.Handle(ctx, r); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		out[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{hs: out}
}
func (m *multiHandler) WithGroup(name string) slog.Handler {
	out := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		out[i] = h.WithGroup(name)
	}
	return &multiHandler{hs: out}
}
