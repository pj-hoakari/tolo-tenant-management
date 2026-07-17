module github.com/pj-hoakari/tolo-tenant-management

go 1.26.3

tool (
	connectrpc.com/connect/cmd/protoc-gen-connect-go
	github.com/dmarkham/enumer
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
	github.com/jackc/pgx/v5 v5.10.0
	github.com/jmoiron/sqlx v1.4.0
	go.uber.org/mock v0.6.0
)

require (
	github.com/dmarkham/enumer v1.6.3 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/pascaldekloe/name v1.0.0 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
)
