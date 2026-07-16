module github.com/pj-hoakari/go-service-template

go 1.26.3

tool (
	connectrpc.com/connect/cmd/protoc-gen-connect-go
	github.com/pj-hoakari/protoc-gen-authz-go/cmd/protoc-gen-authz-go
	google.golang.org/protobuf/cmd/protoc-gen-go
)

require (
	connectrpc.com/connect v1.20.0
	google.golang.org/protobuf v1.36.11
)

require github.com/pj-hoakari/protoc-gen-authz-go v0.2.0
