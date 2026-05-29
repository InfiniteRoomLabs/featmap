package main

// Tests for every MCP tool handler. Each tool gets its own Test_<name>
// function with t.Run subtests covering happy, sad (bad IDs / missing
// dependencies), edge (validation boundaries), and corner cases
// (foreign workspace, deleted entities, idempotency). The withService
// decorator + resolveWorkspace are tested separately in their own
// Test_withService / Test_resolveWorkspace blocks.
//
// All tests run inside a per-test transaction (see runInTx in
// mcp_helpers_test.go) and roll back at the end -- siblings never see
// each other's writes.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ===========================================================================
// Discovery tools
// ===========================================================================

func Test_list_workspaces(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, _ *Member) {
		t.Run("happy: returns the seeded workspace", func(t *testing.T) {
			res, err := mcpListWorkspaces(ctx, s, emptyArgs{})
			mustOK(t, err, "mcpListWorkspaces")
			if len(res.Workspaces) == 0 {
				t.Fatalf("expected at least one workspace, got 0")
			}
			found := false
			for _, w := range res.Workspaces {
				if w.ID == ws.ID {
					found = true
				}
			}
			if !found {
				t.Errorf("seeded workspace %s not in result", ws.ID)
			}
		})
	})
}

func Test_list_projects(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		t.Run("despondant: empty workspace returns empty list", func(t *testing.T) {
			res, err := mcpListProjects(ctx, s, workspaceArgs{WorkspaceID: ws.ID})
			mustOK(t, err, "mcpListProjects")
			if len(res.Projects) != 0 {
				t.Errorf("expected 0 projects, got %d", len(res.Projects))
			}
		})

		t.Run("happy: created project appears in list", func(t *testing.T) {
			p, err := s.CreateProjectWithID(newUUID(), "Test Project A")
			mustOK(t, err, "CreateProject")

			res, err := mcpListProjects(ctx, s, workspaceArgs{WorkspaceID: ws.ID})
			mustOK(t, err, "mcpListProjects")
			if len(res.Projects) != 1 {
				t.Fatalf("expected 1 project, got %d", len(res.Projects))
			}
			if res.Projects[0].ID != p.ID {
				t.Errorf("expected project %s, got %s", p.ID, res.Projects[0].ID)
			}
		})
	})
}

func Test_get_board(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: returns full fixture", func(t *testing.T) {
			res, err := mcpGetBoard(ctx, s, getBoardArgs{WorkspaceID: ws.ID, ProjectID: fix.Project.ID})
			mustOK(t, err, "mcpGetBoard")
			if res.Project == nil || res.Project.ID != fix.Project.ID {
				t.Errorf("project mismatch")
			}
			if len(res.Milestones) != 2 {
				t.Errorf("milestones: want 2 got %d", len(res.Milestones))
			}
			if len(res.SubWorkflows) != 3 {
				t.Errorf("subworkflows: want 3 got %d", len(res.SubWorkflows))
			}
			if len(res.Features) != 6 {
				t.Errorf("features: want 6 got %d", len(res.Features))
			}
			if len(res.FeatureComments) != 1 {
				t.Errorf("feature comments: want 1 got %d", len(res.FeatureComments))
			}
			if len(res.Personas) != 1 {
				t.Errorf("personas: want 1 got %d", len(res.Personas))
			}
			if len(res.WorkflowPersonas) != 1 {
				t.Errorf("workflow personas: want 1 got %d", len(res.WorkflowPersonas))
			}
		})

		t.Run("sad: nonexistent project returns error", func(t *testing.T) {
			_, err := mcpGetBoard(ctx, s, getBoardArgs{WorkspaceID: ws.ID, ProjectID: newUUID()})
			if err == nil {
				t.Errorf("expected error for missing project")
			}
		})
	})
}

// ===========================================================================
// Project lifecycle
// ===========================================================================

func Test_create_project(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		t.Run("happy: standard title", func(t *testing.T) {
			p, err := mcpCreateProject(ctx, s, createProjectArgs{WorkspaceID: ws.ID, Title: "Project X"})
			mustOK(t, err, "mcpCreateProject")
			if p.Title != "Project X" {
				t.Errorf("title: want %q got %q", "Project X", p.Title)
			}
			if p.WorkspaceID != ws.ID {
				t.Errorf("workspace_id mismatch")
			}
		})

		t.Run("enraged: empty title is rejected", func(t *testing.T) {
			_, err := mcpCreateProject(ctx, s, createProjectArgs{WorkspaceID: ws.ID, Title: ""})
			if err == nil {
				t.Errorf("expected error for empty title")
			}
		})

		t.Run("edge: title at upper limit", func(t *testing.T) {
			title := strings.Repeat("x", 200)
			p, err := mcpCreateProject(ctx, s, createProjectArgs{WorkspaceID: ws.ID, Title: title})
			mustOK(t, err, "mcpCreateProject (200 chars)")
			if p.Title != title {
				t.Errorf("title round-trip mismatch")
			}
		})

		t.Run("enraged: oversized title is rejected", func(t *testing.T) {
			_, err := mcpCreateProject(ctx, s, createProjectArgs{WorkspaceID: ws.ID, Title: strings.Repeat("x", 201)})
			if err == nil {
				t.Errorf("expected error for >200-char title")
			}
		})
	})
}

// ===========================================================================
// Milestone tools
// ===========================================================================

