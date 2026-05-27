package main

// Tests for the R2 bulk + partial structural tools (mcp_bulk_structural.go).
// Same harness as mcp_bulk_test.go: per-test tx on a real postgres, rolled back
// at the end. strptr/intptr live in mcp_bulk_test.go.

import (
	"context"
	"testing"
)

// ===========================================================================
// PART-011: update_milestone / update_workflow / update_subworkflow
// ===========================================================================

func Test_update_milestone(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		mid := fix.Milestones[0].ID

		t.Run("happy: only color changes; title preserved", func(t *testing.T) {
			_, err := s.RenameMilestone(mid, "Original MS")
			mustOK(t, err, "seed rename")

			m, err := mcpUpdateMilestone(ctx, s, updateMilestoneArgs{
				WorkspaceID: ws.ID, MilestoneID: mid, Color: strptr("BLUE"),
			})
			mustOK(t, err, "update color only")
			if m.Color != "BLUE" {
				t.Errorf("color: want BLUE got %s", m.Color)
			}
			if m.Title != "Original MS" {
				t.Errorf("title clobbered: got %q", m.Title)
			}
		})

		t.Run("happy: multi (title+status)", func(t *testing.T) {
			m, err := mcpUpdateMilestone(ctx, s, updateMilestoneArgs{
				WorkspaceID: ws.ID, MilestoneID: mid,
				Title: strptr("New MS"), Status: strptr("CLOSED"),
			})
			mustOK(t, err, "multi update")
			if m.Title != "New MS" || m.Status != "CLOSED" {
				t.Errorf("multi update wrong: %q %q", m.Title, m.Status)
			}
		})

		t.Run("happy: index move reorders", func(t *testing.T) {
			m, err := mcpUpdateMilestone(ctx, s, updateMilestoneArgs{
				WorkspaceID: ws.ID, MilestoneID: fix.Milestones[1].ID, Index: intptr(0),
			})
			mustOK(t, err, "index move")
			if m == nil {
				t.Fatalf("nil milestone")
			}
		})

		t.Run("edge: no fields -> current unchanged", func(t *testing.T) {
			m, err := mcpUpdateMilestone(ctx, s, updateMilestoneArgs{WorkspaceID: ws.ID, MilestoneID: mid})
			mustOK(t, err, "noop")
			if m == nil || m.ID != mid {
				t.Errorf("expected current milestone")
			}
		})

		t.Run("enraged: invalid color rejected before write", func(t *testing.T) {
			_, err := mcpUpdateMilestone(ctx, s, updateMilestoneArgs{
				WorkspaceID: ws.ID, MilestoneID: mid, Color: strptr("CHARTREUSE"),
			})
			if err == nil {
				t.Errorf("expected invalid-color error")
			}
		})

		t.Run("enraged: invalid status rejected", func(t *testing.T) {
			_, err := mcpUpdateMilestone(ctx, s, updateMilestoneArgs{
				WorkspaceID: ws.ID, MilestoneID: mid, Status: strptr("DONE"),
			})
			if err == nil {
				t.Errorf("expected invalid-status error")
			}
		})

		t.Run("sad: bogus id -> not found", func(t *testing.T) {
			_, err := mcpUpdateMilestone(ctx, s, updateMilestoneArgs{
				WorkspaceID: ws.ID, MilestoneID: newUUID(), Title: strptr("X"),
			})
			if err == nil {
				t.Errorf("expected not-found error")
			}
		})
	})
}

func Test_update_workflow(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		wid := fix.Workflow.ID

		t.Run("happy: only color changes; title preserved", func(t *testing.T) {
			_, err := s.RenameWorkflow(wid, "Original WF")
			mustOK(t, err, "seed rename")
			w, err := mcpUpdateWorkflow(ctx, s, updateWorkflowArgs{
				WorkspaceID: ws.ID, WorkflowID: wid, Color: strptr("GREEN"),
			})
			mustOK(t, err, "update color only")
			if w.Color != "GREEN" {
				t.Errorf("color: want GREEN got %s", w.Color)
			}
			if w.Title != "Original WF" {
				t.Errorf("title clobbered: got %q", w.Title)
			}
		})

		t.Run("enraged: invalid status rejected", func(t *testing.T) {
			_, err := mcpUpdateWorkflow(ctx, s, updateWorkflowArgs{
				WorkspaceID: ws.ID, WorkflowID: wid, Status: strptr("NOPE"),
			})
			if err == nil {
				t.Errorf("expected invalid-status error")
			}
		})

		t.Run("sad: bogus id -> not found", func(t *testing.T) {
			_, err := mcpUpdateWorkflow(ctx, s, updateWorkflowArgs{
				WorkspaceID: ws.ID, WorkflowID: newUUID(), Title: strptr("X"),
			})
			if err == nil {
				t.Errorf("expected not-found error")
			}
		})
	})
}

