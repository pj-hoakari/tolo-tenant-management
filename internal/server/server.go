package server

import (
	"log"
	"net/http"

	"github.com/pj-hoakari/go-service-template/gen/greet/v1/greetv1connect"
	"github.com/pj-hoakari/go-service-template/internal/greet"
)

func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)

	path, handler := greetv1connect.NewGreetServiceHandlerWithAuthz(
		greet.NewService(),
		newExampleGreetAuthzVerifier(),
	)
	mux.Handle(path, handler)

	return mux
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write([]byte("ok")); err != nil {
		log.Printf("healthz response write: %v", err)
	}
}
