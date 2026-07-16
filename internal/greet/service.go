package greet

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	greetv1 "github.com/pj-hoakari/go-service-template/gen/greet/v1"
	"github.com/pj-hoakari/go-service-template/gen/greet/v1/greetv1connect"
)

type Service struct {
	greetv1connect.UnimplementedGreetServiceHandler
}

func NewService() *Service {
	return &Service{
		UnimplementedGreetServiceHandler: greetv1connect.UnimplementedGreetServiceHandler{},
	}
}

func (s *Service) Greet(_ context.Context, req *connect.Request[greetv1.GreetRequest]) (*connect.Response[greetv1.GreetResponse], error) {
	name := req.Msg.GetName()
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("name is required"))
	}

	res := connect.NewResponse(&greetv1.GreetResponse{
		Greeting: fmt.Sprintf("Hello, %s!", name),
	})

	return res, nil
}
