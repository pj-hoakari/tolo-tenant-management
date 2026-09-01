// Package connect_test drives the process the way the server runs it: both
// TenantService and RelationAdminService mounted on one handler, sharing one
// PostgreSQL pool and one verifier of the internal JWT. The tests here are the
// integration tests of every service served by the process; the packages of
// the services keep the unit tests of their own transport code.
//
// The package is an external test package because the service packages import
// this one, so an in-package test could not import them back.
package connect_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	internaljwt "github.com/pj-hoakari/internal-jwt-handling"
	"github.com/pj-hoakari/internal-jwt-handling/jwks"
	"github.com/pj-hoakari/internal-jwt-handling/jwtgen"
	"github.com/pj-hoakari/internal-jwt-handling/verifier"

	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/relation/v1/relationv1connect"
	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/infra/connect"
	infradb "github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
	relationapplication "github.com/pj-hoakari/tolo-tenant-management/internal/relation/application"
	relationdomain "github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	relationconnect "github.com/pj-hoakari/tolo-tenant-management/internal/relation/infra/connect"
	relationdb "github.com/pj-hoakari/tolo-tenant-management/internal/relation/infra/db"
	relationhttpapi "github.com/pj-hoakari/tolo-tenant-management/internal/relation/infra/httpapi"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/application"
	tenantdomain "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/domain"
	tenantconnect "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/infra/connect"
	tenantdb "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/infra/db"
)

const (
	internalJWTIssuer   = connect.DefaultInternalJWTIssuer
	internalJWTAudience = connect.DefaultInternalJWTAudience

	// callerSubject is the subject of every token jwtgen mints, and therefore
	// the caller whose membership the administrative writes re-check.
	callerSubject = jwtgen.DefaultSubject

	// allTenantScopes grants everything a tenant_access token can be granted, so
	// a test that is not about scopes is not refused for lacking one.
	allTenantScopes = "tenant.read tenant.write events.read events.write"

	// testRefreshCooldown lets a key a test registers after the first request be
	// picked up at once. jwks.Config reads a non-positive duration as "use the
	// default", so the shortest cooldown a test can ask for is one nanosecond.
	// These tests are about the transport, not about the cache; the jwks
	// package covers the cache itself.
	testRefreshCooldown = time.Nanosecond
)

// testDB is the pool of the PostgreSQL container TestMain starts. Every test
// resets it through newProcess, so the tests of this package do not run in
// parallel.
var testDB *sqlx.DB

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	container, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("tenant_management"),
		postgres.WithUsername("tenant_management"),
		postgres.WithPassword("tenant_management"),
		postgres.WithInitScripts(migrationPaths()...),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start PostgreSQL test container: %v\n", err)
		os.Exit(1)
	}

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err == nil {
		// Open is the same instrumented entry point the server uses.
		testDB, err = infradb.Open(ctx, databaseURL)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "connect to PostgreSQL test container: %v\n", err)

		_ = container.Terminate(context.Background())

		os.Exit(1)
	}

	code := m.Run()
	_ = testDB.Close()
	_ = container.Terminate(context.Background())

	os.Exit(code)
}

func migrationPaths() []string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("locate test source file")
	}

	paths, err := filepath.Glob(filepath.Join(filepath.Dir(filename), "..", "..", "..", "migrations", "*.up.sql"))
	if err != nil || len(paths) == 0 {
		panic("locate migration files")
	}

	sort.Strings(paths)

	return paths
}

// testInternalJWTs are the static tokens every process verifies: their keys are
// registered with each JWKS the tests serve. tenantAccess is bound to the
// tenant "0123456789abcdef"; service is machine-origin and carries no tenant.
type testInternalJWTs struct {
	tenantAccess string
	registration string
	service      string
	jwks         internaljwt.JWKS
}

var (
	testTokensOnce sync.Once
	testTokens     testInternalJWTs
	testTokensErr  error
)

func internalJWTs(t *testing.T) testInternalJWTs {
	t.Helper()
	testTokensOnce.Do(func() {
		configs := []jwtgen.Config{
			{Issuer: internalJWTIssuer, Audience: internalJWTAudience, TokenUse: internaljwt.TokenUseTenantAccess, TenantPublicID: "0123456789abcdef", Scope: allTenantScopes, KeyID: "tenant-key", TTL: time.Hour},
			{Issuer: internalJWTIssuer, Audience: internalJWTAudience, TokenUse: internaljwt.TokenUseRegistration, Scope: "tenant.claim", KeyID: "registration-key", TTL: time.Hour},
			{Issuer: internalJWTIssuer, Audience: internalJWTAudience, TokenUse: internaljwt.TokenUseService, KeyID: "service-key", TTL: time.Hour},
		}

		outputs := make([]jwtgen.Output, len(configs))
		for i, config := range configs {
			outputs[i], testTokensErr = jwtgen.Generate(config)
			if testTokensErr != nil {
				return
			}

			testTokens.jwks.Keys = append(testTokens.jwks.Keys, outputs[i].JWKS.Keys...)
		}

		testTokens.tenantAccess = "Bearer " + outputs[0].Token
		testTokens.registration = "Bearer " + outputs[1].Token
		testTokens.service = "Bearer " + outputs[2].Token
	})

	if testTokensErr != nil {
		t.Fatalf("generate test internal JWTs: %v", testTokensErr)
	}

	return testTokens
}