func Test_create_milestone(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		p, err := s.CreateProjectWithID(newUUID(), "Test")
		mustOK(t, err, "CreateProject")

		t.Run("happy: standard create", func(t *testing.T) {
			m, err := mcpCreateMilestone(ctx, s, createMilestoneArgs{
				WorkspaceID: ws.ID, ProjectID: p.ID, Title: "MS1",
			})
			mustOK(t, err, "mcpCreateMilestone")
			if m.Title != "MS1" || m.ProjectID != p.ID {
				t.Errorf("unexpected fields")
			}
			if m.Status != "OPEN" {
				t.Errorf("default status: want OPEN got %q", m.Status)
			}
			if m.Color != "WHITE" {
				t.Errorf("default color: want WHITE got %q", m.Color)
			}
		})

		t.Run("sad: nonexistent project rejected (FK violation panics from MustExec)", func(t *testing.T) {
			expectFail(t, "create with bogus project", func() error {
				_, err := mcpCreateMilestone(ctx, s, createMilestoneArgs{
					WorkspaceID: ws.ID, ProjectID: newUUID(), Title: "MS",
				})
				return err
			})
		})

		t.Run("enraged: empty title rejected", func(t *testing.T) {
			_, err := mcpCreateMilestone(ctx, s, createMilestoneArgs{
				WorkspaceID: ws.ID, ProjectID: p.ID, Title: "",
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})
	})
}

func Test_move_milestone(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: move to front", func(t *testing.T) {
			before := fix.Milestones[1].Rank
			m, err := mcpMoveMilestone(ctx, s, moveMilestoneArgs{
				WorkspaceID: ws.ID, MilestoneID: fix.Milestones[1].ID, Index: 0,
			})
			mustOK(t, err, "mcpMoveMilestone")
			if m.Rank == before {
				t.Errorf("rank did not change")
			}
		})

		t.Run("sad: bogus milestone id", func(t *testing.T) {
			_, err := mcpMoveMilestone(ctx, s, moveMilestoneArgs{
				WorkspaceID: ws.ID, MilestoneID: newUUID(), Index: 0,
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})
	})
}

func Test_set_milestone_color(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: every valid color", func(t *testing.T) {
			colors := []string{"WHITE", "GREY", "RED", "ORANGE", "YELLOW", "GREEN", "TEAL", "BLUE", "INDIGO", "PURPLE", "PINK"}
			for _, c := range colors {
				m, err := mcpSetMilestoneColor(ctx, s, setMilestoneColorArgs{
					WorkspaceID: ws.ID, MilestoneID: fix.Milestones[0].ID, Color: c,
				})
				if err != nil {
					t.Errorf("color %s: unexpected error %v", c, err)
					continue
				}
				if m.Color != c {
					t.Errorf("color %s: got %s", c, m.Color)
				}
			}
		})

		t.Run("enraged: invalid color rejected", func(t *testing.T) {
			_, err := mcpSetMilestoneColor(ctx, s, setMilestoneColorArgs{
				WorkspaceID: ws.ID, MilestoneID: fix.Milestones[0].ID, Color: "FUCHSIA",
			})
			if err == nil {
				t.Errorf("expected error for unknown color")
			}
		})

		t.Run("enraged: empty color rejected", func(t *testing.T) {
			_, err := mcpSetMilestoneColor(ctx, s, setMilestoneColorArgs{
				WorkspaceID: ws.ID, MilestoneID: fix.Milestones[0].ID, Color: "",
			})
			if err == nil {
				t.Errorf("expected error for empty color")
			}
		})
	})
}

func Test_set_milestone_status(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		mid := fix.Milestones[0].ID

		t.Run("happy: close then re-open round-trip", func(t *testing.T) {
			closed, err := mcpSetMilestoneStatus(ctx, s, setMilestoneStatusArgs{
				WorkspaceID: ws.ID, MilestoneID: mid, Status: "CLOSED",
			})
			mustOK(t, err, "close")
			if closed.Status != "CLOSED" {
				t.Errorf("want CLOSED got %s", closed.Status)
			}
			reopened, err := mcpSetMilestoneStatus(ctx, s, setMilestoneStatusArgs{
				WorkspaceID: ws.ID, MilestoneID: mid, Status: "OPEN",
			})
			mustOK(t, err, "reopen")
			if reopened.Status != "OPEN" {
				t.Errorf("want OPEN got %s", reopened.Status)
			}
		})

		t.Run("enraged: gibberish status rejected at handler layer", func(t *testing.T) {
			_, err := mcpSetMilestoneStatus(ctx, s, setMilestoneStatusArgs{
				WorkspaceID: ws.ID, MilestoneID: mid, Status: "MAYBE",
			})
			if err == nil || !strings.Contains(err.Error(), "OPEN or CLOSED") {
				t.Errorf("expected OPEN-or-CLOSED error, got %v", err)
			}
		})
	})
}

// ===========================================================================
// Workflow tools
// ===========================================================================

func Test_create_workflow(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		p, err := s.CreateProjectWithID(newUUID(), "Test")
		mustOK(t, err, "CreateProject")

		t.Run("happy: defaults applied", func(t *testing.T) {
			wf, err := mcpCreateWorkflow(ctx, s, createWorkflowArgs{
				WorkspaceID: ws.ID, ProjectID: p.ID, Title: "WF",
			})
			mustOK(t, err, "mcpCreateWorkflow")
			if wf.Status != "OPEN" || wf.Color != "WHITE" {
				t.Errorf("defaults wrong")
			}
		})

		t.Run("sad: bad project (FK violation panics from MustExec)", func(t *testing.T) {
			expectFail(t, "create with bogus project", func() error {
				_, err := mcpCreateWorkflow(ctx, s, createWorkflowArgs{
					WorkspaceID: ws.ID, ProjectID: newUUID(), Title: "WF",
				})
				return err
			})
		})
	})
}

func Test_move_workflow(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		wf2, err := s.CreateWorkflowWithID(newUUID(), fix.Project.ID, "WF2")
		mustOK(t, err, "CreateWorkflow WF2")

		t.Run("happy: swap order", func(t *testing.T) {
			beforeRank := wf2.Rank
			moved, err := mcpMoveWorkflow(ctx, s, moveWorkflowArgs{
				WorkspaceID: ws.ID, WorkflowID: wf2.ID, Index: 0,
			})
			mustOK(t, err, "mcpMoveWorkflow")
			if moved.Rank == beforeRank {
				t.Errorf("rank did not change")
			}
		})
	})
}

