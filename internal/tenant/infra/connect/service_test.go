//go:generate go tool mockgen -source=../../repository/tenant.go -destination=mock_tenant_repository_test.go -package=connect

package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	connectrpc "connectrpc.com/connect"
	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/internal/logging"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

// passthroughTransactor runs the unit of work directly for mock-backed tests.
type passthroughTransactor struct{}

func (passthroughTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

// membershipRecorder stands in for the relation model's membership store.
type membershipRecorder struct {
	mu       sync.Mutex
	err      error
	tenantID string
	userID   string
	calls    int
}

func (r *membershipRecorder) AddOwner(_ context.Context, tenantID, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls++
	r.tenantID, r.userID = tenantID, userID

	return r.err
}

func (r *membershipRecorder) owner() (string, string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.tenantID, r.userID, r.calls
}

// permissionStub stands in for the relation side's current-permission check.
// It holds no mutable state, so the handler goroutines can share one.
type permissionStub struct {
	err     error
	allowed bool
}

func (s permissionStub) Allowed(context.Context, string, string) (bool, error) {
	return s.allowed, s.err
}

func TestEventStatusConversions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status tenantv1.EventStatus
		want   domain.EventStatus
	}{
		{name: "unspecified", status: tenantv1.EventStatus_EVENT_STATUS_UNSPECIFIED, want: domain.EventStatusUnspecified},
		{name: "draft", status: tenantv1.EventStatus_EVENT_STATUS_DRAFT, want: domain.EventStatusDraft},
		{name: "open", status: tenantv1.EventStatus_EVENT_STATUS_OPEN, want: domain.EventStatusOpen},
		{name: "locked", status: tenantv1.EventStatus_EVENT_STATUS_LOCKED, want: domain.EventStatusLocked},
		{name: "closed", status: tenantv1.EventStatus_EVENT_STATUS_CLOSED, want: domain.EventStatusClosed},
		{name: "archived", status: tenantv1.EventStatus_EVENT_STATUS_ARCHIVED, want: domain.EventStatusArchived},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eventStatusDomain(tt.status); got != tt.want {
				t.Errorf("eventStatusDomain(%v) = %v, want %v", tt.status, got, tt.want)
			}

			if got := eventStatusProto(tt.want); got != tt.status {
				t.Errorf("eventStatusProto(%q) = %v, want %v", tt.want, got, tt.status)
			}
		})
	}
}

func TestEventProto(t *testing.T) {
	t.Parallel()

	event := domain.NewEvent(
		"event-id",
		"event-public-id",
		"tenant-id",
		"tenant-public-id",
		"Festival",
		domain.EventTypeShortTerm,
		domain.EventStatusDraft,
	)

	openEvent, err := event.TransitionTo(domain.EventStatusOpen)
	if err != nil {
		t.Fatalf("TransitionTo() error = %v", err)
	}

	got := eventProto(openEvent)

	// The wire representation carries public IDs only.
	if got, want := got.GetEventId(), openEvent.PublicID(); got != want {
		t.Errorf("EventId = %q, want %q", got, want)
	}

	if got, want := got.GetTenantId(), openEvent.TenantPublicID(); got != want {
		t.Errorf("TenantId = %q, want %q", got, want)
	}

	if got, want := got.GetStatus(), tenantv1.EventStatus_EVENT_STATUS_OPEN; got != want {
		t.Errorf("Status = %v, want %v", got, want)
	}
}

func TestGetEvent(t *testing.T) {
	t.Parallel()

	service, _, events := newReadService(t)

	ctx := tenantctx.WithTenantPublicID(context.Background(), events[0].TenantPublicID())

	response, err := service.GetEvent(ctx, connectrpc.NewRequest(&tenantv1.GetEventRequest{EventId: events[0].PublicID()}))
	if err != nil {
		t.Fatalf("GetEvent() error = %v", err)
	}

	if got, want := response.Msg.GetEvent().GetEventId(), events[0].PublicID(); got != want {
		t.Errorf("EventId = %q, want %q", got, want)
	}
}