// jwksRegistry serves a mutable set of JWKs, so a test can mint a token for a
// tenant it created and have the process verify it. Every key it hands out has
// its own kid, because a JWKS naming one kid twice is refused as a whole.
type jwksRegistry struct {
	mu     sync.Mutex
	keys   []internaljwt.JWK
	minted int
}

func (r *jwksRegistry) add(keys ...internaljwt.JWK) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.keys = append(r.keys, keys...)
}

func (r *jwksRegistry) document() internaljwt.JWKS {
	r.mu.Lock()
	defer r.mu.Unlock()

	return internaljwt.JWKS{Keys: append([]internaljwt.JWK(nil), r.keys...)}
}

// nextKeyID names the signing key of the next token minted for the registry.
func (r *jwksRegistry) nextKeyID(prefix string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.minted++

	return fmt.Sprintf("%s-%d", prefix, r.minted)
}

// process is one instance of the server's handler, wired as cmd/server does it,
// together with the repositories behind it (to seed and inspect state) and a
// client of each service it serves.
type process struct {
	tenants        *tenantdb.PostgresTenantRepository
	memberships    *relationdb.PostgresMembershipRepository
	jwks           *jwksRegistry
	tenantClient   tenantv1connect.TenantServiceClient
	relationClient relationv1connect.RelationAdminServiceClient
	// baseURL and httpClient reach the same handler without a generated
	// client, for the plain HTTP API mounted next to the Connect services.
	baseURL    string
	httpClient *http.Client
}

// newProcess resets the database and serves both services on one handler. The
// wiring mirrors cmd/server: the membership repository is the tenant side's
// membership writer and, through the relation side's Authorizer, its
// current-permission checker, and the transactor of the relation use cases.
// options adjust the tenant use cases (the clock, the ownership claim TTL).
func newProcess(t *testing.T, options ...application.Option) *process {
	t.Helper()

	if _, err := testDB.Exec(`TRUNCATE events, tenants CASCADE`); err != nil {
		t.Fatalf("truncate test database: %v", err)
	}

	tenants := tenantdb.NewPostgresTenantRepository(testDB)
	memberships := relationdb.NewPostgresMembershipRepository(testDB)
	tenantService := application.NewTenantService(tenants, tenants, memberships, relationapplication.NewAuthorizer(memberships), options...)
	relationService := relationapplication.NewRelationService(tenants, memberships, memberships)

	registry := &jwksRegistry{}
	registry.add(internalJWTs(t).jwks.Keys...)

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(registry.document()); err != nil {
			t.Errorf("encode JWKS: %v", err)
		}
	}))
	t.Cleanup(jwksServer.Close)

	cache, err := jwks.New(jwks.Config{
		URL:             jwksServer.URL,
		RefreshCooldown: testRefreshCooldown,
		FailureCooldown: testRefreshCooldown,
	})
	if err != nil {
		t.Fatalf("jwks.New() error = %v", err)
	}

	tokenVerifier, err := verifier.New(internalJWTIssuer, internalJWTAudience, cache)
	if err != nil {
		t.Fatalf("verifier.New() error = %v", err)
	}

	handler, err := connect.NewHandlerWithVerifier(tokenVerifier, tenantconnect.Mount(tenantService), relationconnect.Mount(relationService), relationhttpapi.Mount(relationService))
	if err != nil {
		t.Fatalf("NewHandlerWithVerifier() error = %v", err)
	}

	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	return &process{
		tenants:        tenants,
		memberships:    memberships,
		jwks:           registry,
		tenantClient:   tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL),
		relationClient: relationv1connect.NewRelationAdminServiceClient(httpServer.Client(), httpServer.URL),
		baseURL:        httpServer.URL,
		httpClient:     httpServer.Client(),
	}
}

// mint issues an internal JWT with config, registers its signing key with the
// process's JWKS and returns the Authorization header value.
func (p *process) mint(t *testing.T, config jwtgen.Config) string {
	t.Helper()

	config.Issuer = internalJWTIssuer
	config.Audience = internalJWTAudience
	config.TTL = time.Hour

	output, err := jwtgen.Generate(config)
	if err != nil {
		t.Fatalf("generate %s token: %v", config.TokenUse, err)
	}

	p.jwks.add(output.JWKS.Keys...)

	return "Bearer " + output.Token
}

// mintTenantAccessToken issues a tenant_access internal JWT bound to the tenant
// and granted every scope, as the Service Gateway issues one to a caller
// authenticated for the tenant.
func (p *process) mintTenantAccessToken(t *testing.T, tenantPublicID string) string {
	t.Helper()

	return p.mintTenantAccessTokenWithScope(t, tenantPublicID, allTenantScopes)
}