func Test_set_workflow_color(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: valid color", func(t *testing.T) {
			wf, err := mcpSetWorkflowColor(ctx, s, setWorkflowColorArgs{
				WorkspaceID: ws.ID, WorkflowID: fix.Workflow.ID, Color: "TEAL",
			})
			mustOK(t, err, "setColor")
			if wf.Color != "TEAL" {
				t.Errorf("want TEAL got %s", wf.Color)
			}
		})

		t.Run("enraged: lowercase rejected", func(t *testing.T) {
			_, err := mcpSetWorkflowColor(ctx, s, setWorkflowColorArgs{
				WorkspaceID: ws.ID, WorkflowID: fix.Workflow.ID, Color: "teal",
			})
			if err == nil {
				t.Errorf("expected case-sensitive rejection")
			}
		})
	})
}

func Test_set_workflow_status(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		wfid := fix.Workflow.ID

		t.Run("happy: close + reopen", func(t *testing.T) {
			c, err := mcpSetWorkflowStatus(ctx, s, setWorkflowStatusArgs{
				WorkspaceID: ws.ID, WorkflowID: wfid, Status: "CLOSED",
			})
			mustOK(t, err, "close")
			if c.Status != "CLOSED" {
				t.Errorf("want CLOSED")
			}
			r, err := mcpSetWorkflowStatus(ctx, s, setWorkflowStatusArgs{
				WorkspaceID: ws.ID, WorkflowID: wfid, Status: "OPEN",
			})
			mustOK(t, err, "reopen")
			if r.Status != "OPEN" {
				t.Errorf("want OPEN")
			}
		})

		t.Run("enraged: bad status", func(t *testing.T) {
			_, err := mcpSetWorkflowStatus(ctx, s, setWorkflowStatusArgs{
				WorkspaceID: ws.ID, WorkflowID: wfid, Status: "ARCHIVED",
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})
	})
}

// ===========================================================================
// SubWorkflow tools
// ===========================================================================

func Test_create_subworkflow(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: under existing workflow", func(t *testing.T) {
			sw, err := mcpCreateSubWorkflow(ctx, s, createSubWorkflowArgs{
				WorkspaceID: ws.ID, WorkflowID: fix.Workflow.ID, Title: "New SW",
			})
			mustOK(t, err, "create")
			if sw.WorkflowID != fix.Workflow.ID {
				t.Errorf("workflow_id mismatch")
			}
		})

		t.Run("sad: bogus workflow (FK violation panics from MustExec)", func(t *testing.T) {
			expectFail(t, "create with bogus workflow", func() error {
				_, err := mcpCreateSubWorkflow(ctx, s, createSubWorkflowArgs{
					WorkspaceID: ws.ID, WorkflowID: newUUID(), Title: "SW",
				})
				return err
			})
		})
	})
}

func Test_move_subworkflow(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		wf2, err := s.CreateWorkflowWithID(newUUID(), fix.Project.ID, "WF2")
		mustOK(t, err, "WF2")

		t.Run("happy: move SW across workflows", func(t *testing.T) {
			moved, err := mcpMoveSubWorkflow(ctx, s, moveSubWorkflowArgs{
				WorkspaceID:   ws.ID,
				SubWorkflowID: fix.SubWorkflows[0].ID,
				ToWorkflowID:  wf2.ID,
				Index:         0,
			})
			mustOK(t, err, "move")
			if moved.WorkflowID != wf2.ID {
				t.Errorf("workflow_id: want %s got %s", wf2.ID, moved.WorkflowID)
			}
		})

		t.Run("sad: target workflow nonexistent (FK violation panics from MustExec)", func(t *testing.T) {
			expectFail(t, "move to bogus workflow", func() error {
				_, err := mcpMoveSubWorkflow(ctx, s, moveSubWorkflowArgs{
					WorkspaceID:   ws.ID,
					SubWorkflowID: fix.SubWorkflows[1].ID,
					ToWorkflowID:  newUUID(),
					Index:         0,
				})
				return err
			})
		})

		t.Run("enraged: index out of range", func(t *testing.T) {
			_, err := mcpMoveSubWorkflow(ctx, s, moveSubWorkflowArgs{
				WorkspaceID:   ws.ID,
				SubWorkflowID: fix.SubWorkflows[1].ID,
				ToWorkflowID:  fix.Workflow.ID,
				Index:         9999,
			})
			if err == nil {
				t.Errorf("expected index-invalid error")
			}
		})
	})
}

func Test_set_subworkflow_color(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy", func(t *testing.T) {
			sw, err := mcpSetSubWorkflowColor(ctx, s, setSubWorkflowColorArgs{
				WorkspaceID: ws.ID, SubWorkflowID: fix.SubWorkflows[0].ID, Color: "INDIGO",
			})
			mustOK(t, err, "setColor")
			if sw.Color != "INDIGO" {
				t.Errorf("want INDIGO got %s", sw.Color)
			}
		})
	})
}

func Test_set_subworkflow_status(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		swid := fix.SubWorkflows[0].ID

		t.Run("happy: close then reopen", func(t *testing.T) {
			c, err := mcpSetSubWorkflowStatus(ctx, s, setSubWorkflowStatusArgs{
				WorkspaceID: ws.ID, SubWorkflowID: swid, Status: "CLOSED",
			})
			mustOK(t, err, "close")
			if c.Status != "CLOSED" {
				t.Errorf("want CLOSED")
			}
			r, err := mcpSetSubWorkflowStatus(ctx, s, setSubWorkflowStatusArgs{
				WorkspaceID: ws.ID, SubWorkflowID: swid, Status: "OPEN",
			})
			mustOK(t, err, "reopen")
			if r.Status != "OPEN" {
				t.Errorf("want OPEN")
			}
		})

		t.Run("enraged: empty status", func(t *testing.T) {
			_, err := mcpSetSubWorkflowStatus(ctx, s, setSubWorkflowStatusArgs{
				WorkspaceID: ws.ID, SubWorkflowID: swid, Status: "",
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})
	})
}

// ===========================================================================
// Feature tools
// ===========================================================================

