package main

// Shared test infrastructure for the MCP tool suite. Spins up a real
// PostgreSQL via testcontainers-go once per `go test ./...` invocation,
// applies all production migrations from the embedded bindata, and seeds
// a single account + workspace + member shared across every test.
//
// Per-test isolation is provided by `runInTx` in mcp_helpers_test.go --
// each test opens its own sqlx.Tx against the shared DB and rolls it back
// at the end, so writes made during a test never leak into siblings.
//
// To run with containers:   go test -tags integration ./...
// Without docker:           SKIP_DB_TESTS=1 go test ./...   (skips harness)

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/amborle/featmap/migrations"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	bindata "github.com/golang-migrate/migrate/v4/source/go_bindata"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Shared state populated by TestMain. Tests read these via runInTx.
var (
	testDB          *sqlx.DB
	testAccountID   string
	testWorkspaceID string
	testMemberID    string
	testEmail       = "tester@example.com"
)

func TestMain(m *testing.M) {
	if os.Getenv("SKIP_DB_TESTS") == "1" {
		// Allow `go vet` and CI lint passes without docker.
		os.Exit(m.Run())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pgContainer, err := postgres.Run(ctx,
		"postgres:11-alpine",
		postgres.WithDatabase("featmap_test"),
		postgres.WithUsername("featmap"),
		postgres.WithPassword("featmap"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start postgres container: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = pgContainer.Terminate(context.Background())
	}()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get connection string: %v\n", err)
		os.Exit(1)
	}

	if err := applyMigrations(connStr); err != nil {
		fmt.Fprintf(os.Stderr, "failed to apply migrations: %v\n", err)
		os.Exit(1)
	}

	db, err := sqlx.Connect("postgres", connStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect: %v\n", err)
		os.Exit(1)
	}
	testDB = db

	if err := seedTestAccount(db); err != nil {
		fmt.Fprintf(os.Stderr, "failed to seed test account: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	_ = db.Close()
	os.Exit(code)
}

// applyMigrations runs the embedded golang-migrate bindata source against
// the test postgres. Mirrors the production path in main.go.
func applyMigrations(connStr string) error {
	src := bindata.Resource(migrations.AssetNames(),
		func(name string) ([]byte, error) {
			return migrations.Asset(name)
		})

	d, err := bindata.WithInstance(src)
	if err != nil {
		return fmt.Errorf("bindata.WithInstance: %w", err)
	}

	mi, err := migrate.NewWithSourceInstance("go-bindata", d, connStr)
	if err != nil {
		return fmt.Errorf("migrate.New: %w", err)
	}
	if err := mi.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("mi.Up: %w", err)
	}
	return nil
}

// seedTestAccount creates the single account + workspace + member that every
// test reuses. Runs in its own tx, committed (other tests' tx-rollbacks see
// this committed data via MVCC).
//
// Uses service.Register so the seeded data exercises the same code path as
// production signup, including bcrypt + member level + subscription.
func seedTestAccount(db *sqlx.DB) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	repo := NewFeatmapRepository(db)
	repo.SetTx(tx)

	s := NewFeatmapService()
	// `selfhost` mode (anything not "hosted") gives an immediately-active
	// subscription with a 1000-year expiry -- skips Stripe entirely.
	s.SetConfig(Configuration{Environment: "development", Mode: "selfhost"})
	s.SetRepoObject(repo)

	ws, acc, member, err := s.Register("testws", "Test User", testEmail, "secret-password-123")
	if err != nil {
		return fmt.Errorf("Register: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	tx = nil // suppress deferred rollback

	testAccountID = acc.ID
	testWorkspaceID = ws.ID
	testMemberID = member.ID

	log.Printf("seeded test account %s, workspace %s, member %s", acc.ID, ws.ID, member.ID)
	return nil
}

// Compile-time guarantee that database/sql is imported; testcontainers' pq
// driver registration needs this transitively.
var _ = sql.ErrNoRows