func Test_update_subworkflow(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		swid := fix.SubWorkflows[0].ID

		t.Run("happy: only color changes; title preserved", func(t *testing.T) {
			_, err := s.RenameSubWorkflow(swid, "Original SW")
			mustOK(t, err, "seed rename")
			sw, err := mcpUpdateSubWorkflow(ctx, s, updateSubWorkflowArgs{
				WorkspaceID: ws.ID, SubWorkflowID: swid, Color: strptr("RED"),
			})
			mustOK(t, err, "update color only")
			if sw.Color != "RED" {
				t.Errorf("color: want RED got %s", sw.Color)
			}
			if sw.Title != "Original SW" {
				t.Errorf("title clobbered: got %q", sw.Title)
			}
		})

		t.Run("happy: move within same workflow (omit to_workflow_id, give index)", func(t *testing.T) {
			sw, err := mcpUpdateSubWorkflow(ctx, s, updateSubWorkflowArgs{
				WorkspaceID: ws.ID, SubWorkflowID: fix.SubWorkflows[2].ID, Index: intptr(0),
			})
			mustOK(t, err, "index move")
			if sw.WorkflowID != fix.Workflow.ID {
				t.Errorf("workflow changed unexpectedly")
			}
		})

		t.Run("enraged: bad to_workflow_id rejected before write", func(t *testing.T) {
			_, err := mcpUpdateSubWorkflow(ctx, s, updateSubWorkflowArgs{
				WorkspaceID: ws.ID, SubWorkflowID: swid, ToWorkflowID: strptr(newUUID()),
			})
			if err == nil {
				t.Errorf("expected workflow-not-found error")
			}
		})

		t.Run("sad: bogus id -> not found", func(t *testing.T) {
			_, err := mcpUpdateSubWorkflow(ctx, s, updateSubWorkflowArgs{
				WorkspaceID: ws.ID, SubWorkflowID: newUUID(), Title: strptr("X"),
			})
			if err == nil {
				t.Errorf("expected not-found error")
			}
		})
	})
}

// ===========================================================================
// PART-012: update_persona partial-safety (omitted fields preserved)
// ===========================================================================

func Test_update_persona_partial(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		pid := fix.Persona.ID

		// Seed known role + description so we can prove they survive.
		_, err := s.UpdatePersona(pid, "avatar00", "Seed Name", "Seed Role", "Seed Desc")
		mustOK(t, err, "seed persona")

		t.Run("happy: only name changes; role/description/avatar preserved", func(t *testing.T) {
			p, err := mcpUpdatePersona(ctx, s, updatePersonaArgs{
				WorkspaceID: ws.ID, PersonaID: pid, Name: strptr("Just Name"),
			})
			mustOK(t, err, "partial update")
			if p.Name != "Just Name" {
				t.Errorf("name not updated: %q", p.Name)
			}
			if p.Role != "Seed Role" {
				t.Errorf("role clobbered: %q", p.Role)
			}
			if p.Description != "Seed Desc" {
				t.Errorf("description clobbered: %q", p.Description)
			}
			if p.Avatar != "avatar00" {
				t.Errorf("avatar clobbered: %q", p.Avatar)
			}
		})

		t.Run("enraged: invalid avatar rejected before write", func(t *testing.T) {
			_, err := mcpUpdatePersona(ctx, s, updatePersonaArgs{
				WorkspaceID: ws.ID, PersonaID: pid, Avatar: strptr("nope"),
			})
			if err == nil {
				t.Errorf("expected avatar error")
			}
		})

		t.Run("sad: bogus id -> not found", func(t *testing.T) {
			_, err := mcpUpdatePersona(ctx, s, updatePersonaArgs{
				WorkspaceID: ws.ID, PersonaID: newUUID(), Name: strptr("X"),
			})
			if err == nil {
				t.Errorf("expected not-found error")
			}
		})
	})
}

