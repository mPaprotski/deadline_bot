package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"deadline_bot/migrations"

	_ "github.com/tursodatabase/libsql-client-go/libsql"
	_ "modernc.org/sqlite"
)

type Storage struct {
	db     *sql.DB
	logger *slog.Logger
}

func New(ctx context.Context, dbPath string, logger *slog.Logger) (*Storage, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// In-memory sqlite for tests (e.g. ":memory:") doesn't need dir creation
	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file::memory:") {
		dir := filepath.Dir(dbPath)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("failed to create database directory %s: %w", dir, err)
			}
		}
	}

	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// MVP requirement: single connection pool to simplify concurrency control and prevent sqlite write locking conflicts.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	// Explicitly apply pragmas to ensure connection compliance
	pragmaQuery := `
		PRAGMA foreign_keys = ON;
		PRAGMA journal_mode = WAL;
		PRAGMA busy_timeout = 5000;
	`
	if _, err := db.ExecContext(ctx, pragmaQuery); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to configure sqlite PRAGMA settings: %w", err)
	}

	storage := &Storage{
		db:     db,
		logger: logger,
	}

	return storage, nil
}

// NewRemote connects to a persistent Turso/libSQL database.
func NewRemote(ctx context.Context, databaseURL, authToken string, logger *slog.Logger) (*Storage, error) {
	if logger == nil {
		logger = slog.Default()
	}

	parsedURL, err := url.Parse(databaseURL)
	if err != nil || parsedURL.Scheme != "libsql" || parsedURL.Host == "" {
		return nil, fmt.Errorf("invalid Turso database URL")
	}
	query := parsedURL.Query()
	query.Set("authToken", authToken)
	parsedURL.RawQuery = query.Encode()

	db, err := sql.Open("libsql", parsedURL.String())
	if err != nil {
		return nil, fmt.Errorf("failed to open Turso database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to connect to Turso database: %w", err)
	}

	return &Storage{db: db, logger: logger}, nil
}

func (s *Storage) Close() error {
	return s.db.Close()
}

func (s *Storage) DB() *sql.DB {
	return s.db
}

func (s *Storage) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// Migrate executes all pending SQL migrations from embedded migrations.FS in lexical order.
func (s *Storage) Migrate(ctx context.Context) error {
	// Ensure migration table exists
	createTableQuery := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		);
	`
	if _, err := s.db.ExecContext(ctx, createTableQuery); err != nil {
		return fmt.Errorf("failed to initialize schema_migrations table: %w", err)
	}

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("failed to read embedded migrations: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)

	for _, file := range files {
		var exists int
		err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", file).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to check migration status for %s: %w", file, err)
		}
		if exists > 0 {
			continue
		}

		s.logger.Info("Applying database migration", "migration", file)
		content, err := migrations.FS.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", file, err)
		}

		err = s.WithTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(content)); err != nil {
				return fmt.Errorf("failed to execute migration %s: %w", file, err)
			}
			now := time.Now().Unix()
			if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", file, now); err != nil {
				return fmt.Errorf("failed to record migration %s: %w", file, err)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("migration %s failed: %w", file, err)
		}
		s.logger.Info("Applied database migration successfully", "migration", file)
	}

	return nil
}
