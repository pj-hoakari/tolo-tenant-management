package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"testing"

	connectrpc "connectrpc.com/connect"

	relationv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/relation/v1"
	infradb "github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/logging"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/application"
	relationdomain "github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	relationrepository "github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
	tenantrepository "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

func TestRoleConversions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role relationv1.Role
		want relationdomain.Role
	}{
		{name: "unspecified", role: relationv1.Role_ROLE_UNSPECIFIED, want: relationdomain.RoleUnspecified},
		{name: "owner", role: relationv1.Role_ROLE_OWNER, want: relationdomain.RoleOwner},
		{name: "staff", role: relationv1.Role_ROLE_STAFF, want: relationdomain.RoleStaff},
		// The reserved role crosses the wire as itself, so that the domain is
		// the one to refuse it (ErrRoleReserved) rather than the transport
		// silently degrading it to unspecified.
		{name: "admin", role: relationv1.Role_ROLE_ADMIN, want: relationdomain.RoleAdmin},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := roleDomain(tt.role); got != tt.want {
				t.Errorf("roleDomain(%v) = %v, want %v", tt.role, got, tt.want)
			}

			if got := roleProto(tt.want); got != tt.role {
				t.Errorf("roleProto(%v) = %v, want %v", tt.want, got, tt.role)
			}
		})
	}

	t.Run("unknown values fall back to unspecified", func(t *testing.T) {
		t.Parallel()

		// A wire value this build does not know (a newer client) and a domain
		// value the proto does not name are both answered with unspecified
		// rather than mapped onto a role that grants something.
		if got := roleDomain(relationv1.Role(math.MaxInt32)); got != relationdomain.RoleUnspecified {
			t.Errorf("roleDomain(unknown) = %v, want %v", got, relationdomain.RoleUnspecified)
		}

		if got := roleProto(relationdomain.Role(math.MaxUint8)); got != relationv1.Role_ROLE_UNSPECIFIED {
			t.Errorf("roleProto(unknown) = %v, want %v", got, relationv1.Role_ROLE_UNSPECIFIED)
		}
	})
}

// TestMembershipProto pins the wire shape of a membership: public IDs only,
// the tenant role, and every event role in the order the domain holds them.
func TestMembershipProto(t *testing.T) {
	t.Parallel()

	membership := relationdomain.NewMembership("user-1", "00000000-0000-0000-0000-0000000000a1", "aaaaaaaaaaaaaaa1", relationdomain.RoleStaff, []relationdomain.EventRole{
		relationdomain.NewEventRole("00000000-0000-0000-0000-0000000000a2", "aaaaaaaaaaaaaaa2", relationdomain.RoleStaff),
		relationdomain.NewEventRole("00000000-0000-0000-0000-0000000000a3", "aaaaaaaaaaaaaaa3", relationdomain.RoleOwner),
	})

	got := membershipProto(membership)

	if got.GetUserId() != "user-1" || got.GetTenantId() != "aaaaaaaaaaaaaaa1" || got.GetTenantRole() != relationv1.Role_ROLE_STAFF {
		t.Errorf("membershipProto() = %v, want user-1 as staff of aaaaaaaaaaaaaaa1", got)
	}

	wantRoles := []*relationv1.EventRole{
		{EventId: "aaaaaaaaaaaaaaa2", Role: relationv1.Role_ROLE_STAFF},
		{EventId: "aaaaaaaaaaaaaaa3", Role: relationv1.Role_ROLE_OWNER},
	}

	if roles := got.GetEventRoles(); len(roles) != len(wantRoles) {
		t.Fatalf("EventRoles = %v, want %v", roles, wantRoles)
	}

	for i, want := range wantRoles {
		if role := got.GetEventRoles()[i]; role.GetEventId() != want.GetEventId() || role.GetRole() != want.GetRole() {
			t.Errorf("EventRoles[%d] = %v, want %v", i, role, want)
		}
	}

	// The internal IDs of the tenant and the events never leave the service.
	if text := got.String(); strings.Contains(text, "00000000-0000-0000-0000-") {
		t.Errorf("membershipProto() = %s, want no internal ID on the wire", text)
	}

	t.Run("without event roles", func(t *testing.T) {
		t.Parallel()

		got := membershipProto(relationdomain.NewMembership("user-2", "00000000-0000-0000-0000-0000000000b1", "bbbbbbbbbbbbbbb1", relationdomain.RoleOwner, nil))
		if roles := got.GetEventRoles(); len(roles) != 0 {
			t.Errorf("EventRoles = %v, want none", roles)
		}
	})
}

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
