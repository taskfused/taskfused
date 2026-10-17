// Copyright 2026 TaskFused. All rights reserved.

// Package taskfused provides durable shared operations with leased, at-least-once
// execution on one host. External side effects require handler-level idempotency.
package taskfused

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const MaxInputBytes = 1024 * 1024
const schemaVersion = 5

var (
	ErrInvalid           = errors.New("invalid request")
	ErrConflict          = errors.New("operation identity already has different input")
	ErrDuplicate         = errors.New("operation exists; joining was not enabled")
	ErrNotFound          = errors.New("operation not found")
	ErrNoWork            = errors.New("no eligible operation")
	ErrLeaseLost         = errors.New("attempt no longer owns a live lease")
	ErrOperationCanceled = errors.New("operation canceled")
	ErrRetryState        = errors.New("only failed operations can be retried")
	ErrRetryStale        = errors.New("failed attempt count changed")
	ErrRetryConflict     = errors.New("retry action key already has different parameters")
	ErrRetryBudget       = errors.New("lifetime attempt limit exceeded")
	ErrPolicyConflict    = errors.New("operation identity already has a different retry policy")
)

// Request identifies an operation within a namespace. Input is compared byte for
// byte. Callers needing semantic JSON equality must canonicalize before submitting.
// JoinExisting permits returning an existing operation with identical input,
// including after a restart. It never merges work across namespaces or versions.
type Request struct {
	Namespace    string `json:"namespace"`
	Handler      string `json:"handler"`
	Version      string `json:"version"`
	Key          string `json:"key"`
	Input        []byte `json:"input"`
	JoinExisting bool   `json:"join_existing"`
	// MaxAttempts is the immutable initial claim budget; zero defaults to three.
	MaxAttempts int           `json:"max_attempts"`
	RetryDelay  time.Duration `json:"retry_delay"`
}

// Operation is a durable record. Result is populated only on success and may
// later be removed by explicit pruning; ResultPruned distinguishes that outcome.
// Input is encoded as base64 by encoding/json, preserving arbitrary bytes.
type Operation struct {
	ID           string        `json:"id"`
	Namespace    string        `json:"namespace"`
	Handler      string        `json:"handler"`
	Version      string        `json:"version"`
	Key          string        `json:"key"`
	Input        []byte        `json:"input"`
	InputSHA256  string        `json:"input_sha256"`
	State        string        `json:"state"`
	CreatedAt    time.Time     `json:"created_at"`
	AttemptCount int           `json:"attempt_count"`
	MaxAttempts  int           `json:"max_attempts"`
	RetryDelay   time.Duration `json:"retry_delay"`
	// AttemptLimit is the effective lifetime budget, including operator grants.
	AttemptLimit    int        `json:"attempt_limit"`
	RetryCycleStart int        `json:"retry_cycle_start"`
	RetryEligible   bool       `json:"retry_eligible"`
	Result          []byte     `json:"result,omitempty"`
	ResultPruned    bool       `json:"result_pruned"`
	LastError       string     `json:"last_error,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

type Submission struct {
	Operation Operation `json:"operation"`
	Joined    bool      `json:"joined"`
}

// Store owns a connection to one local SQLite database. Independent Store
// instances may submit concurrently on the same host. Network filesystems and
// distributed operation are outside this prototype's supported deployment model.
type Store struct {
	waitMu    sync.Mutex
	waits     map[waitKey]*waitPoll
	waitWG    sync.WaitGroup
	closed    bool
	closeOnce sync.Once
	closeErr  error
	db        *sql.DB
	now       func() time.Time // Fixed on open; replace only before use in deterministic tests.
}

// Open creates or opens a database, verifies its schema and runs initial setup.
// It uses WAL mode and FULL synchronization. WAL and SHM sidecars may exist while
// open; use SQLite-aware backups rather than copying a live main database file.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" || path == ":memory:" {
		return nil, fmt.Errorf("%w: a persistent database path is required", ErrInvalid)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("database path: %w", err)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	q := u.Query()
	q.Set("_busy_timeout", "5000")
	q.Set("_journal_mode", "WAL")
	q.Set("_synchronous", "FULL")
	q.Set("_foreign_keys", "on")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open taskfused: %w", err)
	}
	return s, nil
}

// Close stops local wait pollers, then closes SQLite. Concurrent calls are safe.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.waitMu.Lock()
		s.closed = true
		for _, p := range s.waits {
			p.cancel()
		}
		s.waitMu.Unlock()
		s.waitWG.Wait()
		s.closeErr = s.db.Close()
	})
	return s.closeErr
}

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("unsupported schema version %d (maximum %d)", version, schemaVersion)
	}
	if version == 0 {
		// Never adopt an unrelated SQLite database whose schema happens to be version 0.
		var tables int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return err
		}
		if tables != 0 {
			return errors.New("database already contains an unrecognized schema")
		}
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE taskfused_meta (name TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO taskfused_meta VALUES ('application', 'taskfused');
CREATE TABLE operations (
 id TEXT PRIMARY KEY,
 namespace TEXT NOT NULL,
 handler TEXT NOT NULL,
 handler_version TEXT NOT NULL,
 operation_key TEXT NOT NULL,
 input BLOB NOT NULL,
 input_sha256 TEXT NOT NULL,
 state TEXT NOT NULL CHECK (state = 'queued'),
 created_at TEXT NOT NULL,
 UNIQUE (namespace, handler, handler_version, operation_key)
);
CREATE TABLE events (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 operation_id TEXT NOT NULL REFERENCES operations(id),
 kind TEXT NOT NULL,
 occurred_at TEXT NOT NULL
);
PRAGMA user_version = 1;`); err != nil {
			return err
		}
	}
	var application string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM taskfused_meta WHERE name='application'").Scan(&application); err != nil {
		return err
	}
	if application != "taskfused" {
		return errors.New("database application marker is not taskfused")
	}
	if version < 2 {
		if err := migrateV2(ctx, tx); err != nil {
			return err
		}
	}
	if version < 3 {
		if err := migrateV3(ctx, tx); err != nil {
			return err
		}
	}
	if version < 4 {
		if err := migrateV4(ctx, tx); err != nil {
			return err
		}
	}
	if version < 5 {
		if err := migrateV5(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validate(r Request) error {
	for _, field := range []struct{ name, value string }{
		{"namespace", r.Namespace}, {"handler", r.Handler}, {"version", r.Version}, {"key", r.Key},
	} {
		if strings.TrimSpace(field.value) == "" || strings.ContainsRune(field.value, '\x00') || len(field.value) > 256 {
			return fmt.Errorf("%w: %s must contain 1–256 bytes and no NUL", ErrInvalid, field.name)
		}
	}
	if len(r.Input) > MaxInputBytes {
		return fmt.Errorf("%w: input exceeds %d bytes", ErrInvalid, MaxInputBytes)
	}
	return nil
}

const identityColumns = "id, namespace, handler, handler_version, operation_key, input, input_sha256, state, created_at"
const columns = identityColumns + ", attempt_count, max_attempts, retry_base_ns, result, last_error, completed_at, attempt_limit, retry_cycle_start, result_pruned"

type scanner interface{ Scan(...any) error }

func scanOperation(row scanner) (Operation, error) {
	var op Operation
	var created string
	var completed sql.NullString
	if err := row.Scan(&op.ID, &op.Namespace, &op.Handler, &op.Version, &op.Key, &op.Input, &op.InputSHA256, &op.State, &created, &op.AttemptCount, &op.MaxAttempts, &op.RetryDelay, &op.Result, &op.LastError, &completed, &op.AttemptLimit, &op.RetryCycleStart, &op.ResultPruned); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Operation{}, ErrNotFound
		}
		return Operation{}, err
	}
	var err error
	op.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return Operation{}, fmt.Errorf("invalid stored timestamp: %w", err)
	}
	if completed.Valid {
		t, err := time.Parse(time.RFC3339Nano, completed.String)
		if err != nil {
			return Operation{}, err
		}
		op.CompletedAt = &t
	}
	op.RetryEligible = op.State == StateFailed && op.AttemptCount < MaxLifetimeAttempts
	return op, nil
}

