package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	connectrpc "connectrpc.com/connect"

	infradb "github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/logging"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/application"
	relationdomain "github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	relationrepository "github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
	tenantrepository "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

// TestConnectError pins the code every sentinel error is answered with,
// including the ones the transport cannot reach from a test.
func TestConnectError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want connectrpc.Code
	}{
		{err: application.ErrTenantIDRequired, want: connectrpc.CodeInvalidArgument},
		{err: relationdomain.ErrRoleReserved, want: connectrpc.CodeInvalidArgument},
		{err: relationrepository.ErrMembershipNotFound, want: connectrpc.CodeNotFound},
		{err: relationrepository.ErrMembershipAlreadyExists, want: connectrpc.CodeFailedPrecondition},
		{err: tenantrepository.ErrTenantArchived, want: connectrpc.CodeFailedPrecondition},
		{err: application.ErrPermissionDenied, want: connectrpc.CodePermissionDenied},
		{err: tenantctx.ErrMismatch, want: connectrpc.CodePermissionDenied},
		{err: tenantctx.ErrSubjectMissing, want: connectrpc.CodeUnauthenticated},
		{err: tenantctx.ErrMissing, want: connectrpc.CodeUnauthenticated},
		{err: fmt.Errorf("revoke: %w", infradb.ErrTransactionAborted), want: connectrpc.CodeAborted},
		{err: errors.New("something else"), want: connectrpc.CodeInternal},
	}

	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			t.Parallel()

			if got := connectrpc.CodeOf(connectError(context.Background(), tt.err)); got != tt.want {
				t.Errorf("connectError(%v) code = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestConnectErrorHidesInternalDetail keeps the cause of an internal failure
// out of the response and puts it in the server log instead, as the tenant
// transport does (service_gateway.md「エラー方針」). It cannot run in parallel:
// it reads the log back through the process-wide default logger.
func TestConnectErrorHidesInternalDetail(t *testing.T) {
	logs := captureLog(t)

	err := connectError(context.Background(), errors.New("secret detail"))

	var connectErr *connectrpc.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("connectError() = %v, want a Connect error", err)
	}

	if got, want := connectErr.Message(), "internal error"; got != want {
		t.Errorf("Message() = %q, want %q", got, want)
	}

	if strings.Contains(err.Error(), "secret detail") {
		t.Errorf("error = %q, want it to omit the underlying failure", err)
	}

	entry := decodeLogEntry(t, logs)

	if got, want := entry["severity"], "ERROR"; got != want {
		t.Errorf("severity = %v, want %q", got, want)
	}

	if got, want := entry["message"], "internal error"; got != want {
		t.Errorf("message = %v, want %q", got, want)
	}

	if got, want := entry["error"], "secret detail"; got != want {
		t.Errorf("error = %v, want %q", got, want)
	}
}

// TestConnectErrorReportsCanceledWithoutLogging pins a client that goes away to
// canceled: it is not a server fault, so nothing is logged. It shares the
// process-wide logger with TestConnectErrorHidesInternalDetail and so cannot
// run in parallel either.
func TestConnectErrorReportsCanceledWithoutLogging(t *testing.T) {
	logs := captureLog(t)

	err := connectError(context.Background(), context.Canceled)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeCanceled; got != want {
		t.Errorf("connectError(context.Canceled) code = %v, want %v", got, want)
	}

	if logs.Len() != 0 {
		t.Errorf("log = %q, want nothing logged", logs.String())
	}
}

// captureLog installs the service's own handler over a buffer as the default
// logger for the duration of the test, so the assertions read the records in
// the shape production writes them. The default logger is process-wide, so a
// test that calls this one must not call t.Parallel().
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	var logs bytes.Buffer

	previous := slog.Default()

	slog.SetDefault(logging.NewLogger(&logs, logging.Options{Level: slog.LevelDebug, AddSource: false, ProjectID: ""}))
	t.Cleanup(func() { slog.SetDefault(previous) })

	return &logs
}

// decodeLogEntry parses the single JSON record the captured log holds.
func decodeLogEntry(t *testing.T, logs *bytes.Buffer) map[string]any {
	t.Helper()

	line := strings.TrimSpace(logs.String())
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