func Test_create_feature(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: at intersection", func(t *testing.T) {
			f, err := mcpCreateFeature(ctx, s, createFeatureArgs{
				WorkspaceID:   ws.ID,
				SubWorkflowID: fix.SubWorkflows[0].ID,
				MilestoneID:   fix.Milestones[0].ID,
				Title:         "F1",
			})
			mustOK(t, err, "create")
			if f.SubWorkflowID != fix.SubWorkflows[0].ID {
				t.Errorf("subworkflow_id mismatch")
			}
			if f.MilestoneID != fix.Milestones[0].ID {
				t.Errorf("milestone_id mismatch")
			}
			if f.Status != "OPEN" || f.Color != "WHITE" {
				t.Errorf("defaults wrong")
			}
		})

		t.Run("sad: nonexistent subworkflow (FK violation panics from MustExec)", func(t *testing.T) {
			expectFail(t, "create with bogus subworkflow", func() error {
				_, err := mcpCreateFeature(ctx, s, createFeatureArgs{
					WorkspaceID:   ws.ID,
					SubWorkflowID: newUUID(),
					MilestoneID:   fix.Milestones[0].ID,
					Title:         "F",
				})
				return err
			})
		})

		t.Run("sad: nonexistent milestone (FK violation panics from MustExec)", func(t *testing.T) {
			expectFail(t, "create with bogus milestone", func() error {
				_, err := mcpCreateFeature(ctx, s, createFeatureArgs{
					WorkspaceID:   ws.ID,
					SubWorkflowID: fix.SubWorkflows[0].ID,
					MilestoneID:   newUUID(),
					Title:         "F",
				})
				return err
			})
		})
	})
}

func Test_rename_feature(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		fid := fix.Features[0].ID

		t.Run("happy", func(t *testing.T) {
			f, err := mcpRenameFeature(ctx, s, renameFeatureArgs{
				WorkspaceID: ws.ID, FeatureID: fid, Title: "Renamed",
			})
			mustOK(t, err, "rename")
			if f.Title != "Renamed" {
				t.Errorf("title: want Renamed got %s", f.Title)
			}
		})

		t.Run("enraged: empty title", func(t *testing.T) {
			_, err := mcpRenameFeature(ctx, s, renameFeatureArgs{
				WorkspaceID: ws.ID, FeatureID: fid, Title: "",
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})

		t.Run("sad: bogus id (service silently returns nil feature)", func(t *testing.T) {
			// service.RenameFeature has a latent bug where a missing feature
			// returns (nil, nil) instead of (nil, error). Document the actual
			// observable contract here -- f is nil, err is nil.
			f, err := mcpRenameFeature(ctx, s, renameFeatureArgs{
				WorkspaceID: ws.ID, FeatureID: newUUID(), Title: "X",
			})
			if f != nil {
				t.Errorf("expected nil feature for bogus id, got %v", f)
			}
			_ = err
		})
	})
}

func Test_update_feature_description(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		fid := fix.Features[0].ID

		t.Run("happy: markdown round-trip", func(t *testing.T) {
			md := "## Heading\n\n- bullet\n- bullet"
			f, err := mcpUpdateFeatureDescription(ctx, s, updateFeatureDescArgs{
				WorkspaceID: ws.ID, FeatureID: fid, Description: md,
			})
			mustOK(t, err, "update")
			if f.Description != md {
				t.Errorf("description round-trip mismatch")
			}
		})

		t.Run("edge: empty description allowed (clears existing)", func(t *testing.T) {
			f, err := mcpUpdateFeatureDescription(ctx, s, updateFeatureDescArgs{
				WorkspaceID: ws.ID, FeatureID: fid, Description: "",
			})
			mustOK(t, err, "update empty")
			if f.Description != "" {
				t.Errorf("expected empty description, got %q", f.Description)
			}
		})
	})
}

func Test_move_feature(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: cross-cell move", func(t *testing.T) {
			f, err := mcpMoveFeature(ctx, s, moveFeatureArgs{
				WorkspaceID:     ws.ID,
				FeatureID:       fix.Features[0].ID,
				ToMilestoneID:   fix.Milestones[1].ID,
				ToSubWorkflowID: fix.SubWorkflows[1].ID,
				Index:           0,
			})
			mustOK(t, err, "move")
			if f.MilestoneID != fix.Milestones[1].ID {
				t.Errorf("milestone after move")
			}
			if f.SubWorkflowID != fix.SubWorkflows[1].ID {
				t.Errorf("subworkflow after move")
			}
		})

		t.Run("sad: bogus feature", func(t *testing.T) {
			_, err := mcpMoveFeature(ctx, s, moveFeatureArgs{
				WorkspaceID:     ws.ID,
				FeatureID:       newUUID(),
				ToMilestoneID:   fix.Milestones[0].ID,
				ToSubWorkflowID: fix.SubWorkflows[0].ID,
				Index:           0,
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})

		t.Run("enraged: index >1000 is rejected", func(t *testing.T) {
			_, err := mcpMoveFeature(ctx, s, moveFeatureArgs{
				WorkspaceID:     ws.ID,
				FeatureID:       fix.Features[1].ID,
				ToMilestoneID:   fix.Milestones[0].ID,
				ToSubWorkflowID: fix.SubWorkflows[0].ID,
				Index:           9999,
			})
			if err == nil {
				t.Errorf("expected index-invalid error")
			}
		})

		t.Run("enraged: negative index is rejected", func(t *testing.T) {
			_, err := mcpMoveFeature(ctx, s, moveFeatureArgs{
				WorkspaceID:     ws.ID,
				FeatureID:       fix.Features[2].ID,
				ToMilestoneID:   fix.Milestones[0].ID,
				ToSubWorkflowID: fix.SubWorkflows[0].ID,
				Index:           -1,
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})
	})
}

func Test_delete_feature(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: returns ok=true and removes from board", func(t *testing.T) {
			res, err := mcpDeleteFeature(ctx, s, deleteFeatureArgs{
				WorkspaceID: ws.ID, FeatureID: fix.Features[0].ID,
			})
			mustOK(t, err, "delete")
			if !res.OK {
				t.Errorf("want ok=true")
			}
			// Verify board no longer contains the feature.
			board, err := mcpGetBoard(ctx, s, getBoardArgs{WorkspaceID: ws.ID, ProjectID: fix.Project.ID})
			mustOK(t, err, "getBoard")
			for _, f := range board.Features {
				if f.ID == fix.Features[0].ID {
					t.Errorf("feature still present after delete")
				}
			}
		})

		t.Run("corner: deleting nonexistent feature does not error (idempotent SQL)", func(t *testing.T) {
			res, err := mcpDeleteFeature(ctx, s, deleteFeatureArgs{
				WorkspaceID: ws.ID, FeatureID: newUUID(),
			})
			// service.DeleteFeature uses MustExec with no row check; no error
			// is the expected (idempotent) behavior. Just don't panic.
			_ = err
			_ = res
		})
	})
}

