package obs

import (
	"context"
	"io"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/trace"
)

type LoggerConfig struct {
	Service string
	Env     string
	Version string
	Writer  io.Writer // defaults to os.Stdout
	Level   slog.Level
}

type Logger struct {
	*slog.Logger
}

func NewLogger(cfg LoggerConfig) *Logger {
	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: cfg.Level})
	handler := &traceHandler{Handler: base}
	logger := slog.New(handler).With(
		slog.String("service", cfg.Service),
		slog.String("env", cfg.Env),
		slog.String("version", cfg.Version),
	)
	return &Logger{Logger: logger}
}

type traceHandler struct{ slog.Handler }

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
}