func TestAssignEventType(t *testing.T) {
	t.Parallel()

	service, _, events := newReadService(t)

	ctx := tenantctx.WithTenantPublicID(context.Background(), events[0].TenantPublicID())

	response, err := service.AssignEventType(ctx, connectrpc.NewRequest(&tenantv1.AssignEventTypeRequest{
		EventId: events[0].PublicID(),
		Type:    tenantv1.EventType_EVENT_TYPE_LONG_TERM,
	}))
	if err != nil {
		t.Fatalf("AssignEventType() error = %v", err)
	}

	if got, want := response.Msg.GetEvent().GetType(), tenantv1.EventType_EVENT_TYPE_LONG_TERM; got != want {
		t.Errorf("Event.Type = %v, want %v", got, want)
	}
}

func TestListEvents(t *testing.T) {
	t.Parallel()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)
	events := []domain.Event{
		domain.NewEvent("event-1", "event-public-id-1", tenant.ID(), tenant.PublicID(), "Festival 1", domain.EventTypeShortTerm, domain.EventStatusDraft),
		domain.NewEvent("event-2", "event-public-id-2", tenant.ID(), tenant.PublicID(), "Festival 2", domain.EventTypeLongTerm, domain.EventStatusDraft),
	}

	for _, includeArchived := range []bool{false, true} {
		t.Run(fmt.Sprintf("include archived = %t", includeArchived), func(t *testing.T) {
			t.Parallel()

			// include_archived comes straight off the request, and the listing
			// carries the cap the spec sets.
			wantFilter := repository.ListEventsFilter{IncludeArchived: includeArchived, Limit: repository.MaxListEvents}

			ctrl := gomock.NewController(t)
			repo := NewMockTenantRepository(ctrl)
			repo.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(tenant, nil)
			repo.EXPECT().ListEventsByTenantID(gomock.Any(), tenant.ID(), wantFilter).Return(events, nil)
			service := NewService(application.NewTenantService(repo, passthroughTransactor{}, &membershipRecorder{}, permissionStub{allowed: true}))

			ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

			response, err := service.ListEvents(ctx, connectrpc.NewRequest(&tenantv1.ListEventsRequest{
				TenantId:        tenant.PublicID(),
				IncludeArchived: includeArchived,
			}))
			if err != nil {
				t.Fatalf("ListEvents() error = %v", err)
			}

			if got, want := len(response.Msg.GetEvents()), len(events); got != want {
				t.Fatalf("event count = %d, want %d", got, want)
			}

			for i, event := range events {
				if got, want := response.Msg.GetEvents()[i].GetEventId(), event.PublicID(); got != want {
					t.Errorf("Events[%d].EventId = %q, want %q", i, got, want)
				}
			}
		})
	}
}

