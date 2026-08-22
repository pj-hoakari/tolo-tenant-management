// Package logging builds the slog handler the service logs through.
//
// Records are written as one JSON object per line, in the shape Cloud Logging
// parses out of a container's stdout: the severity, message and timestamp use
// the keys it reads, and the trace of the request the record belongs to is
// attached with the logging.googleapis.com/* fields it correlates on. Nothing
// here is specific to Google Cloud at run time, so the same output stays
// readable as plain JSON elsewhere.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// LevelCritical is the level that maps to Cloud Logging's CRITICAL severity.
// It sits above slog.LevelError, which maps to ERROR.
const LevelCritical = slog.LevelError + 4

// The keys Cloud Logging reads out of a structured entry. The first three take
// the place of slog's own time, level and message keys; the rest are the
// special-purpose fields it recognises inside the JSON payload.
const (
	keyTime           = "time"
	keySeverity       = "severity"
	keyMessage        = "message"
	keySourceLocation = "logging.googleapis.com/sourceLocation"
	keyTrace          = "logging.googleapis.com/trace"
	keySpanID         = "logging.googleapis.com/spanId"
	keyTraceSampled   = "logging.googleapis.com/trace_sampled"
)

// Options configures the handler NewHandler builds.
type Options struct {
	// Level is the lowest level that is logged. A nil Leveler means
	// slog.LevelInfo.
	Level slog.Leveler
	// AddSource records the file, line and function of each call site, at the
	// cost of a stack lookup per record.
	AddSource bool
	// ProjectID is the Google Cloud project the trace resource name is built
	// from. It may be empty; the bare trace ID is logged then, which Cloud
	// Logging cannot correlate with a trace.
	ProjectID string
}

// NewHandler returns a handler that writes Cloud Logging compatible JSON
// records to w and adds the trace of each record's context.
func NewHandler(w io.Writer, opts Options) slog.Handler {
	inner := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       opts.Level,
		AddSource:   opts.AddSource,
		ReplaceAttr: replaceAttr,
	})

	return newTraceHandler(inner, nil, opts.ProjectID)
}

// NewLogger returns a logger over NewHandler(w, opts).
func NewLogger(w io.Writer, opts Options) *slog.Logger {
	return slog.New(NewHandler(w, opts))
}

// ParseLevel reads a configured level name, accepting both slog's names and the
// Cloud Logging severities this package emits, in any case.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	case "critical":
		return LevelCritical, nil
	default:
		return 0, fmt.Errorf("unknown log level %q", s)
	}
}

// replaceAttr renames the built-in attributes of a record to the keys Cloud
// Logging reads. Only the record's own attributes are renamed: inside a group
// the same key means something the caller chose, not a built-in.
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) != 0 {
		return a
	}

	switch a.Key {
	case slog.TimeKey:
		return slog.String(keyTime, a.Value.Time().UTC().Format(time.RFC3339Nano))
	case slog.LevelKey:
		level, ok := a.Value.Any().(slog.Level)
		if !ok {
			return a
		}

		return slog.String(keySeverity, severityOf(level))
	case slog.MessageKey:
		return slog.String(keyMessage, a.Value.String())
	case slog.SourceKey:
		source, ok := a.Value.Any().(*slog.Source)
		if !ok {
			return a
		}

		return slog.Attr{Key: keySourceLocation, Value: slog.GroupValue(
			slog.String("file", source.File),
			// Cloud Logging types the line number as a string.
			slog.String("line", strconv.Itoa(source.Line)),
			slog.String("function", source.Function),
		)}
	default:
		return a
	}
}

// severityOf maps a level onto the Cloud Logging severity that covers it. The
// severities in between (NOTICE, ALERT, EMERGENCY) have no slog counterpart and
// are not emitted.
func severityOf(level slog.Level) string {
	switch {
	case level < slog.LevelInfo:
		return "DEBUG"
	case level < slog.LevelWarn:
		return "INFO"
	case level < slog.LevelError:
		return "WARNING"
	case level < LevelCritical:
		return "ERROR"
	default:
		return "CRITICAL"
	}
}

// traceHandler adds the trace fields of a record's context to what the inner
// handler writes, so that an entry and the span it happened in line up in Cloud
// Logging without any call site passing the trace itself.
//
// Cloud Logging only reads those fields as top-level keys, so the handler keeps
// the ungrouped base handler alongside the WithAttrs and WithGroup calls made
// on it, and adds the trace fields to the base rather than to whatever group is
// open. It keeps the derived handler ready as well, for Enabled and for the
// records that carry no span.
type traceHandler struct {
	base      slog.Handler
	inner     slog.Handler
	ops       []handlerOp
	projectID string
}

// handlerOp is one WithAttrs or WithGroup call made on the base handler. A
// non-empty group names a WithGroup call; slog gives an empty group name no
// effect, so the two never collide.
type handlerOp struct {
	attrs []slog.Attr
	group string
}

func (o handlerOp) apply(handler slog.Handler) slog.Handler {
	if o.group != "" {
		return handler.WithGroup(o.group)
	}

	return handler.WithAttrs(o.attrs)
}

// newTraceHandler applies ops to base once, so that the common path of a record
// without a span logs through a handler that is already built.
func newTraceHandler(base slog.Handler, ops []handlerOp, projectID string) *traceHandler {
	inner := base
	for _, op := range ops {
		inner = op.apply(inner)
	}

	return &traceHandler{base: base, inner: inner, ops: ops, projectID: projectID}
}

func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *traceHandler) Handle(ctx context.Context, record slog.Record) error {
	spanContext := trace.SpanFromContext(ctx).SpanContext()
	if !spanContext.IsValid() {
		return h.inner.Handle(ctx, record)
	}

	// The trace fields go onto the base handler and the groups and attributes
	// of this handler are replayed over them, which puts the fields at the top
	// level and leaves the record's own attributes inside the open group.
	// Rebuilding the chain costs a record's worth of work, which is acceptable
	// because only the records made under an active span pay it.
	handler := h.base.WithAttrs([]slog.Attr{
		slog.String(keyTrace, h.traceResourceName(spanContext.TraceID().String())),
		slog.String(keySpanID, spanContext.SpanID().String()),
		slog.Bool(keyTraceSampled, spanContext.IsSampled()),
	})

	for _, op := range h.ops {
		handler = op.apply(handler)
	}

	return handler.Handle(ctx, record)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	return newTraceHandler(h.base, h.appendOp(handlerOp{attrs: attrs, group: ""}), h.projectID)
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	return newTraceHandler(h.base, h.appendOp(handlerOp{attrs: nil, group: name}), h.projectID)
}

// appendOp copies the recorded operations before appending, so that two
// handlers derived from the same one never share backing storage.
func (h *traceHandler) appendOp(op handlerOp) []handlerOp {
	ops := make([]handlerOp, 0, len(h.ops)+1)
	ops = append(ops, h.ops...)

	return append(ops, op)
}

// traceResourceName names the trace the way Cloud Logging resolves it. The
// project is part of that name, so without one the entry carries the bare trace
// ID and can be matched by eye but not by Cloud Logging itself.
func (h *traceHandler) traceResourceName(traceID string) string {
	if h.projectID == "" {
		return traceID
	}

	return "projects/" + h.projectID + "/traces/" + traceID
}