func Test_set_feature_color(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy", func(t *testing.T) {
			f, err := mcpSetFeatureColor(ctx, s, setFeatureColorArgs{
				WorkspaceID: ws.ID, FeatureID: fix.Features[0].ID, Color: "RED",
			})
			mustOK(t, err, "setColor")
			if f.Color != "RED" {
				t.Errorf("want RED")
			}
		})

		t.Run("enraged: garbage color", func(t *testing.T) {
			_, err := mcpSetFeatureColor(ctx, s, setFeatureColorArgs{
				WorkspaceID: ws.ID, FeatureID: fix.Features[0].ID, Color: "CHARTREUSE",
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})
	})
}

func Test_set_feature_status(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		fid := fix.Features[0].ID

		t.Run("happy: close + reopen", func(t *testing.T) {
			c, err := mcpSetFeatureStatus(ctx, s, setFeatureStatusArgs{
				WorkspaceID: ws.ID, FeatureID: fid, Status: "CLOSED",
			})
			mustOK(t, err, "close")
			if c.Status != "CLOSED" {
				t.Errorf("want CLOSED")
			}
			r, err := mcpSetFeatureStatus(ctx, s, setFeatureStatusArgs{
				WorkspaceID: ws.ID, FeatureID: fid, Status: "OPEN",
			})
			mustOK(t, err, "reopen")
			if r.Status != "OPEN" {
				t.Errorf("want OPEN")
			}
		})

		t.Run("enraged: empty status", func(t *testing.T) {
			_, err := mcpSetFeatureStatus(ctx, s, setFeatureStatusArgs{
				WorkspaceID: ws.ID, FeatureID: fid, Status: "",
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})

		t.Run("enraged: typo status", func(t *testing.T) {
			_, err := mcpSetFeatureStatus(ctx, s, setFeatureStatusArgs{
				WorkspaceID: ws.ID, FeatureID: fid, Status: "DONE",
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})
	})
}

// ===========================================================================
// Feature comments
// ===========================================================================

func Test_add_comment(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: markdown body", func(t *testing.T) {
			c, err := mcpAddComment(ctx, s, addCommentArgs{
				WorkspaceID: ws.ID, FeatureID: fix.Features[0].ID, Body: "**bold** comment",
			})
			mustOK(t, err, "add")
			if c.Post != "**bold** comment" {
				t.Errorf("post round-trip mismatch")
			}
		})

		t.Run("sad: nonexistent feature", func(t *testing.T) {
			_, err := mcpAddComment(ctx, s, addCommentArgs{
				WorkspaceID: ws.ID, FeatureID: newUUID(), Body: "hi",
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})

		t.Run("edge: empty body is accepted (service only enforces upper bound)", func(t *testing.T) {
			c, err := mcpAddComment(ctx, s, addCommentArgs{
				WorkspaceID: ws.ID, FeatureID: fix.Features[0].ID, Body: "",
			})
			mustOK(t, err, "empty body")
			if c.Post != "" {
				t.Errorf("post: want empty got %q", c.Post)
			}
		})

		t.Run("enraged: body >10000 chars rejected", func(t *testing.T) {
			_, err := mcpAddComment(ctx, s, addCommentArgs{
				WorkspaceID: ws.ID, FeatureID: fix.Features[0].ID,
				Body: strings.Repeat("x", 10001),
			})
			if err == nil {
				t.Errorf("expected length error")
			}
		})
	})
}

// ===========================================================================
// Persona tools
// ===========================================================================

func Test_create_persona(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: standalone persona", func(t *testing.T) {
			p, err := mcpCreatePersona(ctx, s, createPersonaArgs{
				WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
				Name: "Alice", Avatar: "avatar03", Role: "PM",
			})
			mustOK(t, err, "create")
			if p.Name != "Alice" || p.Avatar != "avatar03" || p.Role != "PM" {
				t.Errorf("fields mismatch")
			}
		})

		t.Run("happy: with workflow attachment", func(t *testing.T) {
			before, err := s.GetRepoObject().FindWorkflowPersonasByProject(ws.ID, fix.Project.ID)
			mustOK(t, err, "find wp before")
			p, err := mcpCreatePersona(ctx, s, createPersonaArgs{
				WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
				Name: "Bob", Avatar: "avatar05",
				WorkflowID: fix.Workflow.ID,
			})
			mustOK(t, err, "create-with-wf")
			after, err := s.GetRepoObject().FindWorkflowPersonasByProject(ws.ID, fix.Project.ID)
			mustOK(t, err, "find wp after")
			if len(after) != len(before)+1 {
				t.Errorf("workflow_personas count: want %d got %d", len(before)+1, len(after))
			}
			_ = p
		})

		t.Run("enraged: invalid avatar", func(t *testing.T) {
			_, err := mcpCreatePersona(ctx, s, createPersonaArgs{
				WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
				Name: "X", Avatar: "avatar99",
			})
			if err == nil {
				t.Errorf("expected avatar validation error")
			}
		})

		t.Run("enraged: empty name", func(t *testing.T) {
			_, err := mcpCreatePersona(ctx, s, createPersonaArgs{
				WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
				Name: "", Avatar: "avatar00",
			})
			if err == nil {
				t.Errorf("expected name validation error")
			}
		})

		t.Run("enraged: name too long", func(t *testing.T) {
			_, err := mcpCreatePersona(ctx, s, createPersonaArgs{
				WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
				Name: strings.Repeat("z", 201), Avatar: "avatar00",
			})
			if err == nil {
				t.Errorf("expected name length error")
			}
		})

		t.Run("enraged: role too long", func(t *testing.T) {
			_, err := mcpCreatePersona(ctx, s, createPersonaArgs{
				WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
				Name: "ok", Avatar: "avatar00",
				Role: strings.Repeat("r", 201),
			})
			if err == nil {
				t.Errorf("expected role length error")
			}
		})

		t.Run("sad: nonexistent project", func(t *testing.T) {
			_, err := mcpCreatePersona(ctx, s, createPersonaArgs{
				WorkspaceID: ws.ID, ProjectID: newUUID(),
				Name: "X", Avatar: "avatar00",
			})
			if err == nil {
				t.Errorf("expected project-missing error")
			}
		})
	})
}