// TestCreateEventPublicIDCollision pins an event public ID collision to
// already_exists rather than internal (tenant_management_spec.md「エラー」).
func TestCreateEventPublicIDCollision(t *testing.T) {
	t.Parallel()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)

	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(tenant, nil).AnyTimes()
	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).Return(repository.ErrEventPublicIDExists)

	service := NewService(application.NewTenantService(repo, passthroughTransactor{}, &membershipRecorder{}, permissionStub{allowed: true}))
	ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

	_, err := service.CreateEvent(ctx, connectrpc.NewRequest(&tenantv1.CreateEventRequest{
		TenantId: tenant.PublicID(),
		Name:     "Festival",
		Type:     tenantv1.EventType_EVENT_TYPE_SHORT_TERM,
	}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeAlreadyExists; got != want {
		t.Fatalf("CreateEvent() error code = %v, want %v", got, want)
	}
}

// TestInternalErrorHidesDetail keeps the cause of an internal failure out of
// the response and puts it in the server log instead
// (service_gateway.md「エラー方針」). It cannot run in parallel: it reads the
// log back through the process-wide default logger.
func TestInternalErrorHidesDetail(t *testing.T) {
	logs := captureLog(t)
	service, tenant := newFailingService(t, errors.New("secret detail"))

	ctx := withSampledSpan(t, tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID()))

	_, err := service.ListEvents(ctx, connectrpc.NewRequest(&tenantv1.ListEventsRequest{TenantId: tenant.PublicID()}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeInternal; got != want {
		t.Fatalf("ListEvents() error code = %v, want %v", got, want)
	}

	var connectErr *connectrpc.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("ListEvents() error = %v, want a Connect error", err)
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

	// The trace fields replace the trace_id the log line used to carry: the
	// handler reads them off the request context.
	for _, key := range []string{"logging.googleapis.com/trace", "logging.googleapis.com/spanId"} {
		if _, ok := entry[key]; !ok {
			t.Errorf("log entry = %v, want it to name %q", entry, key)
		}
	}
}

// TestCanceledRequestIsNotLogged pins a client that goes away to canceled: it
// is not a server fault, so nothing is logged. It shares the process-wide
// logger with TestInternalErrorHidesDetail and so cannot run in parallel
// either.
func TestCanceledRequestIsNotLogged(t *testing.T) {
	logs := captureLog(t)
	service, tenant := newFailingService(t, context.Canceled)

	ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

	_, err := service.ListEvents(ctx, connectrpc.NewRequest(&tenantv1.ListEventsRequest{TenantId: tenant.PublicID()}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeCanceled; got != want {
		t.Fatalf("ListEvents() error code = %v, want %v", got, want)
	}

	if logs.Len() != 0 {
		t.Errorf("log = %q, want nothing logged", logs.String())
	}
}

// newFailingService returns a service whose tenant lookup fails with err.
func newFailingService(t *testing.T, err error) (*Service, domain.Tenant) {
	t.Helper()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)

	repo := NewMockTenantRepository(gomock.NewController(t))
	repo.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(domain.Tenant{}, err)

	return NewService(application.NewTenantService(repo, passthroughTransactor{}, &membershipRecorder{}, permissionStub{allowed: true})), tenant
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

// withSampledSpan returns ctx carrying a valid, sampled span context, as the
// tracing interceptor leaves on a request it has handled.
func withSampledSpan(t *testing.T, ctx context.Context) context.Context {
	t.Helper()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("TraceIDFromHex() error = %v", err)
	}

	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("SpanIDFromHex() error = %v", err)
	}

	return trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))
}

func newReadService(t *testing.T) (*Service, domain.Tenant, []domain.Event) {
	t.Helper()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)
	events := []domain.Event{
		domain.NewEvent("event-1", "event-public-id-1", tenant.ID(), tenant.PublicID(), "Festival 1", domain.EventTypeShortTerm, domain.EventStatusDraft),
		domain.NewEvent("event-2", "event-public-id-2", tenant.ID(), tenant.PublicID(), "Festival 2", domain.EventTypeLongTerm, domain.EventStatusDraft),
	}

	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindEventByPublicID(gomock.Any(), events[0].PublicID()).Return(events[0], nil).AnyTimes()
	repo.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(tenant, nil).AnyTimes()
	repo.EXPECT().ListEventsByTenantID(gomock.Any(), tenant.ID(), gomock.Any()).Return(events, nil).AnyTimes()
	repo.EXPECT().UpdateEvent(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	return NewService(application.NewTenantService(repo, passthroughTransactor{}, &membershipRecorder{}, permissionStub{allowed: true})), tenant, events
}

