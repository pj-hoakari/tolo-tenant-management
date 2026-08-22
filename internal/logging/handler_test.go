package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// The W3C trace context example identifiers, used wherever a test needs a trace
// it can recognise in the output.
const (
	testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	testSpanID  = "00f067aa0ba902b7"
)

func TestSeverityUsesCloudLoggingNames(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		level slog.Level
		want  string
	}{
		"debug":          {level: slog.LevelDebug, want: "DEBUG"},
		"below info":     {level: slog.LevelInfo - 1, want: "DEBUG"},
		"info":           {level: slog.LevelInfo, want: "INFO"},
		"above info":     {level: slog.LevelInfo + 1, want: "INFO"},
		"warn":           {level: slog.LevelWarn, want: "WARNING"},
		"error":          {level: slog.LevelError, want: "ERROR"},
		"below critical": {level: LevelCritical - 1, want: "ERROR"},
		"critical":       {level: LevelCritical, want: "CRITICAL"},
		"above critical": {level: LevelCritical + 1, want: "CRITICAL"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			logger := NewLogger(&buf, Options{Level: slog.LevelDebug - 4, AddSource: false, ProjectID: ""})
			logger.Log(context.Background(), tt.level, "hello")

			if got := decodeLine(t, &buf)[keySeverity]; got != tt.want {
				t.Errorf("%s = %v, want %q", keySeverity, got, tt.want)
			}
		})
	}
}

func TestRecordUsesCloudLoggingKeys(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := NewLogger(&buf, Options{Level: slog.LevelInfo, AddSource: false, ProjectID: ""})
	logger.Info("server listening", "addr", ":8080")

	entry := decodeLine(t, &buf)

	if got, want := entry[keyMessage], "server listening"; got != want {
		t.Errorf("%s = %v, want %q", keyMessage, got, want)
	}

	if got, want := entry["addr"], ":8080"; got != want {
		t.Errorf("addr = %v, want %q", got, want)
	}

	// slog's own message and level keys must be gone; its time key happens to
	// be the one Cloud Logging reads, so it stays.
	for _, key := range []string{slog.MessageKey, slog.LevelKey} {
		if _, ok := entry[key]; ok {
			t.Errorf("entry has key %q, want it renamed", key)
		}
	}

	timestamp, ok := entry[keyTime].(string)
	if !ok {
		t.Fatalf("%s = %#v, want a string", keyTime, entry[keyTime])
	}

	if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
		t.Errorf("%s = %q, want RFC3339Nano: %v", keyTime, timestamp, err)
	}

	if !strings.HasSuffix(timestamp, "Z") {
		t.Errorf("%s = %q, want it in UTC", keyTime, timestamp)
	}
}

func TestSourceLocationIsAGroupWithAStringLine(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := NewLogger(&buf, Options{Level: slog.LevelInfo, AddSource: true, ProjectID: ""})
	logger.Info("with source")

	entry := decodeLine(t, &buf)

	source, ok := entry[keySourceLocation].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want a group", keySourceLocation, entry[keySourceLocation])
	}

	if file, ok := source["file"].(string); !ok || !strings.HasSuffix(file, "handler_test.go") {
		t.Errorf("file = %#v, want this test file", source["file"])
	}

	// Cloud Logging types the line number as a string, so a number here would
	// make it drop the whole source location.
	if line, ok := source["line"].(string); !ok || line == "" || line == "0" {
		t.Errorf("line = %#v, want a non-zero line number as a string", source["line"])
	}

	if function, ok := source["function"].(string); !ok || !strings.Contains(function, "TestSourceLocationIsAGroupWithAStringLine") {
		t.Errorf("function = %#v, want this test function", source["function"])
	}
}

func TestTraceFieldsFollowTheContextSpan(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		projectID string
		wantTrace string
	}{
		"with a project ID":    {projectID: "tolo-example", wantTrace: "projects/tolo-example/traces/" + testTraceID},
		"without a project ID": {projectID: "", wantTrace: testTraceID},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			logger := NewLogger(&buf, Options{Level: slog.LevelInfo, AddSource: false, ProjectID: tt.projectID})
			logger.ErrorContext(contextWithSampledSpan(t), "internal error")

			assertTraceFields(t, decodeLine(t, &buf), tt.wantTrace)
		})
	}
}

