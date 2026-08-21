// Package application contains use cases for the tenant context.
package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

var (
	ErrTenantNameRequired         = errors.New("tenant name is required")
	ErrTenantContractPlanRequired = errors.New("tenant contract plan is required")
	ErrTenantIDRequired           = errors.New("tenant ID is required")
	ErrEventNameRequired          = errors.New("event name is required")
	ErrEventIDRequired            = errors.New("event ID is required")
	ErrEventTypeRequired          = errors.New("event type is required")
	ErrEventStatusRequired        = errors.New("event status is required")
	// ErrTenantPendingOwner rejects business operations on a tenant whose
	// owner has not claimed it yet.
	ErrTenantPendingOwner = errors.New("tenant is pending an owner")
	// ErrOwnershipClaimTokenRequired rejects a claim without a token.
	ErrOwnershipClaimTokenRequired = errors.New("ownership claim token is required")
	// ErrOwnershipClaimRejected covers every way a claim can fail to match:
	// the tenant is not pending, the token is wrong, expired, or already used.
	// The reasons are deliberately not distinguished.
	ErrOwnershipClaimRejected = errors.New("ownership claim rejected")
)

// DefaultOwnershipClaimTTL is how long a pending_owner tenant waits for
// ClaimTenantOwnership before it expires and its name is released.
const DefaultOwnershipClaimTTL = 24 * time.Hour

// StartTenantRegistrationInput contains the values accepted by the
// StartTenantRegistration use case.
type StartTenantRegistrationInput struct {
	Name         string
	ContractPlan string
}

// TenantRegistration is the outcome of StartTenantRegistration. ClaimToken is
// the plaintext one-time token; it is returned here and nowhere else.
type TenantRegistration struct {
	Tenant     domain.Tenant
	ClaimToken string
	ExpiresAt  time.Time
}

// CreateEventInput contains the values accepted by the CreateEvent use case.
// TenantPublicID is the target tenant taken from the request; it is
// cross-checked against the authenticated tenant carried in the context.
type CreateEventInput struct {
	TenantPublicID string
	Name           string
	Type           domain.EventType
}

// TransitionEventStatusInput contains the requested event status change.
type TransitionEventStatusInput struct {
	EventPublicID string
	To            domain.EventStatus
}

// AssignEventTypeInput contains the requested event type assignment.
type AssignEventTypeInput struct {
	EventPublicID string
	Type          domain.EventType
}

// StartTenantRegistrationUseCase creates a pending_owner tenant without
// authentication.
type StartTenantRegistrationUseCase interface {
	StartTenantRegistration(context.Context, StartTenantRegistrationInput) (TenantRegistration, error)
}

// ClaimTenantOwnershipInput contains the values accepted by the
// ClaimTenantOwnership use case. The claiming user is the authenticated
// subject carried in the context.
type ClaimTenantOwnershipInput struct {
	TenantPublicID string
	ClaimToken     string
}

// ClaimTenantOwnershipUseCase turns a pending_owner tenant into an owned one.
type ClaimTenantOwnershipUseCase interface {
	ClaimTenantOwnership(context.Context, ClaimTenantOwnershipInput) (domain.Tenant, error)
}

// CreateEventUseCase creates an event for a tenant.
type CreateEventUseCase interface {
	CreateEvent(context.Context, CreateEventInput) (domain.Event, error)
}

// TransitionEventStatusUseCase changes an event's lifecycle status.
type TransitionEventStatusUseCase interface {
	TransitionEventStatus(context.Context, TransitionEventStatusInput) (domain.Event, error)
}

// AssignEventTypeUseCase changes an event's type.
type AssignEventTypeUseCase interface {
	AssignEventType(context.Context, AssignEventTypeInput) (domain.Event, error)
}

// GetEventUseCase retrieves one event by its public ID.
type GetEventUseCase interface {
	GetEvent(context.Context, string) (domain.Event, error)
}

// ListEventsUseCase lists events belonging to the requested tenant.
type ListEventsUseCase interface {
	ListEvents(context.Context, string) ([]domain.Event, error)
}

// TenantUseCases groups the tenant operations exposed by the Connect
// transport.
type TenantUseCases interface {
	StartTenantRegistrationUseCase
	ClaimTenantOwnershipUseCase
	CreateEventUseCase
	AssignEventTypeUseCase
	TransitionEventStatusUseCase
	GetEventUseCase
	ListEventsUseCase
}

