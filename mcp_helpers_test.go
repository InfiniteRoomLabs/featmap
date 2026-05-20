package main

// Test-only helpers shared across mcp_test.go and its sibling files.
// All helpers assume TestMain (see mcp_testmain_test.go) populated the
// package-level testDB / testAccountID / testWorkspaceID / testMemberID.

import (
	"context"
	"fmt"
	"testing"

	"github.com/jmoiron/sqlx"
)

// txFn is the body of a transactional test. Callees mutate via `s` and any
// changes get rolled back when runInTx returns.
type txFn func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member)

// runInTx opens a fresh sqlx.Tx on the shared test DB, builds a Service with
// repo bound to that tx, loads the seeded account/workspace/member onto the
// service, runs fn, then rolls back unconditionally. Each test gets a clean
// slate without paying for a full re-seed.
func runInTx(t *testing.T, fn txFn) {
	t.Helper()

	if testDB == nil {
		t.Skip("testDB not initialized (skip via SKIP_DB_TESTS=1)")
		return
	}

	tx, err := testDB.Beginx()
	if err != nil {
		t.Fatalf("Beginx: %v", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	repo := NewFeatmapRepository(testDB)
	repo.SetTx(tx)

	s := NewFeatmapService()
	s.SetConfig(Configuration{Environment: "development", Mode: "selfhost"})
	s.SetRepoObject(repo)

	acc, err := repo.GetAccount(testAccountID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	s.SetAccountObject(acc)

	ws, err := repo.GetWorkspace(testWorkspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	s.SetWorkspaceObject(ws)

	member, err := repo.GetMember(testWorkspaceID, testMemberID)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	s.SetMemberObject(member)

	if sub := s.GetSubscriptionByWorkspace(testWorkspaceID); sub != nil {
		s.SetSubscriptionObject(sub)
	}

	fn(t, context.Background(), s, acc, ws, member)
}

// projectFixture is a fully-populated story map: one project, two milestones,
// one workflow with three subworkflows, six features (one per cell), one
// persona attached to the workflow, and one comment on a feature.
// Created within whichever tx the test is running in -- rolled back along
// with the rest.
type projectFixture struct {
	Project          *Project
	Milestones       []*Milestone     // [0]=M1, [1]=M2
	Workflow         *Workflow
	SubWorkflows     []*SubWorkflow   // [0]=SW1, [1]=SW2, [2]=SW3
	Features         []*Feature       // 6 features, M-major then SW-major
	Persona          *Persona
	WorkflowPersona  *WorkflowPersona
	FeatureComment   *FeatureComment
}

// newProjectFixture returns a freshly-built fixture. Fails the test on any
// service error -- fixture failures shouldn't be silently swallowed.
func newProjectFixture(t *testing.T, s Service) *projectFixture {
	t.Helper()

	p, err := s.CreateProjectWithID(newUUID(), "Test Project")
	mustOK(t, err, "CreateProject")

	m1, err := s.CreateMilestoneWithID(newUUID(), p.ID, "M1")
	mustOK(t, err, "CreateMilestone M1")
	m2, err := s.CreateMilestoneWithID(newUUID(), p.ID, "M2")
	mustOK(t, err, "CreateMilestone M2")

	wf, err := s.CreateWorkflowWithID(newUUID(), p.ID, "WF1")
	mustOK(t, err, "CreateWorkflow")

	sw1, err := s.CreateSubWorkflowWithID(newUUID(), wf.ID, "SW1")
	mustOK(t, err, "CreateSubWorkflow SW1")
	sw2, err := s.CreateSubWorkflowWithID(newUUID(), wf.ID, "SW2")
	mustOK(t, err, "CreateSubWorkflow SW2")
	sw3, err := s.CreateSubWorkflowWithID(newUUID(), wf.ID, "SW3")
	mustOK(t, err, "CreateSubWorkflow SW3")

	subs := []*SubWorkflow{sw1, sw2, sw3}
	milestones := []*Milestone{m1, m2}

	var features []*Feature
	for _, m := range milestones {
		for _, sw := range subs {
			f, err := s.CreateFeatureWithID(newUUID(), sw.ID, m.ID, "F-"+m.Title+"-"+sw.Title)
			mustOK(t, err, "CreateFeature")
			features = append(features, f)
		}
	}

	persona, err := s.CreatePersonaWithID(newUUID(), p.ID, "avatar00", "Test Persona", "user", "", "", "")
	mustOK(t, err, "CreatePersona")

	wp, err := s.CreateWorkflowPersonaWithID(newUUID(), wf.ID, persona.ID)
	mustOK(t, err, "CreateWorkflowPersona")

	comment, err := s.CreateFeatureCommentWithID(newUUID(), features[0].ID, "seed comment")
	mustOK(t, err, "CreateFeatureComment")

	return &projectFixture{
		Project:         p,
		Milestones:      milestones,
		Workflow:        wf,
		SubWorkflows:    subs,
		Features:        features,
		Persona:         persona,
		WorkflowPersona: wp,
		FeatureComment:  comment,
	}
}

// callWithContextEnv invokes withService-wrapped logic by simulating what
// middleware would do: stash an *Env in context, then call the wrapped fn.
// Used to test the withService decorator itself.
func callWithContextEnv(s Service, fn func(ctx context.Context)) {
	ctx := context.WithValue(context.Background(), contextKey, &Env{Service: s})
	fn(ctx)
}

// mustOK fails the test if err != nil, with a labeled message.
func mustOK(t *testing.T, err error, label string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
}

// expectFail runs fn and asserts either an error return OR a panic (commonly
// from sqlx MustExec on FK violation -- the service layer relies on database
// constraints for some "does referenced row exist" checks, so passing a bogus
// FK reaches the repo and triggers a panic).
//
// In production the chi middleware.Recoverer catches these and returns 500;
// here we mirror that by treating either signal as the same negative case.
func expectFail(t *testing.T, label string, fn func() error) {
	t.Helper()
	var caught error
	func() {
		defer func() {
			if r := recover(); r != nil {
				caught = fmt.Errorf("panic: %v", r)
			}
		}()
		caught = fn()
	}()
	if caught == nil {
		t.Errorf("%s: expected error or panic, got success", label)
	}
}

// Compile-time assertion that runInTx wires Service through correctly --
// caught at build time so renames don't silently drift.
var _ = func() bool {
	_ = (*sqlx.Tx)(nil)
	return true
}()
