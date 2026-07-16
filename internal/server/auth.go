package server

import (
	"context"

	"connectrpc.com/connect"

	"github.com/pj-hoakari/go-service-template/gen/greet/v1/greetv1connect"
)

const exampleGreetBearerToken = "example-greet-token"

// newExampleGreetAuthzVerifier demonstrates how to adapt an application's
// identity provider to the Verifier generated from authz policy annotations.
// Replace the fixed token and scopes with validated identity claims in a real
// service.
func newExampleGreetAuthzVerifier() greetv1connect.Verifier {
	return greetv1connect.VerifierFunc(func(ctx context.Context, policy greetv1connect.AuthPolicy) error {
		if policy.Level == greetv1connect.AuthLevelPublic {
			return nil
		}

		callInfo, ok := connect.CallInfoForHandlerContext(ctx)
		if !ok || callInfo.RequestHeader().Get("Authorization") != "Bearer "+exampleGreetBearerToken {
			return connect.NewError(connect.CodeUnauthenticated, nil)
		}

		// In this example, a successfully authenticated token has this single
		// scope. A production verifier would read scopes from validated claims.
		grantedScopes := map[string]bool{"greeting.read": true}
		for _, requiredScope := range policy.RequiredScopes {
			if !grantedScopes[requiredScope] {
				return connect.NewError(connect.CodePermissionDenied, nil)
			}
		}

		if policy.Level == greetv1connect.AuthLevelInternal {
			return connect.NewError(connect.CodePermissionDenied, nil)
		}

		return nil
	})
}

func exampleGreetAuthorizationHeader() string {
	return "Bearer " + exampleGreetBearerToken
}