// TenantService implements tenant use cases.
type TenantService struct {
	tenantRepository  repository.TenantRepository
	transactor        repository.Transactor
	memberships       MembershipWriter
	now               func() time.Time
	ownershipClaimTTL time.Duration
}

// Option configures a TenantService.
type Option func(*TenantService)

// WithClock replaces the wall clock, so tests can move time.
func WithClock(now func() time.Time) Option {
	return func(s *TenantService) { s.now = now }
}

// WithOwnershipClaimTTL sets how long a pending_owner tenant can be claimed.
func WithOwnershipClaimTTL(ttl time.Duration) Option {
	return func(s *TenantService) { s.ownershipClaimTTL = ttl }
}

func NewTenantService(tenantRepository repository.TenantRepository, transactor repository.Transactor, memberships MembershipWriter, options ...Option) *TenantService {
	service := &TenantService{
		tenantRepository:  tenantRepository,
		transactor:        transactor,
		memberships:       memberships,
		now:               time.Now,
		ownershipClaimTTL: DefaultOwnershipClaimTTL,
	}

	for _, option := range options {
		option(service)
	}

	return service
}

// StartTenantRegistration creates a pending_owner tenant and hands out the
// one-time claim token. Expired pending tenants are swept first, so a name
// held by an abandoned registration becomes available again.
func (s *TenantService) StartTenantRegistration(ctx context.Context, input StartTenantRegistrationInput) (TenantRegistration, error) {
	if input.Name == "" {
		return TenantRegistration{}, ErrTenantNameRequired
	}

	if input.ContractPlan == "" {
		return TenantRegistration{}, ErrTenantContractPlanRequired
	}

	now := s.now()

	if _, err := s.tenantRepository.DeleteExpiredPendingTenants(ctx, now); err != nil {
		return TenantRegistration{}, fmt.Errorf("sweep expired pending tenants: %w", err)
	}

	tenantID, err := newUUIDv7()
	if err != nil {
		return TenantRegistration{}, err
	}

	publicID, err := newPublicID()
	if err != nil {
		return TenantRegistration{}, err
	}

	claimToken, tokenHash, err := domain.NewOwnershipClaimToken()
	if err != nil {
		return TenantRegistration{}, err
	}

	tenant := domain.NewTenant(tenantID, publicID, input.Name, input.ContractPlan, domain.TenantOwnershipStatePendingOwner, false)
	claim := domain.OwnershipClaim{TokenHash: tokenHash, ExpiresAt: now.Add(s.ownershipClaimTTL)}

	if err := s.tenantRepository.CreatePendingTenant(ctx, tenant, claim); err != nil {
		return TenantRegistration{}, err
	}

	return TenantRegistration{Tenant: tenant, ClaimToken: claimToken, ExpiresAt: claim.ExpiresAt}, nil
}

// ClaimTenantOwnership verifies the one-time claim token and, in one
// transaction, records the authenticated subject as owner, moves the tenant to
// owned, and consumes the token. TenantRegistered is established by the commit
// of this transaction.
func (s *TenantService) ClaimTenantOwnership(ctx context.Context, input ClaimTenantOwnershipInput) (domain.Tenant, error) {
	if input.TenantPublicID == "" {
		return domain.Tenant{}, ErrTenantIDRequired
	}

	if input.ClaimToken == "" {
		return domain.Tenant{}, ErrOwnershipClaimTokenRequired
	}

	subject, ok := tenantctx.SubjectFromContext(ctx)
	if !ok {
		return domain.Tenant{}, tenantctx.ErrSubjectMissing
	}

	var owned domain.Tenant

	err := s.transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		tenant, claim, err := s.tenantRepository.FindTenantByPublicIDForUpdate(ctx, input.TenantPublicID)
		if err != nil {
			return err
		}

		if tenant.OwnershipState() != domain.TenantOwnershipStatePendingOwner || claim.Expired(s.now()) || !claim.TokenHash.Matches(input.ClaimToken) {
			return ErrOwnershipClaimRejected
		}

		owned, err = tenant.ClaimOwnership()
		if err != nil {
			return err
		}

		if err := s.memberships.AddOwner(ctx, tenant.ID(), subject); err != nil {
			return fmt.Errorf("add owner membership: %w", err)
		}

		return s.tenantRepository.MarkTenantOwned(ctx, owned)
	})
	if err != nil {
		return domain.Tenant{}, err
	}

	return owned, nil
}