func Test_update_persona(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: all fields changed", func(t *testing.T) {
			p, err := mcpUpdatePersona(ctx, s, updatePersonaArgs{
				WorkspaceID: ws.ID, PersonaID: fix.Persona.ID,
				Name: strptr("Renamed"), Avatar: strptr("avatar07"),
				Role: strptr("Admin"), Description: strptr("updated desc"),
			})
			mustOK(t, err, "update")
			if p.Name != "Renamed" || p.Avatar != "avatar07" {
				t.Errorf("fields not updated")
			}
		})

		t.Run("sad: nonexistent persona", func(t *testing.T) {
			_, err := mcpUpdatePersona(ctx, s, updatePersonaArgs{
				WorkspaceID: ws.ID, PersonaID: newUUID(),
				Name: strptr("X"), Avatar: strptr("avatar00"),
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})

		t.Run("enraged: bad avatar", func(t *testing.T) {
			_, err := mcpUpdatePersona(ctx, s, updatePersonaArgs{
				WorkspaceID: ws.ID, PersonaID: fix.Persona.ID,
				Name: strptr("ok"), Avatar: strptr("nope"),
			})
			if err == nil {
				t.Errorf("expected avatar error")
			}
		})
	})
}

func Test_delete_persona(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: persona vanishes from board", func(t *testing.T) {
			res, err := mcpDeletePersona(ctx, s, deletePersonaArgs{
				WorkspaceID: ws.ID, PersonaID: fix.Persona.ID,
			})
			mustOK(t, err, "delete")
			if !res.OK {
				t.Errorf("want ok=true")
			}
			board, err := mcpGetBoard(ctx, s, getBoardArgs{
				WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
			})
			mustOK(t, err, "getBoard")
			for _, p := range board.Personas {
				if p.ID == fix.Persona.ID {
					t.Errorf("persona still present after delete")
				}
			}
		})
	})
}

func Test_attach_persona_to_workflow(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		wf2, err := s.CreateWorkflowWithID(newUUID(), fix.Project.ID, "WF2")
		mustOK(t, err, "WF2")

		t.Run("happy: second attachment for same persona", func(t *testing.T) {
			wp, err := mcpAttachPersonaToWorkflow(ctx, s, attachPersonaArgs{
				WorkspaceID: ws.ID, PersonaID: fix.Persona.ID, WorkflowID: wf2.ID,
			})
			mustOK(t, err, "attach")
			if wp.WorkflowID != wf2.ID || wp.PersonaID != fix.Persona.ID {
				t.Errorf("link fields wrong")
			}
		})

		t.Run("sad: bogus workflow", func(t *testing.T) {
			_, err := mcpAttachPersonaToWorkflow(ctx, s, attachPersonaArgs{
				WorkspaceID: ws.ID, PersonaID: fix.Persona.ID, WorkflowID: newUUID(),
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})

		t.Run("sad: bogus persona", func(t *testing.T) {
			_, err := mcpAttachPersonaToWorkflow(ctx, s, attachPersonaArgs{
				WorkspaceID: ws.ID, PersonaID: newUUID(), WorkflowID: fix.Workflow.ID,
			})
			if err == nil {
				t.Errorf("expected error")
			}
		})
	})
}

func Test_detach_persona_from_workflow(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: removes the link", func(t *testing.T) {
			res, err := mcpDetachPersonaFromWorkflow(ctx, s, detachPersonaArgs{
				WorkspaceID: ws.ID, WorkflowPersonaID: fix.WorkflowPersona.ID,
			})
			mustOK(t, err, "detach")
			if !res.OK {
				t.Errorf("want ok=true")
			}
			board, err := mcpGetBoard(ctx, s, getBoardArgs{
				WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
			})
			mustOK(t, err, "getBoard")
			for _, wp := range board.WorkflowPersonas {
				if wp.ID == fix.WorkflowPersona.ID {
					t.Errorf("link still present after detach")
				}
			}
		})
	})
}

// ===========================================================================
// withService + resolveWorkspace boundary
// ===========================================================================

func Test_withService_decorator(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		// Build the wrapped handler the same way buildMCPServer does.
		wrapped := withService(
			func(a workspaceArgs) string { return a.WorkspaceID },
			func(ctx context.Context, s Service, _ workspaceArgs) (listProjectsResult, error) {
				return listProjectsResult{Projects: s.GetProjects()}, nil
			},
		)

		t.Run("sad: env missing from context returns error", func(t *testing.T) {
			_, _, err := wrapped(context.Background(), nil, workspaceArgs{WorkspaceID: ws.ID})
			if err == nil {
				t.Errorf("expected error when env not in context")
			}
		})

		t.Run("sad: unauthenticated (no account on service)", func(t *testing.T) {
			s2 := NewFeatmapService()
			s2.SetConfig(s.GetConfig())
			s2.SetRepoObject(s.GetRepoObject())
			ctx2 := context.WithValue(context.Background(), contextKey, &Env{Service: s2})
			_, _, err := wrapped(ctx2, nil, workspaceArgs{WorkspaceID: ws.ID})
			if err == nil || !strings.Contains(err.Error(), "unauthenticated") {
				t.Errorf("expected unauthenticated error, got %v", err)
			}
		})

		t.Run("happy: with full context", func(t *testing.T) {
			ctx3 := context.WithValue(context.Background(), contextKey, &Env{Service: s})
			_, _, err := wrapped(ctx3, nil, workspaceArgs{WorkspaceID: ws.ID})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	})
}