// ===========================================================================
// BULK-012: bulk_add_comment
// ===========================================================================

func Test_bulk_add_comment(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: each result carries a comment id", func(t *testing.T) {
			items := []bulkAddCommentItem{
				{FeatureID: fix.Features[0].ID, Body: "c1"},
				{FeatureID: fix.Features[1].ID, Body: "c2"},
			}
			res, err := mcpBulkAddComment(ctx, s, bulkAddCommentArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk add comment")
			for i, r := range res.Results {
				if !r.OK || r.ID == "" {
					t.Errorf("item %d: want ok+id got %+v", i, r)
				}
			}
		})

		t.Run("mixed: bad feature id fails its slot, others persist", func(t *testing.T) {
			items := []bulkAddCommentItem{
				{FeatureID: fix.Features[2].ID, Body: "good"},
				{FeatureID: newUUID(), Body: "orphan"},
				{FeatureID: fix.Features[3].ID, Body: "good2"},
			}
			res, err := mcpBulkAddComment(ctx, s, bulkAddCommentArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk add comment mixed")
			if !res.Results[0].OK || res.Results[1].OK || !res.Results[2].OK {
				t.Fatalf("want [ok,err,ok] got %+v", res.Results)
			}
			board, err := mcpGetBoard(ctx, s, getBoardArgs{WorkspaceID: ws.ID, ProjectID: fix.Project.ID})
			mustOK(t, err, "getBoard after mixed")
			ids := map[string]bool{res.Results[0].ID: true, res.Results[2].ID: true}
			got := 0
			for _, c := range board.FeatureComments {
				if ids[c.ID] {
					got++
				}
			}
			if got != 2 {
				t.Errorf("expected 2 good comments persisted, found %d", got)
			}
		})

		t.Run("guardrail: empty batch rejected", func(t *testing.T) {
			_, err := mcpBulkAddComment(ctx, s, bulkAddCommentArgs{WorkspaceID: ws.ID, Items: nil})
			if err == nil {
				t.Errorf("expected empty-batch error")
			}
		})

		t.Run("guardrail: oversized batch rejected", func(t *testing.T) {
			items := make([]bulkAddCommentItem, maxBulkItems+1)
			for i := range items {
				items[i] = bulkAddCommentItem{FeatureID: fix.Features[0].ID, Body: "x"}
			}
			_, err := mcpBulkAddComment(ctx, s, bulkAddCommentArgs{WorkspaceID: ws.ID, Items: items})
			if err == nil {
				t.Errorf("expected oversized-batch error")
			}
		})
	})
}

// ===========================================================================
// BULK-013: bulk structural creates
// ===========================================================================

