module github.com/pj-hoakari/tolo-tenant-management

go 1.26.3

tool (
	connectrpc.com/connect/cmd/protoc-gen-connect-go
	github.com/pj-hoakari/protoc-gen-authz-go/cmd/protoc-gen-authz-go
	go.uber.org/mock/mockgen
	golang.org/x/tools/cmd/stringer
	google.golang.org/protobuf/cmd/protoc-gen-go
)

require (
	connectrpc.com/connect v1.20.0
	google.golang.org/protobuf v1.36.11
)

require github.com/pj-hoakari/protoc-gen-authz-go v0.2.0

require (
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
	go.uber.org/mock v0.6.0
)

require (
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
)
