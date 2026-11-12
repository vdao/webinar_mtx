// Package store implements all database queries.
package store

import (
	"context"
	"database/sql"
)

// Store wraps the database handle with typed queries.
type Store struct {
	DB *sql.DB
}

// New creates a Store.
func New(db *sql.DB) *Store { return &Store{DB: db} }

// Execer is satisfied by both *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}