func Test_bulk_create_structural(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("milestones: mixed bad project fails slot, others persist", func(t *testing.T) {
			items := []bulkCreateMilestoneItem{
				{ProjectID: fix.Project.ID, Title: "MS-A"},
				{ProjectID: newUUID(), Title: "MS-bad"},
				{ProjectID: fix.Project.ID, Title: "MS-B"},
			}
			res, err := mcpBulkCreateMilestones(ctx, s, bulkCreateMilestonesArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk create milestones")
			if !res.Results[0].OK || res.Results[1].OK || !res.Results[2].OK {
				t.Fatalf("want [ok,err,ok] got %+v", res.Results)
			}
			board, err := mcpGetBoard(ctx, s, getBoardArgs{WorkspaceID: ws.ID, ProjectID: fix.Project.ID})
			mustOK(t, err, "getBoard")
			ids := map[string]bool{res.Results[0].ID: true, res.Results[2].ID: true}
			got := 0
			for _, m := range board.Milestones {
				if ids[m.ID] {
					got++
				}
			}
			if got != 2 {
				t.Errorf("expected 2 good milestones persisted, found %d", got)
			}
		})

		t.Run("workflows: happy", func(t *testing.T) {
			items := []bulkCreateWorkflowItem{
				{ProjectID: fix.Project.ID, Title: "WF-A"},
				{ProjectID: fix.Project.ID, Title: "WF-B"},
			}
			res, err := mcpBulkCreateWorkflows(ctx, s, bulkCreateWorkflowsArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk create workflows")
			for i, r := range res.Results {
				if !r.OK || r.ID == "" {
					t.Errorf("item %d not ok: %+v", i, r)
				}
			}
		})

		t.Run("subworkflows: mixed bad workflow fails slot", func(t *testing.T) {
			items := []bulkCreateSubWorkflowItem{
				{WorkflowID: fix.Workflow.ID, Title: "SW-A"},
				{WorkflowID: newUUID(), Title: "SW-bad"},
			}
			res, err := mcpBulkCreateSubWorkflows(ctx, s, bulkCreateSubWorkflowsArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk create subworkflows")
			if !res.Results[0].OK || res.Results[1].OK {
				t.Errorf("want [ok,err] got %+v", res.Results)
			}
		})

		t.Run("personas: mixed bad avatar fails slot", func(t *testing.T) {
			items := []bulkCreatePersonaItem{
				{ProjectID: fix.Project.ID, Name: "P-A", Avatar: "avatar01", Role: "user"},
				{ProjectID: fix.Project.ID, Name: "P-bad", Avatar: "nope"},
				{ProjectID: fix.Project.ID, Name: "P-B", Avatar: "avatar02"},
			}
			res, err := mcpBulkCreatePersonas(ctx, s, bulkCreatePersonasArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk create personas")
			if !res.Results[0].OK || res.Results[1].OK || !res.Results[2].OK {
				t.Fatalf("want [ok,err,ok] got %+v", res.Results)
			}
			board, err := mcpGetBoard(ctx, s, getBoardArgs{WorkspaceID: ws.ID, ProjectID: fix.Project.ID})
			mustOK(t, err, "getBoard")
			ids := map[string]bool{res.Results[0].ID: true, res.Results[2].ID: true}
			got := 0
			for _, p := range board.Personas {
				if ids[p.ID] {
					got++
				}
			}
			if got != 2 {
				t.Errorf("expected 2 good personas persisted, found %d", got)
			}
		})

		t.Run("guardrail: empty + oversized rejected", func(t *testing.T) {
			_, err := mcpBulkCreateMilestones(ctx, s, bulkCreateMilestonesArgs{WorkspaceID: ws.ID, Items: nil})
			if err == nil {
				t.Errorf("expected empty-batch error")
			}
			big := make([]bulkCreateMilestoneItem, maxBulkItems+1)
			for i := range big {
				big[i] = bulkCreateMilestoneItem{ProjectID: fix.Project.ID, Title: "x"}
			}
			_, err = mcpBulkCreateMilestones(ctx, s, bulkCreateMilestonesArgs{WorkspaceID: ws.ID, Items: big})
			if err == nil {
				t.Errorf("expected oversized-batch error")
			}
		})
	})
}

// ===========================================================================
// BULK-014: bulk attach / detach personas
// ===========================================================================

func Test_bulk_attach_detach_personas(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		// A second persona so we can attach without colliding with the fixture's
		// pre-attached one.
		p2, err := s.CreatePersonaWithID(newUUID(), fix.Project.ID, "avatar03", "Persona Two", "", "", "", "")
		mustOK(t, err, "create p2")

		t.Run("attach: mixed bad persona id fails slot", func(t *testing.T) {
			items := []bulkAttachPersonaItem{
				{PersonaID: p2.ID, WorkflowID: fix.Workflow.ID},
				{PersonaID: newUUID(), WorkflowID: fix.Workflow.ID},
			}
			res, err := mcpBulkAttachPersonas(ctx, s, bulkAttachPersonasArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk attach")
			if !res.Results[0].OK || res.Results[1].OK {
				t.Fatalf("want [ok,err] got %+v", res.Results)
			}
			if res.Results[0].ID == "" {
				t.Errorf("expected workflow-persona link id")
			}
		})

		t.Run("detach: happy + bogus id fails slot", func(t *testing.T) {
			// fixture's WorkflowPersona is a real link we can detach.
			items := []bulkDetachPersonaItem{
				{WorkflowPersonaID: fix.WorkflowPersona.ID},
				{WorkflowPersonaID: newUUID()},
			}
			res, err := mcpBulkDetachPersonas(ctx, s, bulkDetachPersonasArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk detach")
			if !res.Results[0].OK || res.Results[1].OK {
				t.Fatalf("want [ok,err] got %+v", res.Results)
			}
		})

		t.Run("guardrail: empty batch rejected", func(t *testing.T) {
			_, err := mcpBulkAttachPersonas(ctx, s, bulkAttachPersonasArgs{WorkspaceID: ws.ID, Items: nil})
			if err == nil {
				t.Errorf("expected empty-batch error")
			}
		})
	})
}

// ===========================================================================
// BULK-015: bulk partial structural updates
// ===========================================================================

func Test_bulk_update_structural(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("milestones: mixed bad id, good items persist", func(t *testing.T) {
			_, err := s.RenameMilestone(fix.Milestones[0].ID, "Keep")
			mustOK(t, err, "seed")
			items := []bulkUpdateMilestoneItem{
				{MilestoneID: fix.Milestones[0].ID, Color: strptr("BLUE")},
				{MilestoneID: newUUID(), Color: strptr("RED")},
				{MilestoneID: fix.Milestones[1].ID, Title: strptr("Renamed-MS")},
			}
			res, err := mcpBulkUpdateMilestones(ctx, s, bulkUpdateMilestonesArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk update milestones")
			if !res.Results[0].OK || res.Results[1].OK || !res.Results[2].OK {
				t.Fatalf("want [ok,err,ok] got %+v", res.Results)
			}
			m0, err := s.GetRepoObject().GetMilestone(ws.ID, fix.Milestones[0].ID)
			mustOK(t, err, "refetch m0")
			if m0.Color != "BLUE" || m0.Title != "Keep" {
				t.Errorf("partial update wrong / not persisted: color=%q title=%q", m0.Color, m0.Title)
			}
		})

		t.Run("workflows: invalid color fails slot only", func(t *testing.T) {
			items := []bulkUpdateWorkflowItem{
				{WorkflowID: fix.Workflow.ID, Color: strptr("NOTACOLOR")},
				{WorkflowID: fix.Workflow.ID, Status: strptr("CLOSED")},
			}
			res, err := mcpBulkUpdateWorkflows(ctx, s, bulkUpdateWorkflowsArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk update workflows")
			if res.Results[0].OK || !res.Results[1].OK {
				t.Errorf("want [err,ok] got %+v", res.Results)
			}
		})

		t.Run("subworkflows: bad to_workflow_id fails slot, good persist", func(t *testing.T) {
			items := []bulkUpdateSubWorkflowItem{
				{SubWorkflowID: fix.SubWorkflows[0].ID, Title: strptr("SW-keep")},
				{SubWorkflowID: fix.SubWorkflows[1].ID, ToWorkflowID: strptr(newUUID())},
			}
			res, err := mcpBulkUpdateSubWorkflows(ctx, s, bulkUpdateSubWorkflowsArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk update subworkflows")
			if !res.Results[0].OK || res.Results[1].OK {
				t.Fatalf("want [ok,err] got %+v", res.Results)
			}
			sw0, err := s.GetRepoObject().GetSubWorkflow(ws.ID, fix.SubWorkflows[0].ID)
			mustOK(t, err, "refetch sw0")
			if sw0.Title != "SW-keep" {
				t.Errorf("good subworkflow update not persisted: %q", sw0.Title)
			}
		})

		t.Run("guardrail: oversized rejected", func(t *testing.T) {
			big := make([]bulkUpdateMilestoneItem, maxBulkItems+1)
			for i := range big {
				big[i] = bulkUpdateMilestoneItem{MilestoneID: fix.Milestones[0].ID, Color: strptr("RED")}
			}
			_, err := mcpBulkUpdateMilestones(ctx, s, bulkUpdateMilestonesArgs{WorkspaceID: ws.ID, Items: big})
			if err == nil {
				t.Errorf("expected oversized-batch error")
			}
		})
	})
}

// ===========================================================================
// BULK-016: bulk_reorder_features
// ===========================================================================

func Test_bulk_reorder_features(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		mid := fix.Milestones[0].ID
		swid := fix.SubWorkflows[0].ID

		// Cell M1/SW1 starts with one fixture feature (features[0]). Add two more
		// so we have a non-trivial cell to reorder.
		f1 := fix.Features[0].ID
		f2, err := s.CreateFeatureWithID(newUUID(), swid, mid, "R2")
		mustOK(t, err, "create f2")
		f3, err := s.CreateFeatureWithID(newUUID(), swid, mid, "R3")
		mustOK(t, err, "create f3")

		t.Run("happy: final rank order matches requested order", func(t *testing.T) {
			want := []string{f3.ID, f1, f2.ID} // deliberately not creation order
			res, err := mcpBulkReorderFeatures(ctx, s, bulkReorderFeaturesArgs{
				WorkspaceID: ws.ID, MilestoneID: mid, SubWorkflowID: swid, FeatureIDs: want,
			})
			mustOK(t, err, "reorder")
			if len(res.Features) != 3 {
				t.Fatalf("want 3 features got %d", len(res.Features))
			}

			// Re-read the cell ordered by rank; order must equal the request.
			cell, err := s.GetRepoObject().FindFeaturesByMilestoneAndSubWorkflow(ws.ID, mid, swid)
			mustOK(t, err, "refetch cell")
			if len(cell) != 3 {
				t.Fatalf("want 3 in cell got %d", len(cell))
			}
			for i := range want {
				if cell[i].ID != want[i] {
					t.Errorf("position %d: want %s got %s", i, want[i], cell[i].ID)
				}
			}
			// Ranks must be strictly increasing in requested order.
			for i := 1; i < len(cell); i++ {
				if !(cell[i-1].Rank < cell[i].Rank) {
					t.Errorf("ranks not strictly increasing: %q !< %q", cell[i-1].Rank, cell[i].Rank)
				}
			}
		})

		t.Run("sad: subset (valid members but incomplete) rejects, no writes", func(t *testing.T) {
			before, err := s.GetRepoObject().FindFeaturesByMilestoneAndSubWorkflow(ws.ID, mid, swid)
			mustOK(t, err, "snapshot before")
			orderBefore := make([]string, len(before))
			ranksBefore := map[string]string{}
			for i, f := range before {
				orderBefore[i] = f.ID
				ranksBefore[f.ID] = f.Rank
			}
			// Cell has 3 features; list only 2 valid members -> must reject.
			_, err = mcpBulkReorderFeatures(ctx, s, bulkReorderFeaturesArgs{
				WorkspaceID: ws.ID, MilestoneID: mid, SubWorkflowID: swid,
				FeatureIDs: []string{f3.ID, f1},
			})
			if err == nil {
				t.Errorf("expected full-set-required error for subset")
			}
			after, err := s.GetRepoObject().FindFeaturesByMilestoneAndSubWorkflow(ws.ID, mid, swid)
			mustOK(t, err, "snapshot after")
			if len(after) != len(orderBefore) {
				t.Fatalf("cell size changed")
			}
			for i, f := range after {
				if f.ID != orderBefore[i] || f.Rank != ranksBefore[f.ID] {
					t.Errorf("cell mutated despite subset rejection at pos %d", i)
				}
			}
		})

		t.Run("sad: id not in cell rejects whole call, no writes", func(t *testing.T) {
			// features[3] is in M2/SW1, not this cell.
			before, err := s.GetRepoObject().FindFeaturesByMilestoneAndSubWorkflow(ws.ID, mid, swid)
			mustOK(t, err, "snapshot before")
			ranksBefore := map[string]string{}
			for _, f := range before {
				ranksBefore[f.ID] = f.Rank
			}
			// Count matches the cell (3) but one id (features[3], in M2/SW1) is
			// not a member -> membership check must reject.
			_, err = mcpBulkReorderFeatures(ctx, s, bulkReorderFeaturesArgs{
				WorkspaceID: ws.ID, MilestoneID: mid, SubWorkflowID: swid,
				FeatureIDs: []string{f1, f2.ID, fix.Features[3].ID},
			})
			if err == nil {
				t.Errorf("expected not-in-cell error")
			}
			after, err := s.GetRepoObject().FindFeaturesByMilestoneAndSubWorkflow(ws.ID, mid, swid)
			mustOK(t, err, "snapshot after")
			for _, f := range after {
				if ranksBefore[f.ID] != f.Rank {
					t.Errorf("rank mutated despite rejection: %s", f.ID)
				}
			}
		})

		t.Run("sad: duplicate id rejected", func(t *testing.T) {
			// Length matches the cell (3) but f1 appears twice -> dup check fires.
			_, err := mcpBulkReorderFeatures(ctx, s, bulkReorderFeaturesArgs{
				WorkspaceID: ws.ID, MilestoneID: mid, SubWorkflowID: swid,
				FeatureIDs: []string{f1, f1, f2.ID},
			})
			if err == nil {
				t.Errorf("expected duplicate-id error")
			}
		})

		t.Run("guardrail: empty feature_ids rejected", func(t *testing.T) {
			_, err := mcpBulkReorderFeatures(ctx, s, bulkReorderFeaturesArgs{
				WorkspaceID: ws.ID, MilestoneID: mid, SubWorkflowID: swid, FeatureIDs: nil,
			})
			if err == nil {
				t.Errorf("expected empty error")
			}
		})
	})
}

// ===========================================================================
// BULK-017: bulk delete features / personas
// ===========================================================================

func Test_bulk_delete_features(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("mixed: bad id fails slot, good deletes happen", func(t *testing.T) {
			items := []bulkDeleteFeatureItem{
				{FeatureID: fix.Features[1].ID},
				{FeatureID: newUUID()},
				{FeatureID: fix.Features[2].ID},
			}
			res, err := mcpBulkDeleteFeatures(ctx, s, bulkDeleteFeaturesArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk delete features")
			if !res.Results[0].OK || res.Results[1].OK || !res.Results[2].OK {
				t.Fatalf("want [ok,err,ok] got %+v", res.Results)
			}
			// Deleted features must be gone; the bad id must not have poisoned tx.
			board, err := mcpGetBoard(ctx, s, getBoardArgs{WorkspaceID: ws.ID, ProjectID: fix.Project.ID})
			mustOK(t, err, "getBoard")
			deleted := map[string]bool{fix.Features[1].ID: true, fix.Features[2].ID: true}
			for _, f := range board.Features {
				if deleted[f.ID] {
					t.Errorf("feature %s should be deleted", f.ID)
				}
			}
		})

		t.Run("guardrail: empty batch rejected", func(t *testing.T) {
			_, err := mcpBulkDeleteFeatures(ctx, s, bulkDeleteFeaturesArgs{WorkspaceID: ws.ID, Items: nil})
			if err == nil {
				t.Errorf("expected empty-batch error")
			}
		})
	})
}