// mintTenantAccessTokenWithScope issues a tenant_access internal JWT granting
// exactly scope, so a test can present a caller the procedure's required
// scopes are not all granted to.
func (p *process) mintTenantAccessTokenWithScope(t *testing.T, tenantPublicID, scope string) string {
	t.Helper()

	return p.mint(t, jwtgen.Config{
		TokenUse:       internaljwt.TokenUseTenantAccess,
		TenantPublicID: tenantPublicID,
		Scope:          scope,
		KeyID:          p.jwks.nextKeyID("tenant-key-" + tenantPublicID),
	})
}

// mintServiceToken issues a user-origin service internal JWT whose tenant_id
// claim is tenantPublicID, as the Service Gateway re-issues it for a call made
// on behalf of a user inside that tenant.
func (p *process) mintServiceToken(t *testing.T, tenantPublicID string) string {
	t.Helper()

	return p.mint(t, jwtgen.Config{
		TokenUse:       internaljwt.TokenUseService,
		TenantPublicID: tenantPublicID,
		Scope:          "events.read",
		OriginSub:      "user-" + tenantPublicID,
		KeyID:          p.jwks.nextKeyID("service-key-" + tenantPublicID),
	})
}

// mintRegistrationTokenWithScope issues a registration internal JWT granting
// exactly scope. A registration token carries no tenant context, so the
// procedure's required scopes are all the policy has left to check.
func (p *process) mintRegistrationTokenWithScope(t *testing.T, scope string) string {
	t.Helper()

	return p.mint(t, jwtgen.Config{
		TokenUse: internaljwt.TokenUseRegistration,
		Scope:    scope,
		KeyID:    p.jwks.nextKeyID("registration-key"),
	})
}

// authorized builds a request carrying token as its bearer credential; an
// empty token leaves the request unauthenticated.
func authorized[T any](token string, msg *T) *connectrpc.Request[T] {
	req := connectrpc.NewRequest(msg)
	if token != "" {
		req.Header().Set("Authorization", token)
	}

	return req
}

// createTenant stores an owned tenant directly, standing in for the onboarding
// flow where a test is not about it.
func (p *process) createTenant(t *testing.T, publicID, name string) tenantdomain.Tenant {
	t.Helper()

	tenant := tenantdomain.NewTenant(uuid.Must(uuid.NewV7()).String(), publicID, name, "standard", tenantdomain.TenantOwnershipStateOwned, false)
	if err := p.tenants.CreateTenant(context.Background(), tenant); err != nil {
		t.Fatalf("CreateTenant(%q) error = %v", name, err)
	}

	return tenant
}

// addTenantMember records userID's membership of the tenant (by internal ID)
// directly. The caller of the tests is callerSubject; giving it a membership is
// how a test makes the administrative writes find a current permission.
func (p *process) addTenantMember(t *testing.T, tenantID, userID string, role relationdomain.Role) {
	t.Helper()

	if _, err := p.memberships.AddTenantMember(context.Background(), tenantID, userID, role); err != nil {
		t.Fatalf("AddTenantMember(%q, %v) error = %v", userID, role, err)
	}
}

func (p *process) changeTenantRole(t *testing.T, tenantID, userID string, role relationdomain.Role) {
	t.Helper()

	if _, err := p.memberships.ChangeTenantRole(context.Background(), tenantID, userID, role); err != nil {
		t.Fatalf("ChangeTenantRole(%q, %v) error = %v", userID, role, err)
	}
}

// startRegistration creates a pending_owner tenant through the public RPC.
func (p *process) startRegistration(t *testing.T, name string) *tenantv1.StartTenantRegistrationResponse {
	t.Helper()

	res, err := p.tenantClient.StartTenantRegistration(context.Background(), connectrpc.NewRequest(&tenantv1.StartTenantRegistrationRequest{Name: name, ContractPlan: "standard"}))
	if err != nil {
		t.Fatalf("StartTenantRegistration(%q) error = %v", name, err)
	}

	return res.Msg
}

// claim calls ClaimTenantOwnership with token as the bearer credential.
func (p *process) claim(t *testing.T, token, tenantID, claimToken string) (*tenantv1.Tenant, error) {
	t.Helper()

	res, err := p.tenantClient.ClaimTenantOwnership(context.Background(), authorized(token, &tenantv1.ClaimTenantOwnershipRequest{TenantId: tenantID, OwnershipClaimToken: claimToken}))
	if err != nil {
		return nil, err
	}

	return res.Msg.GetTenant(), nil
}

// createEvent creates an event through TenantService and returns it as the
// wire carries it; tests of either service reach for it to have an event.
func (p *process) createEvent(t *testing.T, token, tenantPublicID, name string) *tenantv1.Event {
	t.Helper()

	res, err := p.tenantClient.CreateEvent(context.Background(), authorized(token, &tenantv1.CreateEventRequest{TenantId: tenantPublicID, Name: name}))
	if err != nil {
		t.Fatalf("CreateEvent(%q) error = %v", name, err)
	}

	return res.Msg.GetEvent()
}
