package connect

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	connectrpc "connectrpc.com/connect"
	internaljwt "github.com/pj-hoakari/internal-jwt-handling"
	"github.com/pj-hoakari/protoc-gen-authz-go/authz"
)

// verifierStub stands in for the internal JWT verifier; these tests never
// present a token, so it is only there for the handler to be built.
type verifierStub struct{}

func (verifierStub) Verify(context.Context, string) (internaljwt.Claims, error) {
	return internaljwt.Claims{}, errors.New("verifier stub")
}

func TestNewHandlerWithVerifierServesHealthz(t *testing.T) {
	t.Parallel()

	handler, err := NewHandlerWithVerifier(verifierStub{})
	if err != nil {
		t.Fatalf("NewHandlerWithVerifier() error = %v", err)
	}

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	resp, err := server.Client().Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz error = %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Errorf("GET /healthz = %d %q, want %d %q", resp.StatusCode, body, http.StatusOK, "ok")
	}
}

// TestNewHandlerWithVerifierMountsEveryService checks that each mount is handed
// the mux, an auth builder that yields an interceptor for its policies, and the
// process-wide interceptors, and that a mount's failure fails the handler.
func TestNewHandlerWithVerifierMountsEveryService(t *testing.T) {
	t.Parallel()

	var mounted []string

	mount := func(name string) Mount {
		return func(mux *http.ServeMux, auth AuthInterceptor, interceptors ...connectrpc.Interceptor) error {
			if mux == nil {
				t.Errorf("mount %s: mux is nil", name)
			}

			if len(interceptors) == 0 {
				t.Errorf("mount %s: no process-wide interceptors", name)
			}

			if _, err := auth(authz.Policies{"/test." + name + "/Call": {Level: authz.LevelAuthenticated}}); err != nil {
				t.Errorf("mount %s: auth() error = %v", name, err)
			}

			mounted = append(mounted, name)

			return nil
		}
	}

	if _, err := NewHandlerWithVerifier(verifierStub{}, mount("first"), mount("second")); err != nil {
		t.Fatalf("NewHandlerWithVerifier() error = %v", err)
	}

	if got, want := strings.Join(mounted, ","), "first,second"; got != want {
		t.Errorf("mounted = %q, want %q", got, want)
	}

	errMount := errors.New("mount failed")
	failing := func(*http.ServeMux, AuthInterceptor, ...connectrpc.Interceptor) error { return errMount }

	if _, err := NewHandlerWithVerifier(verifierStub{}, mount("first"), failing); !errors.Is(err, errMount) {
		t.Errorf("NewHandlerWithVerifier() error = %v, want %v", err, errMount)
	}
}

func TestInternalErrorCodes(t *testing.T) {
	// InternalError logs the cause through the process-wide default logger,
	// which is replaced here so the test output stays clean; the logged record
	// itself is covered by the tenant transport's tests.
	slog.SetDefault(slog.New(slog.DiscardHandler))

	tests := []struct {
		name string
		err  error
		want connectrpc.Code
	}{
		{name: "canceled", err: context.Canceled, want: connectrpc.CodeCanceled},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: connectrpc.CodeDeadlineExceeded},
		{name: "anything else", err: errors.New("secret detail"), want: connectrpc.CodeInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := InternalError(context.Background(), tt.err)
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("InternalError(%v) code = %v, want %v", tt.err, got, tt.want)
			}

			if tt.want == connectrpc.CodeInternal && strings.Contains(err.Error(), "secret detail") {
				t.Errorf("error = %q, want it to omit the underlying failure", err)
			}
		})
	}
}