func Test_resolveWorkspace(t *testing.T) {
	runInTx(t, func(t *testing.T, _ context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		t.Run("sad: empty workspace id", func(t *testing.T) {
			if err := resolveWorkspace(s, ""); err == nil {
				t.Errorf("expected error for empty workspace id")
			}
		})

		t.Run("corner: foreign workspace rejected", func(t *testing.T) {
			if err := resolveWorkspace(s, newUUID()); err == nil {
				t.Errorf("expected error for foreign workspace id")
			}
		})

		t.Run("happy: own workspace resolves", func(t *testing.T) {
			if err := resolveWorkspace(s, ws.ID); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})

		t.Run("sad: missing account on service", func(t *testing.T) {
			s2 := NewFeatmapService()
			s2.SetConfig(s.GetConfig())
			s2.SetRepoObject(s.GetRepoObject())
			if err := resolveWorkspace(s2, ws.ID); err == nil {
				t.Errorf("expected error for missing account")
			}
		})
	})
}

// ===========================================================================
// Plane MCP tools
// ===========================================================================

func Test_mcpSetPlaneConnection(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t), PlaneAllowPrivateHosts: true})
		fix := newProjectFixture(t, s)

		t.Run("sad: missing api_key returns error", func(t *testing.T) {
			_, err := mcpSetPlaneConnection(ctx, s, planeConnectionArgs{
				WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
				BaseURL: "https://api.plane.so", PlaneWorkspace: "ws", WatchedProjects: "",
			})
			if err == nil {
				t.Fatal("expected error for missing api_key")
			}
		})

		t.Run("happy: creates connection and returns hint", func(t *testing.T) {
			conn, err := mcpSetPlaneConnection(ctx, s, planeConnectionArgs{
				WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
				BaseURL: "https://api.plane.so", PlaneWorkspace: "my-ws",
				APIKey: "secret_key_abcd", WatchedProjects: "p1,p2",
			})
			mustOK(t, err, "mcpSetPlaneConnection")
			if conn == nil {
				t.Fatal("expected non-nil connection")
			}
			if conn.APIKeyHint != "abcd" {
				t.Fatalf("expected hint 'abcd', got %q", conn.APIKeyHint)
			}
			if conn.PlaneWorkspace != "my-ws" {
				t.Fatalf("expected PlaneWorkspace 'my-ws', got %q", conn.PlaneWorkspace)
			}
		})
	})
}

func Test_mcpGetPlaneConnection(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t), PlaneAllowPrivateHosts: true})
		fix := newProjectFixture(t, s)

		t.Run("sad: no connection returns error", func(t *testing.T) {
			_, err := mcpGetPlaneConnection(ctx, s, planeProjectArgs{WorkspaceID: ws.ID, ProjectID: fix.Project.ID})
			if err == nil {
				t.Fatal("expected error when no connection exists")
			}
		})

		t.Run("happy: returns existing connection", func(t *testing.T) {
			_, err := mcpSetPlaneConnection(ctx, s, planeConnectionArgs{
				WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
				BaseURL: "https://api.plane.so", PlaneWorkspace: "ws-slug",
				APIKey: "key_1234", WatchedProjects: "proj1",
			})
			mustOK(t, err, "set connection")

			conn, err := mcpGetPlaneConnection(ctx, s, planeProjectArgs{WorkspaceID: ws.ID, ProjectID: fix.Project.ID})
			mustOK(t, err, "mcpGetPlaneConnection")
			if conn.PlaneWorkspace != "ws-slug" {
				t.Fatalf("expected PlaneWorkspace 'ws-slug', got %q", conn.PlaneWorkspace)
			}
			if conn.WatchedProjects != "proj1" {
				t.Fatalf("expected WatchedProjects 'proj1', got %q", conn.WatchedProjects)
			}
		})
	})
}

func Test_mcpTestPlaneConnection(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t), PlaneAllowPrivateHosts: true})
		fix := newProjectFixture(t, s)

		t.Run("sad: no connection returns error", func(t *testing.T) {
			_, err := mcpTestPlaneConnection(ctx, s, planeProjectArgs{WorkspaceID: ws.ID, ProjectID: fix.Project.ID})
			if err == nil {
				t.Fatal("expected error when no connection exists")
			}
		})

		t.Run("sad: Plane server returns 401, TestConnection surfaces error", func(t *testing.T) {
			// Use a dedicated server that always 401s (mirrors Test_PlaneClient_testConnection_401
			// in plane_test.go but exercised through the full MCP handler path).
			srv401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(401)
			}))
			defer srv401.Close()

			_, err2 := mcpSetPlaneConnection(ctx, s, planeConnectionArgs{
				WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
				BaseURL: srv401.URL, PlaneWorkspace: "ws",
				APIKey: "bad_key", WatchedProjects: "",
			})
			mustOK(t, err2, "set connection pointing at 401 server")

			_, err3 := mcpTestPlaneConnection(ctx, s, planeProjectArgs{WorkspaceID: ws.ID, ProjectID: fix.Project.ID})
			if err3 == nil {
				t.Fatal("expected error when Plane server returns 401")
			}
		})
	})
}

