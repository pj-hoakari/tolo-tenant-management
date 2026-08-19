package db

import (
	"context"
	"fmt"

	"github.com/XSAM/otelsql"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/jmoiron/sqlx"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// driverName is the database/sql driver wrapped with OpenTelemetry
// instrumentation. It is also handed to sqlx.NewDb so that sqlx keeps using the
// pgx bindvar style ($1, $2, ...).
const driverName = "pgx"

// Open connects to PostgreSQL through an OpenTelemetry-instrumented pgx driver
// and verifies the connection.
//
// Every statement issued through the returned *sqlx.DB produces a span carrying
// the SQL text (db.query.text), so repository queries show up as children of the
// RPC span that triggered them. When no tracer provider is configured the
// instrumentation falls back to the global no-op provider.
func Open(ctx context.Context, databaseURL string) (*sqlx.DB, error) {
	sqlDB, err := otelsql.Open(driverName, databaseURL,
		otelsql.WithAttributes(semconv.DBSystemNamePostgreSQL),
		otelsql.WithSpanOptions(otelsql.SpanOptions{
			// Record the SQL text on query spans.
			DisableQuery: false,
			// driver.ErrSkip is a normal part of the database/sql fallback
			// protocol and would otherwise mark spans as failed.
			DisableErrSkip: true,
			// Row iteration and session resets add a lot of spans without
			// telling much about the query itself.
			OmitRows:             true,
			RowsNext:             false,
			OmitConnResetSession: true,
			Ping:                 false,
			OmitConnPrepare:      false,
			OmitConnQuery:        false,
			OmitConnectorConnect: false,
			RecordError:          nil,
			SpanFilter:           nil,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db := sqlx.NewDb(sqlDB, driverName)

	if err := db.PingContext(ctx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("ping database: %w; close database: %v", err, closeErr)
		}

		return nil, fmt.Errorf("ping database: %w", err)
	}

	return db, nil
}