func TestTraceFieldsAreAbsentWithoutASpan(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := NewLogger(&buf, Options{Level: slog.LevelInfo, AddSource: false, ProjectID: "tolo-example"})
	logger.ErrorContext(context.Background(), "internal error")

	entry := decodeLine(t, &buf)

	for _, key := range []string{keyTrace, keySpanID, keyTraceSampled} {
		if _, ok := entry[key]; ok {
			t.Errorf("entry has key %q, want no trace fields without a span", key)
		}
	}
}

func TestDerivedHandlersKeepTraceFields(t *testing.T) {
	t.Parallel()

	t.Run("WithAttrs", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer

		logger := NewLogger(&buf, Options{Level: slog.LevelInfo, AddSource: false, ProjectID: "tolo-example"}).With("service", "tenant-management")
		logger.ErrorContext(contextWithSampledSpan(t), "internal error")

		entry := decodeLine(t, &buf)

		if got, want := entry["service"], "tenant-management"; got != want {
			t.Errorf("service = %v, want %q", got, want)
		}

		assertTraceFields(t, entry, "projects/tolo-example/traces/"+testTraceID)
	})

	t.Run("WithGroup", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer

		logger := NewLogger(&buf, Options{Level: slog.LevelInfo, AddSource: false, ProjectID: "tolo-example"}).WithGroup("request")
		logger.ErrorContext(contextWithSampledSpan(t), "internal error")

		// An open group nests every attribute of the record, the trace fields
		// included, which is why the service logs through an ungrouped logger.
		group, ok := decodeLine(t, &buf)["request"].(map[string]any)
		if !ok {
			t.Fatalf("request = %#v, want a group", decodeLine(t, &buf)["request"])
		}

		assertTraceFields(t, group, "projects/tolo-example/traces/"+testTraceID)
	})
}

func TestParseLevel(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		"debug":         {in: "debug", want: slog.LevelDebug, wantErr: false},
		"info":          {in: "info", want: slog.LevelInfo, wantErr: false},
		"warn":          {in: "warn", want: slog.LevelWarn, wantErr: false},
		"warning":       {in: "warning", want: slog.LevelWarn, wantErr: false},
		"error":         {in: "error", want: slog.LevelError, wantErr: false},
		"critical":      {in: "critical", want: LevelCritical, wantErr: false},
		"upper case":    {in: "WARNING", want: slog.LevelWarn, wantErr: false},
		"mixed case":    {in: "Critical", want: LevelCritical, wantErr: false},
		"unknown":       {in: "verbose", want: 0, wantErr: true},
		"empty":         {in: "", want: 0, wantErr: true},
		"numeric":       {in: "4", want: 0, wantErr: true},
		"leading space": {in: " info", want: 0, wantErr: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseLevel(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseLevel(%q) error = %v, wantErr %t", tt.in, err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// assertTraceFields checks the three fields Cloud Logging correlates on.
func assertTraceFields(t *testing.T, entry map[string]any, wantTrace string) {
	t.Helper()

	if got := entry[keyTrace]; got != wantTrace {
		t.Errorf("%s = %v, want %q", keyTrace, got, wantTrace)
	}

	if got := entry[keySpanID]; got != testSpanID {
		t.Errorf("%s = %v, want %q", keySpanID, got, testSpanID)
	}

	if got := entry[keyTraceSampled]; got != true {
		t.Errorf("%s = %v, want true", keyTraceSampled, got)
	}
}

// contextWithSampledSpan returns a context carrying a valid, sampled span
// context, as a request that the tracing interceptor has already handled does.
func contextWithSampledSpan(t *testing.T) context.Context {
	t.Helper()

	traceID, err := trace.TraceIDFromHex(testTraceID)
	if err != nil {
		t.Fatalf("TraceIDFromHex() error = %v", err)
	}

	spanID, err := trace.SpanIDFromHex(testSpanID)
	if err != nil {
		t.Fatalf("SpanIDFromHex() error = %v", err)
	}

	return trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))
}

// decodeLine parses the single JSON record the buffer holds.
func decodeLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("nothing was logged")
	}

	if strings.Contains(line, "\n") {
		t.Fatalf("log = %q, want a single record", line)
	}

	var entry map[string]any

	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}

	return entry
}