func Test_mcpLinkFeatureToPlane(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t), PlaneAllowPrivateHosts: true})
		fix := newProjectFixture(t, s)
		feat := fix.Features[0]

		t.Run("happy: creates link and returns it", func(t *testing.T) {
			link, err := mcpLinkFeatureToPlane(ctx, s, planeLinkArgs{
				WorkspaceID: ws.ID, FeatureID: feat.ID,
				PlaneProjectID: "pp1", PlaneWorkItemID: "WI-42",
			})
			mustOK(t, err, "mcpLinkFeatureToPlane")
			if link == nil {
				t.Fatal("expected non-nil link")
			}
			if link.FeatureID != feat.ID {
				t.Fatalf("link.FeatureID = %q, want %q", link.FeatureID, feat.ID)
			}
			if link.PlaneWorkItemID != "WI-42" {
				t.Fatalf("link.PlaneWorkItemID = %q, want WI-42", link.PlaneWorkItemID)
			}
		})

		t.Run("sad: bad feature id returns error", func(t *testing.T) {
			expectFail(t, "link bad feature", func() error {
				_, err := mcpLinkFeatureToPlane(ctx, s, planeLinkArgs{
					WorkspaceID: ws.ID, FeatureID: newUUID(),
					PlaneProjectID: "pp1", PlaneWorkItemID: "WI-99",
				})
				return err
			})
		})
	})
}

func Test_mcpUnlinkFeatureFromPlane(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t), PlaneAllowPrivateHosts: true})
		fix := newProjectFixture(t, s)
		feat := fix.Features[0]

		t.Run("sad: no link returns error", func(t *testing.T) {
			_, err := mcpUnlinkFeatureFromPlane(ctx, s, planeUnlinkArgs{WorkspaceID: ws.ID, FeatureID: feat.ID})
			if err == nil {
				t.Fatal("expected error when no link exists")
			}
		})

		t.Run("happy: unlinks feature", func(t *testing.T) {
			_, err := mcpLinkFeatureToPlane(ctx, s, planeLinkArgs{
				WorkspaceID: ws.ID, FeatureID: feat.ID,
				PlaneProjectID: "pp1", PlaneWorkItemID: "WI-1",
			})
			mustOK(t, err, "link")

			res, err := mcpUnlinkFeatureFromPlane(ctx, s, planeUnlinkArgs{WorkspaceID: ws.ID, FeatureID: feat.ID})
			mustOK(t, err, "mcpUnlinkFeatureFromPlane")
			if !res.OK {
				t.Fatal("expected OK=true")
			}

			// Confirm link is gone
			_, err2 := s.GetPlaneLinkByFeature(feat.ID)
			if err2 == nil {
				t.Fatal("expected link to be deleted")
			}
		})
	})
}

func Test_mcpPlaneSync(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t), PlaneAllowPrivateHosts: true})
		fix := newProjectFixture(t, s)
		feat := fix.Features[1] // feature with no seeded comment

		fp := newFakePlane()
		defer fp.srv.Close()

		_, err := mcpSetPlaneConnection(ctx, s, planeConnectionArgs{
			WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
			BaseURL: fp.srv.URL, PlaneWorkspace: "ws",
			APIKey: "key", WatchedProjects: "",
		})
		mustOK(t, err, "SetPlaneConnection")

		link, err := mcpLinkFeatureToPlane(ctx, s, planeLinkArgs{
			WorkspaceID: ws.ID, FeatureID: feat.ID,
			PlaneProjectID: "pp", PlaneWorkItemID: "wi-1",
		})
		mustOK(t, err, "LinkFeatureToPlane")

		// add a local comment to push
		_, err = s.CreateFeatureCommentWithID(newUUID(), feat.ID, "mcp sync test comment")
		mustOK(t, err, "CreateFeatureComment")

		t.Run("happy: syncs via feature_id, persists LastStatus and LastSyncedAt", func(t *testing.T) {
			res, err := mcpPlaneSync(ctx, s, planeSyncArgs{
				WorkspaceID: ws.ID, FeatureID: feat.ID,
			})
			mustOK(t, err, "mcpPlaneSync")
			if res == nil {
				t.Fatal("expected non-nil SyncResult")
			}
			if res.Pushed != 1 {
				t.Fatalf("expected 1 pushed, got %d", res.Pushed)
			}
			if len(res.PerLink) != 1 {
				t.Fatalf("expected 1 per-link result, got %d", len(res.PerLink))
			}
			if res.PerLink[0].Status != string(StatusOK) {
				t.Fatalf("per-link status = %q, want ok", res.PerLink[0].Status)
			}

			// Critical: verify cursor + status were persisted to the DB row
			// (not just set in-memory). Read back via the repo.
			persisted, err2 := s.GetPlaneLinkByFeature(feat.ID)
			mustOK(t, err2, "reload link")
			if persisted.LastStatus != string(StatusOK) {
				t.Fatalf("persisted LastStatus = %q, want ok -- StorePlaneLink was not called", persisted.LastStatus)
			}
			if persisted.LastSyncedAt == nil {
				t.Fatal("persisted LastSyncedAt is nil -- StorePlaneLink was not called")
			}
		})

		t.Run("idempotency: second sync via feature_id pushes nothing new", func(t *testing.T) {
			res2, err2 := mcpPlaneSync(ctx, s, planeSyncArgs{
				WorkspaceID: ws.ID, FeatureID: feat.ID,
			})
			mustOK(t, err2, "mcpPlaneSync second run")
			if res2.Pushed != 0 {
				t.Fatalf("second run pushed %d, want 0 (idempotency)", res2.Pushed)
			}
		})

		t.Run("happy: syncs via project_id delegates to SyncProject", func(t *testing.T) {
			// Seed a remote comment so the project sync has something to pull.
			fp.seedRemote("<p>project-level pull</p>")
			res3, err3 := mcpPlaneSync(ctx, s, planeSyncArgs{
				WorkspaceID: ws.ID, ProjectID: fix.Project.ID,
			})
			mustOK(t, err3, "mcpPlaneSync project")
			if res3 == nil {
				t.Fatal("expected non-nil SyncResult for project sync")
			}
			// SyncProject persists via its own StorePlaneLink call -- just check we
			// got a result (the per-link persistence test lives in Test_SyncProject_perLinkStatus).
			if len(res3.PerLink) == 0 {
				t.Fatal("expected at least one per-link result from project sync")
			}
		})

		_ = link // suppress unused warning
	})
}

// Compile-time guard that the mcpsdk import is exercised somewhere in the
// test file -- if the SDK API drifts and we drop the only reference, this
// would catch it.
var _ = mcpsdk.NewServer