// Submit commits the operation and its submission event atomically before
// reporting acceptance. Joining uses an explicit durable identity, not inference
// from payload similarity. Join events are not recorded as execution attempts.
func (s *Store) Submit(ctx context.Context, r Request) (Submission, error) {
	if err := validate(r); err != nil {
		return Submission{}, err
	}
	attempts, delay, err := retryPolicy(r.MaxAttempts, r.RetryDelay)
	if err != nil {
		return Submission{}, err
	}
	input := append([]byte{}, r.Input...)
	sum := sha256.Sum256(input)
	hash := hex.EncodeToString(sum[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Submission{}, err
	}
	defer tx.Rollback()
	existing, err := scanOperation(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM operations WHERE namespace=? AND handler=? AND handler_version=? AND operation_key=?", r.Namespace, r.Handler, r.Version, r.Key))
	if err == nil {
		if existing.InputSHA256 != hash {
			return Submission{}, ErrConflict
		}
		// Check actual bytes too; identity must not depend on hash collision assumptions.
		if string(existing.Input) != string(input) {
			return Submission{}, ErrConflict
		}
		if existing.MaxAttempts != attempts || existing.RetryDelay != delay {
			return Submission{}, ErrPolicyConflict
		}
		if !r.JoinExisting {
			return Submission{}, ErrDuplicate
		}
		if err := tx.Commit(); err != nil {
			return Submission{}, err
		}
		return Submission{Operation: existing, Joined: true}, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Submission{}, err
	}
	if err := checkAdmission(ctx, tx, r.Namespace, true); err != nil {
		return Submission{}, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Submission{}, err
	}
	op := Operation{
		ID: "op_" + hex.EncodeToString(random[:]), Namespace: r.Namespace,
		Handler: r.Handler, Version: r.Version, Key: r.Key, Input: input,
		InputSHA256: hash, State: "queued", CreatedAt: s.now().UTC(), MaxAttempts: attempts, RetryDelay: delay, AttemptLimit: attempts,
	}
	created := op.CreatedAt.Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO operations (`+identityColumns+`, max_attempts, retry_base_ns, next_run_ns, attempt_limit) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, op.ID, op.Namespace, op.Handler, op.Version, op.Key, op.Input, hash, op.State, created, attempts, delay.Nanoseconds(), op.CreatedAt.UnixNano(), attempts); err != nil {
		return Submission{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO events(operation_id, kind, occurred_at) VALUES (?, 'submitted', ?)", op.ID, created); err != nil {
		return Submission{}, err
	}
	if err := tx.Commit(); err != nil {
		return Submission{}, err
	}
	return Submission{Operation: op}, nil
}

// Get retrieves an operation by namespace and ID. Namespace is mandatory to avoid
// accidentally looking up another account's operation. Authentication belongs to
// the embedding application; this method alone is not an authorization boundary.
func (s *Store) Get(ctx context.Context, namespace, id string) (Operation, error) {
	if err := validateLookup(namespace, id); err != nil {
		return Operation{}, err
	}
	return scanOperation(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM operations WHERE namespace=? AND id=?", namespace, id))
}

func validateLookup(namespace, id string) error {
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(id) == "" {
		return ErrInvalid
	}
	return nil
}
