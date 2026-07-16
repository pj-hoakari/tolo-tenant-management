package server

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	greetv1 "github.com/pj-hoakari/go-service-template/gen/greet/v1"
	"github.com/pj-hoakari/go-service-template/gen/greet/v1/greetv1connect"
)

func TestGreetServiceAuthz(t *testing.T) {
	t.Parallel()

	httpServer := httptest.NewServer(NewHandler())
	t.Cleanup(httpServer.Close)
	client := greetv1connect.NewGreetServiceClient(httpServer.Client(), httpServer.URL)

	t.Run("rejects missing bearer token", func(t *testing.T) {
		t.Parallel()

		_, err := client.Greet(context.Background(), connect.NewRequest(&greetv1.GreetRequest{Name: "Ada"}))
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("Greet() error code = %v, want %v", connect.CodeOf(err), connect.CodeUnauthenticated)
		}
	})

	t.Run("accepts bearer token with required scope", func(t *testing.T) {
		t.Parallel()

		req := connect.NewRequest(&greetv1.GreetRequest{Name: "Ada"})
		req.Header().Set("Authorization", exampleGreetAuthorizationHeader())

		res, err := client.Greet(context.Background(), req)
		if err != nil {
			t.Fatalf("Greet() error = %v", err)
		}

		if got, want := res.Msg.GetGreeting(), "Hello, Ada!"; got != want {
			t.Errorf("Greeting = %q, want %q", got, want)
		}
	})
}