// TestEveryHandlerHidesInternalErrorDetail drives each authenticated handler
// into its internal-error fallback with a repository that fails on every
// lookup, and checks that none of them leaks the cause. It shares the
// process-wide logger and so must not run in parallel.
func TestEveryHandlerHidesInternalErrorDetail(t *testing.T) {
	logs := captureLog(t)
	errDetail := errors.New("secret detail")
	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)

	repo := NewMockTenantRepository(gomock.NewController(t))
	repo.EXPECT().FindTenantByPublicID(gomock.Any(), gomock.Any()).Return(domain.Tenant{}, errDetail).AnyTimes()
	repo.EXPECT().FindTenantByPublicIDForUpdate(gomock.Any(), gomock.Any()).Return(domain.Tenant{}, domain.OwnershipClaim{}, errDetail).AnyTimes()
	repo.EXPECT().FindEventByPublicID(gomock.Any(), gomock.Any()).Return(domain.Event{}, errDetail).AnyTimes()
	repo.EXPECT().FindObservationSettingsByEventPublicID(gomock.Any(), gomock.Any()).Return(domain.ObservationSettings{}, errDetail).AnyTimes()
	repo.EXPECT().DeleteExpiredPendingTenants(gomock.Any(), gomock.Any()).Return(int64(0), errDetail).AnyTimes()
	service := NewService(application.NewTenantService(repo, passthroughTransactor{}, &membershipRecorder{}, permissionStub{allowed: true}))

	ctx := tenantctx.WithSubject(tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID()), "user-1")
	eventID := "0123456789abcdef"

	calls := map[string]func() error{
		"StartTenantRegistration": func() error {
			_, err := service.StartTenantRegistration(ctx, connectrpc.NewRequest(&tenantv1.StartTenantRegistrationRequest{Name: "Acme", ContractPlan: "standard"}))

			return err
		},
		"ClaimTenantOwnership": func() error {
			_, err := service.ClaimTenantOwnership(ctx, connectrpc.NewRequest(&tenantv1.ClaimTenantOwnershipRequest{TenantId: tenant.PublicID(), OwnershipClaimToken: "token"}))

			return err
		},
		"ChangeTenantContract": func() error {
			_, err := service.ChangeTenantContract(ctx, connectrpc.NewRequest(&tenantv1.ChangeTenantContractRequest{TenantId: tenant.PublicID(), ContractPlan: "enterprise"}))

			return err
		},
		"ArchiveTenant": func() error {
			_, err := service.ArchiveTenant(ctx, connectrpc.NewRequest(&tenantv1.ArchiveTenantRequest{TenantId: tenant.PublicID()}))

			return err
		},
		"CreateEvent": func() error {
			_, err := service.CreateEvent(ctx, connectrpc.NewRequest(&tenantv1.CreateEventRequest{TenantId: tenant.PublicID(), Name: "Festival"}))

			return err
		},
		"AssignEventType": func() error {
			_, err := service.AssignEventType(ctx, connectrpc.NewRequest(&tenantv1.AssignEventTypeRequest{EventId: eventID, Type: tenantv1.EventType_EVENT_TYPE_SHORT_TERM}))

			return err
		},
		"TransitionEventStatus": func() error {
			_, err := service.TransitionEventStatus(ctx, connectrpc.NewRequest(&tenantv1.TransitionEventStatusRequest{EventId: eventID, To: tenantv1.EventStatus_EVENT_STATUS_OPEN}))

			return err
		},
		"GetEvent": func() error {
			_, err := service.GetEvent(ctx, connectrpc.NewRequest(&tenantv1.GetEventRequest{EventId: eventID}))

			return err
		},
		"GetObservationSettings": func() error {
			_, err := service.GetObservationSettings(ctx, connectrpc.NewRequest(&tenantv1.GetObservationSettingsRequest{EventId: eventID}))

			return err
		},
		"UpdateObservationSettings": func() error {
			_, err := service.UpdateObservationSettings(ctx, connectrpc.NewRequest(&tenantv1.UpdateObservationSettingsRequest{EventId: eventID, Settings: &tenantv1.ObservationSettings{HistoryWindowDays: 10}}))

			return err
		},
		"ListEvents": func() error {
			_, err := service.ListEvents(ctx, connectrpc.NewRequest(&tenantv1.ListEventsRequest{TenantId: tenant.PublicID()}))

			return err
		},
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			logs.Reset()

			err := call()
			if got, want := connectrpc.CodeOf(err), connectrpc.CodeInternal; got != want {
				t.Fatalf("%s error code = %v, want %v", name, got, want)
			}

			if strings.Contains(err.Error(), "secret detail") {
				t.Errorf("%s error = %q, want it to omit the underlying failure", name, err)
			}

			if !strings.Contains(logs.String(), "secret detail") {
				t.Errorf("%s log = %q, want the cause to be logged", name, logs.String())
			}
		})
	}
}