// resolveTenant loads the tenant named in the request after verifying that it
// is the tenant the caller is authenticated for. The request carries the
// target explicitly; the claim is only used to confirm it. A pending_owner
// tenant accepts no business operation until its owner has claimed it.
func (s *TenantService) resolveTenant(ctx context.Context, tenantPublicID string) (domain.Tenant, error) {
	if tenantPublicID == "" {
		return domain.Tenant{}, ErrTenantIDRequired
	}

	if err := tenantctx.Ensure(ctx, tenantPublicID); err != nil {
		return domain.Tenant{}, err
	}

	tenant, err := s.tenantRepository.FindTenantByPublicID(ctx, tenantPublicID)
	if err != nil {
		return domain.Tenant{}, err
	}

	if !tenant.Owned() {
		return domain.Tenant{}, ErrTenantPendingOwner
	}

	return tenant, nil
}

// resolveEvent loads the event named in the request and verifies that it
// belongs to the tenant the caller is authenticated for. The tenant is only
// known once the event is loaded, so the check happens here.
func (s *TenantService) resolveEvent(ctx context.Context, eventPublicID string) (domain.Event, error) {
	event, err := s.tenantRepository.FindEventByPublicID(ctx, eventPublicID)
	if err != nil {
		return domain.Event{}, err
	}

	if err := tenantctx.Ensure(ctx, event.TenantPublicID()); err != nil {
		return domain.Event{}, err
	}

	return event, nil
}

func (s *TenantService) CreateEvent(ctx context.Context, input CreateEventInput) (domain.Event, error) {
	if input.Name == "" {
		return domain.Event{}, ErrEventNameRequired
	}

	tenant, err := s.resolveTenant(ctx, input.TenantPublicID)
	if err != nil {
		return domain.Event{}, err
	}

	if tenant.Archived() {
		return domain.Event{}, repository.ErrTenantArchived
	}

	eventID, err := newUUIDv7()
	if err != nil {
		return domain.Event{}, err
	}

	publicID, err := newPublicID()
	if err != nil {
		return domain.Event{}, err
	}

	event := domain.NewEvent(eventID, publicID, tenant.ID(), tenant.PublicID(), input.Name, input.Type, domain.EventStatusDraft)
	if err := s.tenantRepository.CreateEvent(ctx, event); err != nil {
		return domain.Event{}, err
	}

	return event, nil
}

func (s *TenantService) TransitionEventStatus(ctx context.Context, input TransitionEventStatusInput) (domain.Event, error) {
	if input.EventPublicID == "" {
		return domain.Event{}, ErrEventIDRequired
	}

	if input.To == domain.EventStatusUnspecified {
		return domain.Event{}, ErrEventStatusRequired
	}

	event, err := s.resolveEvent(ctx, input.EventPublicID)
	if err != nil {
		return domain.Event{}, err
	}

	updatedEvent, err := event.TransitionTo(input.To)
	if err != nil {
		return domain.Event{}, err
	}

	if err := s.tenantRepository.UpdateEvent(ctx, updatedEvent); err != nil {
		return domain.Event{}, err
	}

	return updatedEvent, nil
}

func (s *TenantService) AssignEventType(ctx context.Context, input AssignEventTypeInput) (domain.Event, error) {
	if input.EventPublicID == "" {
		return domain.Event{}, ErrEventIDRequired
	}

	if input.Type == domain.EventTypeUnspecified {
		return domain.Event{}, ErrEventTypeRequired
	}

	event, err := s.resolveEvent(ctx, input.EventPublicID)
	if err != nil {
		return domain.Event{}, err
	}

	if event.Status() == domain.EventStatusArchived {
		return domain.Event{}, repository.ErrEventArchived
	}

	updatedEvent := event.AssignType(input.Type)
	if err := s.tenantRepository.UpdateEvent(ctx, updatedEvent); err != nil {
		return domain.Event{}, err
	}

	return updatedEvent, nil
}

// GetEvent serves the service-to-service referential-integrity read. It
// enforces the tenant boundary: the caller's tenant context (from the service
// token's tenant_id claim) must match the event's tenant.
func (s *TenantService) GetEvent(ctx context.Context, eventPublicID string) (domain.Event, error) {
	if eventPublicID == "" {
		return domain.Event{}, ErrEventIDRequired
	}

	return s.resolveEvent(ctx, eventPublicID)
}

func (s *TenantService) ListEvents(ctx context.Context, tenantPublicID string) ([]domain.Event, error) {
	tenant, err := s.resolveTenant(ctx, tenantPublicID)
	if err != nil {
		return nil, err
	}

	return s.tenantRepository.ListEventsByTenantID(ctx, tenant.ID())
}
