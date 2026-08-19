package telemetry_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/telemetry"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestSetupWithoutEndpointKeepsTracingNoop(t *testing.T) {
	clearOTLPEndpoints(t)

	shutdown, err := telemetry.Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	if telemetry.Enabled() {
		t.Error("Enabled() = true, want false without an OTLP endpoint")
	}

	if _, ok := otel.GetTracerProvider().(noop.TracerProvider); !ok {
		t.Errorf("tracer provider = %T, want noop.TracerProvider", otel.GetTracerProvider())
	}

	if fields := otel.GetTextMapPropagator().Fields(); !slices.Contains(fields, "traceparent") {
		t.Errorf("propagator fields = %v, want to contain %q", fields, "traceparent")
	}

	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown() error = %v", err)
	}
}

func TestSetupWithEndpointInstallsSDKProvider(t *testing.T) {
	clearOTLPEndpoints(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")

	shutdown, err := telemetry.Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// No span is recorded in this test, so shutting down must not try to
		// reach the (absent) collector.
		if err := shutdown(ctx); err != nil {
			t.Errorf("shutdown() error = %v", err)
		}

		otel.SetTracerProvider(noop.NewTracerProvider())
	})

	if !telemetry.Enabled() {
		t.Error("Enabled() = false, want true with an OTLP endpoint")
	}

	if _, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); !ok {
		t.Errorf("tracer provider = %T, want *sdktrace.TracerProvider", otel.GetTracerProvider())
	}
}

func TestServiceName(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")

	if got := telemetry.ServiceName(); got != telemetry.DefaultServiceName {
		t.Errorf("ServiceName() = %q, want %q", got, telemetry.DefaultServiceName)
	}

	t.Setenv("OTEL_SERVICE_NAME", "custom-service")

	if got := telemetry.ServiceName(); got != "custom-service" {
		t.Errorf("ServiceName() = %q, want %q", got, "custom-service")
	}
}

func clearOTLPEndpoints(t *testing.T) {
	t.Helper()

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
}
