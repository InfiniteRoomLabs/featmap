package main

import (
	"fmt"
	"log"
	"time"

	"github.com/jmoiron/sqlx"
)

// startPlanePoller launches a background goroutine that periodically syncs every
// project that has a Plane connection. Runs outside the HTTP request lifecycle,
// so it builds its own Service + repo + tx per cycle (mirrors how middleware
// would, but self-managed). interval "" or "0" disables it.
func startPlanePoller(db *sqlx.DB, config Configuration) {
	interval := config.PlanePollInterval
	if interval == "" || interval == "0" {
		log.Println("plane poller: disabled")
		return
	}
	d, err := time.ParseDuration(interval)
	if err != nil || d <= 0 {
		log.Printf("plane poller: invalid planePollInterval %q, disabled", interval)
		return
	}
	log.Printf("plane poller: every %s", d)
	go func() {
		ticker := time.NewTicker(d)
		defer ticker.Stop()
		for range ticker.C {
			runPlanePollCycle(db, config)
		}
	}()
}

// runPlanePollCycle is the body of one poll tick. It must never panic out to the
// caller: it runs inside the ticker goroutine launched by startPlanePoller, which
// has no recover() of its own, so an escaping panic would kill the whole server
// process. The outer recover() below is the last-resort backstop; the per-
// connection savepoint+recover loop is the real isolation (see SyncProject call).
func runPlanePollCycle(db *sqlx.DB, config Configuration) {
	// Last-resort backstop. Any panic that escapes the per-connection isolation
	// (e.g. from savepoint bookkeeping itself, or commit) is caught here so the
	// poller goroutine -- and the process -- survives to the next tick.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("plane poller: panic in poll cycle: %v", r)
		}
	}()

	tx, err := db.Beginx()
	if err != nil {
		log.Printf("plane poller: begin tx: %v", err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	repo := NewFeatmapRepository(db)
	repo.SetTx(tx)

	conns, err := repo.FindAllPlaneConnections()
	if err != nil {
		log.Printf("plane poller: list connections: %v", err)
		return
	}
	for i, conn := range conns {
		svc := NewFeatmapService()
		svc.SetConfig(config)
		svc.SetRepoObject(repo)
		// load the workspace member context the connection belongs to.
		// The poller acts as the connection's workspace; load any member of it.
		m, err := repo.GetMemberByWorkspaceFirst(conn.WorkspaceID)
		if err != nil {
			log.Printf("plane poller: no member for workspace %s: %v", conn.WorkspaceID, err)
			continue
		}
		svc.SetMemberObject(m)
		// A missing account is fatal for this connection: SyncProject -> SyncLink ->
		// CreateFeatureCommentWithID dereferences s.Acc.Name when a Plane comment is
		// pulled. Skipping SetAccountObject but still calling SyncProject would nil-
		// deref and panic. Treat it the same as a missing member: log and continue.
		acc, err := repo.GetAccount(m.AccountID)
		if err != nil {
			log.Printf("plane poller: no account for workspace %s (account %s): %v", conn.WorkspaceID, m.AccountID, err)
			continue
		}
		svc.SetAccountObject(acc)

		// Isolate each connection on its own SAVEPOINT of the shared cycle tx.
		// Service writes use tx.MustExec, which panics on any SQL error and leaves
		// postgres in an aborted-transaction state -- every later statement,
		// including the final Commit and other connections' writes, would then
		// fail. Without this, a single bad connection's panic triggers the deferred
		// cycle-wide Rollback, silently undoing every healthy connection's synced
		// state with no log attribution. SAVEPOINT + recover() converts the panic
		// into a logged, contained per-connection failure: ROLLBACK TO SAVEPOINT
		// recovers the tx so siblings and the final commit survive.
		// Fixed prefix + integer index -- never user input (savepoint names cannot
		// be parameterized in SQL).
		spName := fmt.Sprintf("plane_conn_%d", i)
		if err := repo.Savepoint(spName); err != nil {
			log.Printf("plane poller: savepoint for connection %s: %v", conn.ProjectID, err)
			continue
		}
		var serr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					serr = fmt.Errorf("panic: %v", r)
				}
			}()
			_, serr = svc.SyncProject(conn.ProjectID)
		}()
		if serr != nil {
			log.Printf("plane poller: sync project %s: %v", conn.ProjectID, serr)
			// Roll back this connection's (possibly aborted) writes so the shared tx
			// stays valid for the remaining connections and the final commit.
			_ = repo.RollbackToSavepoint(spName)
			_ = repo.ReleaseSavepoint(spName)
			continue
		}
		_ = repo.ReleaseSavepoint(spName)
	}
	if err := tx.Commit(); err != nil {
		log.Printf("plane poller: commit: %v", err)
		return
	}
	committed = true
}
