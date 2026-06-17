package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lightwave-media/lightwave-cli/internal/config"
)

var pool *pgxpool.Pool

// Connect establishes connection to PostgreSQL
func Connect(ctx context.Context) (*pgxpool.Pool, error) {
	if pool != nil {
		return pool, nil
	}

	cfg := config.Get()
	if cfg == nil {
		return nil, errors.New("no configuration found; run `lw config init` to initialize")
	}
	connStr := cfg.GetDSN()

	var err error
	pool, err = pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, WrapConnectError(err, cfg.DisplayHost(), cfg.DisplayPort())
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		return nil, WrapConnectError(err, cfg.DisplayHost(), cfg.DisplayPort())
	}

	// Set tenant schema
	if err := SetTenantSchema(ctx, pool, cfg.Tenant); err != nil {
		return nil, err
	}

	return pool, nil
}

// SetTenantSchema sets the PostgreSQL search_path to the tenant schema
func SetTenantSchema(ctx context.Context, pool *pgxpool.Pool, tenant string) error {
	query := fmt.Sprintf("SET search_path TO %s, public", pgx.Identifier{tenant}.Sanitize())
	_, err := pool.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to set tenant schema: %w", err)
	}
	return nil
}

// Close closes the database connection pool
func Close() {
	if pool != nil {
		pool.Close()
	}
}

// GetPool returns the connection pool (connects if not already connected)
func GetPool(ctx context.Context) (*pgxpool.Pool, error) {
	if pool == nil {
		return Connect(ctx)
	}
	return pool, nil
}