func Test_bulk_delete_personas(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		// fix.Persona is attached to the workflow (fix.WorkflowPersona). Deleting
		// it must cascade-remove that attachment.
		t.Run("mixed: bad id fails slot; good delete cascades attachment", func(t *testing.T) {
			items := []bulkDeletePersonaItem{
				{PersonaID: fix.Persona.ID},
				{PersonaID: newUUID()},
			}
			res, err := mcpBulkDeletePersonas(ctx, s, bulkDeletePersonasArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk delete personas")
			if !res.Results[0].OK || res.Results[1].OK {
				t.Fatalf("want [ok,err] got %+v", res.Results)
			}
			board, err := mcpGetBoard(ctx, s, getBoardArgs{WorkspaceID: ws.ID, ProjectID: fix.Project.ID})
			mustOK(t, err, "getBoard")
			for _, p := range board.Personas {
				if p.ID == fix.Persona.ID {
					t.Errorf("persona should be deleted")
				}
			}
			for _, wp := range board.WorkflowPersonas {
				if wp.PersonaID == fix.Persona.ID {
					t.Errorf("workflow attachment not cascaded for deleted persona")
				}
			}
		})

		t.Run("guardrail: oversized batch rejected", func(t *testing.T) {
			big := make([]bulkDeletePersonaItem, maxBulkItems+1)
			for i := range big {
				big[i] = bulkDeletePersonaItem{PersonaID: fix.Persona.ID}
			}
			_, err := mcpBulkDeletePersonas(ctx, s, bulkDeletePersonasArgs{WorkspaceID: ws.ID, Items: big})
			if err == nil {
				t.Errorf("expected oversized-batch error")
			}
		})
	})
}
