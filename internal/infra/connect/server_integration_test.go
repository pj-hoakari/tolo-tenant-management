package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/jwtgen"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

type testInternalJWTs struct {
	tenantAccess string
	registration string
	service      string
	jwks         jwtgen.JWKS
}

var (
	testTokensOnce sync.Once
	testTokens     testInternalJWTs
	testTokensErr  error

	integrationDB        *sqlx.DB
	integrationContainer *postgres.PostgresContainer
	integrationSetupOnce sync.Once
	integrationSetupErr  error
	integrationDBMu      sync.Mutex
)

func TestMain(m *testing.M) {
	code := m.Run()

	if integrationDB != nil {
		_ = integrationDB.Close()
	}

	if integrationContainer != nil {
		_ = integrationContainer.Terminate(context.Background())
	}

	os.Exit(code)
}

func internalJWTs(t *testing.T) testInternalJWTs {
	t.Helper()
	testTokensOnce.Do(func() {
		configs := []jwtgen.Config{
			{Issuer: internalJWTIssuer, Audience: internalJWTAudience, TokenUse: "tenant_access", TenantID: "test-tenant-id", Scope: "tenant_access tenant.write events.read events.write", KeyID: "tenant-key", TTL: time.Hour},
			{Issuer: internalJWTIssuer, Audience: internalJWTAudience, TokenUse: "registration", Scope: "tenant.register", KeyID: "registration-key", TTL: time.Hour},
			{Issuer: internalJWTIssuer, Audience: internalJWTAudience, TokenUse: "service", KeyID: "service-key", TTL: time.Hour},
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

func newJWKSStub(t *testing.T, jwks jwtgen.JWKS) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(jwks); err != nil {
			t.Errorf("encode JWKS: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	return server
}

func newIntegrationTenantRepository(t *testing.T) *db.PostgresTenantRepository {
	t.Helper()
	integrationSetupOnce.Do(setupIntegrationDatabase)

	if integrationSetupErr != nil {
		t.Fatalf("set up PostgreSQL integration test database: %v", integrationSetupErr)
	}

	integrationDBMu.Lock()
	t.Cleanup(integrationDBMu.Unlock)

	if _, err := integrationDB.Exec(`TRUNCATE events, tenants`); err != nil {
		t.Fatalf("truncate PostgreSQL integration test database: %v", err)
	}

	return db.NewPostgresTenantRepository(integrationDB)
}

func setupIntegrationDatabase() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	integrationContainer, integrationSetupErr = postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("tenant_management"),
		postgres.WithUsername("tenant_management"),
		postgres.WithPassword("tenant_management"),
		postgres.WithInitScripts(integrationMigrationPath()),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if integrationSetupErr != nil {
		return
	}

	databaseURL, err := integrationContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		integrationSetupErr = fmt.Errorf("get test database URL: %w", err)

		return
	}

	integrationDB, integrationSetupErr = sqlx.ConnectContext(ctx, "pgx", databaseURL)
}

func integrationMigrationPath() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("locate integration test source file")
	}

	return filepath.Join(filepath.Dir(filename), "..", "..", "..", "migrations", "000001_init.up.sql")
}
