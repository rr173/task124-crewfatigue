// Package store is the SQLite-backed persistence layer for the aviation crew
// flight-duty & fatigue compliance engine. It owns the *sql.DB, applies
// idempotent schema migrations on Open (the recovery entry point) and exposes
// typed query methods grouped by resource in sibling files.
//
// SQLite is configured for single-writer safety: SetMaxOpenConns(1) plus
// _txlock=immediate. All mutations go through InTx. Reads performed inside a
// transaction must use the DBTX (tx) handle, not the Store handle, otherwise
// the single connection is held by the writer and the read deadlocks.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DBTX is the common interface over *sql.DB and *sql.Tx so store methods can
// run either at top level (Read) or inside a caller's transaction (ReadTx),
// which is required to avoid SetMaxOpenConns(1) read-during-write deadlocks.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store owns the *sql.DB.
type Store struct {
	db *sql.DB
}

// Open creates or opens the SQLite database at path, runs idempotent schema
// migrations, and returns a ready Store. It is the recovery entry point: any
// previously persisted state is available immediately after Open returns.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single-writer mode: at most one connection. Transactions take it
	// exclusively, so reads inside a tx MUST use the tx handle.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	s := &Store{db: db}
	if err := s.ensureMetaDefaults(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB exposes the underlying *sql.DB for callers that run transactions.
func (s *Store) DB() *sql.DB { return s.db }

// InTx runs fn inside a transaction. If fn returns an error the transaction
// is rolled back; otherwise it is committed. This is the single transactional
// path used by the service layer, ensuring duty-close / rest-completion /
// evaluation operations are atomic and restart-safe.
func (s *Store) InTx(ctx context.Context, fn func(tx DBTX) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	txd := txAdapter{tx: tx}
	if err := fn(txd); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// txAdapter wraps *sql.Tx to satisfy DBTX.
type txAdapter struct {
	tx *sql.Tx
}

func (t txAdapter) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, q, args...)
}
func (t txAdapter) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, q, args...)
}
func (t txAdapter) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, q, args...)
}

// ensureMetaDefaults seeds the meta table.
func (s *Store) ensureMetaDefaults() error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO meta(key, value) VALUES(?, ?)`, MetaKeySchemaVersion, "1")
	if err != nil {
		return fmt.Errorf("seed meta: %w", err)
	}
	return nil
}

// nowUTC returns the current time in UTC. Centralized so call sites are
// consistent and tests can spot non-UTC storage.
func nowUTC() time.Time { return time.Now().UTC() }
