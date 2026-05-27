package main

// Tests for the bulk + unified-partial feature tools (mcp_bulk.go). Same
// harness as mcp_test.go: per-test tx, real postgres, rolled back at the end.

import (
	"context"
	"testing"
)

func strptr(s string) *string { return &s }
func intptr(i int) *int       { return &i }

// ===========================================================================
// update_feature (PART-010): partial single-entity update
// ===========================================================================

func Test_update_feature(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		fid := fix.Features[0].ID

		t.Run("happy: only color changes; title/description preserved", func(t *testing.T) {
			// Seed a known title + description so we can prove they survive.
			_, err := s.RenameFeature(fid, "Original Title")
			mustOK(t, err, "seed rename")
			_, err = s.UpdateFeatureDescription(fid, "original description")
			mustOK(t, err, "seed desc")

			f, err := mcpUpdateFeature(ctx, s, updateFeatureArgs{
				WorkspaceID: ws.ID, FeatureID: fid, Color: strptr("BLUE"),
			})
			mustOK(t, err, "update color only")
			if f.Color != "BLUE" {
				t.Errorf("color: want BLUE got %s", f.Color)
			}
			if f.Title != "Original Title" {
				t.Errorf("title clobbered: got %q", f.Title)
			}
			if f.Description != "original description" {
				t.Errorf("description clobbered: got %q", f.Description)
			}
		})

		t.Run("happy: multiple fields at once", func(t *testing.T) {
			f, err := mcpUpdateFeature(ctx, s, updateFeatureArgs{
				WorkspaceID: ws.ID, FeatureID: fid,
				Title:  strptr("New Title"),
				Status: strptr("CLOSED"),
			})
			mustOK(t, err, "multi update")
			if f.Title != "New Title" || f.Status != "CLOSED" {
				t.Errorf("multi update wrong: %q %q", f.Title, f.Status)
			}
		})

		t.Run("happy: move via partial (omit index -> append)", func(t *testing.T) {
			f, err := mcpUpdateFeature(ctx, s, updateFeatureArgs{
				WorkspaceID:     ws.ID, FeatureID: fid,
				ToMilestoneID:   strptr(fix.Milestones[1].ID),
				ToSubWorkflowID: strptr(fix.SubWorkflows[1].ID),
			})
			mustOK(t, err, "partial move")
			if f.MilestoneID != fix.Milestones[1].ID || f.SubWorkflowID != fix.SubWorkflows[1].ID {
				t.Errorf("move did not land in target cell")
			}
		})

		t.Run("edge: no fields -> returns current feature unchanged", func(t *testing.T) {
			f, err := mcpUpdateFeature(ctx, s, updateFeatureArgs{
				WorkspaceID: ws.ID, FeatureID: fid,
			})
			mustOK(t, err, "noop update")
			if f == nil || f.ID != fid {
				t.Errorf("expected current feature, got %v", f)
			}
		})

		t.Run("enraged: invalid color rejected before write", func(t *testing.T) {
			_, err := mcpUpdateFeature(ctx, s, updateFeatureArgs{
				WorkspaceID: ws.ID, FeatureID: fid, Color: strptr("CHARTREUSE"),
			})
			if err == nil {
				t.Errorf("expected invalid-color error")
			}
		})

		t.Run("enraged: invalid status rejected", func(t *testing.T) {
			_, err := mcpUpdateFeature(ctx, s, updateFeatureArgs{
				WorkspaceID: ws.ID, FeatureID: fid, Status: strptr("DONE"),
			})
			if err == nil {
				t.Errorf("expected invalid-status error")
			}
		})

		t.Run("sad: bogus feature id -> not found", func(t *testing.T) {
			_, err := mcpUpdateFeature(ctx, s, updateFeatureArgs{
				WorkspaceID: ws.ID, FeatureID: newUUID(), Title: strptr("X"),
			})
			if err == nil {
				t.Errorf("expected not-found error")
			}
		})
	})
}

// ===========================================================================
// bulk_create_features (BULK-010)
// ===========================================================================

func Test_bulk_create_features(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: N features, each result carries an id", func(t *testing.T) {
			items := []bulkCreateFeatureItem{
				{SubWorkflowID: fix.SubWorkflows[0].ID, MilestoneID: fix.Milestones[0].ID, Title: "B1"},
				{SubWorkflowID: fix.SubWorkflows[1].ID, MilestoneID: fix.Milestones[0].ID, Title: "B2"},
				{SubWorkflowID: fix.SubWorkflows[2].ID, MilestoneID: fix.Milestones[1].ID, Title: "B3"},
			}
			res, err := mcpBulkCreateFeatures(ctx, s, bulkCreateFeaturesArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk create")
			if len(res.Results) != 3 {
				t.Fatalf("want 3 results got %d", len(res.Results))
			}
			for i, r := range res.Results {
				if !r.OK || r.ID == "" {
					t.Errorf("item %d: want ok+id, got %+v", i, r)
				}
				if r.Index != i {
					t.Errorf("item %d: index mismatch %d", i, r.Index)
				}
			}
		})

		t.Run("mixed: bad milestone fails its slot, others proceed", func(t *testing.T) {
			items := []bulkCreateFeatureItem{
				{SubWorkflowID: fix.SubWorkflows[0].ID, MilestoneID: fix.Milestones[0].ID, Title: "OK1"},
				{SubWorkflowID: fix.SubWorkflows[0].ID, MilestoneID: newUUID(), Title: "BadMS"},
				{SubWorkflowID: fix.SubWorkflows[0].ID, MilestoneID: fix.Milestones[0].ID, Title: "OK2"},
			}
			res, err := mcpBulkCreateFeatures(ctx, s, bulkCreateFeaturesArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk create mixed")
			if !res.Results[0].OK || res.Results[2].OK == false {
				t.Errorf("expected items 0 and 2 ok, got %+v", res.Results)
			}
			if res.Results[1].OK {
				t.Errorf("expected item 1 (bad milestone) to fail, got %+v", res.Results[1])
			}
			// Persistence proof: the two successful creates must survive on the
			// board (tx not poisoned by the failed sibling).
			board, err := mcpGetBoard(ctx, s, getBoardArgs{WorkspaceID: ws.ID, ProjectID: fix.Project.ID})
			mustOK(t, err, "getBoard after mixed bulk create")
			ids := map[string]bool{res.Results[0].ID: true, res.Results[2].ID: true}
			got := 0
			for _, f := range board.Features {
				if ids[f.ID] {
					got++
				}
			}
			if got != 2 {
				t.Errorf("expected both good creates persisted, found %d", got)
			}
		})

		t.Run("mixed: empty title fails its slot, others proceed", func(t *testing.T) {
			items := []bulkCreateFeatureItem{
				{SubWorkflowID: fix.SubWorkflows[0].ID, MilestoneID: fix.Milestones[0].ID, Title: ""},
				{SubWorkflowID: fix.SubWorkflows[0].ID, MilestoneID: fix.Milestones[0].ID, Title: "Good"},
			}
			res, err := mcpBulkCreateFeatures(ctx, s, bulkCreateFeaturesArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk create empty title")
			if res.Results[0].OK {
				t.Errorf("expected empty-title item to fail")
			}
			if !res.Results[1].OK {
				t.Errorf("expected second item to succeed, got %+v", res.Results[1])
			}
		})

		t.Run("guardrail: empty batch rejected, no writes", func(t *testing.T) {
			_, err := mcpBulkCreateFeatures(ctx, s, bulkCreateFeaturesArgs{WorkspaceID: ws.ID, Items: nil})
			if err == nil {
				t.Errorf("expected empty-batch error")
			}
		})

		t.Run("guardrail: oversized batch rejected, no writes", func(t *testing.T) {
			items := make([]bulkCreateFeatureItem, maxBulkItems+1)
			for i := range items {
				items[i] = bulkCreateFeatureItem{
					SubWorkflowID: fix.SubWorkflows[0].ID, MilestoneID: fix.Milestones[0].ID, Title: "x",
				}
			}
			_, err := mcpBulkCreateFeatures(ctx, s, bulkCreateFeaturesArgs{WorkspaceID: ws.ID, Items: items})
			if err == nil {
				t.Errorf("expected oversized-batch error")
			}
		})
	})
}

// ===========================================================================
// bulk_update_features (BULK-011)
// ===========================================================================

func Test_bulk_update_features(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)

		t.Run("happy: all valid", func(t *testing.T) {
			items := []bulkUpdateFeatureItem{
				{FeatureID: fix.Features[0].ID, Color: strptr("RED")},
				{FeatureID: fix.Features[1].ID, Title: strptr("Updated")},
			}
			res, err := mcpBulkUpdateFeatures(ctx, s, bulkUpdateFeaturesArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk update")
			for i, r := range res.Results {
				if !r.OK {
					t.Errorf("item %d failed: %+v", i, r)
				}
			}
		})

		t.Run("mixed: one valid + one bad feature_id; request still succeeds", func(t *testing.T) {
			items := []bulkUpdateFeatureItem{
				{FeatureID: fix.Features[2].ID, Color: strptr("GREEN")},
				{FeatureID: newUUID(), Color: strptr("BLUE")},
			}
			res, err := mcpBulkUpdateFeatures(ctx, s, bulkUpdateFeaturesArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk update mixed")
			if len(res.Results) != 2 {
				t.Fatalf("want 2 results got %d", len(res.Results))
			}
			if !res.Results[0].OK {
				t.Errorf("item 0 should succeed: %+v", res.Results[0])
			}
			if res.Results[1].OK {
				t.Errorf("item 1 (bad id) should fail: %+v", res.Results[1])
			}
		})

		t.Run("mixed: invalid color fails its slot only", func(t *testing.T) {
			items := []bulkUpdateFeatureItem{
				{FeatureID: fix.Features[3].ID, Color: strptr("NOTACOLOR")},
				{FeatureID: fix.Features[4].ID, Status: strptr("CLOSED")},
			}
			res, err := mcpBulkUpdateFeatures(ctx, s, bulkUpdateFeaturesArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk update bad color")
			if res.Results[0].OK {
				t.Errorf("bad color item should fail")
			}
			if !res.Results[1].OK {
				t.Errorf("valid status item should succeed: %+v", res.Results[1])
			}
		})

		t.Run("guardrail: oversized batch rejected", func(t *testing.T) {
			items := make([]bulkUpdateFeatureItem, maxBulkItems+1)
			for i := range items {
				items[i] = bulkUpdateFeatureItem{FeatureID: fix.Features[0].ID, Color: strptr("RED")}
			}
			_, err := mcpBulkUpdateFeatures(ctx, s, bulkUpdateFeaturesArgs{WorkspaceID: ws.ID, Items: items})
			if err == nil {
				t.Errorf("expected oversized-batch error")
			}
		})

		t.Run("guardrail: empty batch rejected", func(t *testing.T) {
			_, err := mcpBulkUpdateFeatures(ctx, s, bulkUpdateFeaturesArgs{WorkspaceID: ws.ID, Items: nil})
			if err == nil {
				t.Errorf("expected empty-batch error")
			}
		})

		t.Run("regression: bad to_milestone_id mid-batch -> [ok,err,ok] AND good items persist", func(t *testing.T) {
			// move pre-validation makes the bad item fail cleanly (no panic);
			// savepoints are the backstop. Either way the shared tx must stay
			// valid so the surrounding items' writes survive the commit.
			items := []bulkUpdateFeatureItem{
				{FeatureID: fix.Features[0].ID, Title: strptr("Persisted-A")},
				{FeatureID: fix.Features[1].ID, ToMilestoneID: strptr(newUUID())},
				{FeatureID: fix.Features[2].ID, Title: strptr("Persisted-B")},
			}
			res, err := mcpBulkUpdateFeatures(ctx, s, bulkUpdateFeaturesArgs{WorkspaceID: ws.ID, Items: items})
			mustOK(t, err, "bulk update with bad move target")
			if !res.Results[0].OK || res.Results[1].OK || !res.Results[2].OK {
				t.Fatalf("want [ok,err,ok], got %+v", res.Results)
			}
			// Persistence proof: re-fetch via a fresh read on the same tx. If the
			// tx had been poisoned this SELECT would error at mustOK.
			f0, err := s.GetRepoObject().GetFeature(ws.ID, fix.Features[0].ID)
			mustOK(t, err, "refetch f0")
			f2, err := s.GetRepoObject().GetFeature(ws.ID, fix.Features[2].ID)
			mustOK(t, err, "refetch f2")
			if f0.Title != "Persisted-A" || f2.Title != "Persisted-B" {
				t.Errorf("good items not persisted: %q %q", f0.Title, f2.Title)
			}
		})
	})
}

// Test_runBulkTx_savepoint_isolation drives runBulkTx directly with a function
// that triggers a genuine mid-write FK panic (StoreFeature -> tx.MustExec on a
// bogus subworkflow), bypassing the bulk handlers' Go-level pre-validation. This
// is the test that fails WITHOUT the savepoint fix: the panic poisons the shared
// postgres tx, so the trailing good item's write (and the subsequent SELECT)
// also fail. WITH per-item savepoints, ROLLBACK TO SAVEPOINT recovers the tx and
// the good items persist.
func Test_runBulkTx_savepoint_isolation(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, _ *Account, ws *Workspace, _ *Member) {
		fix := newProjectFixture(t, s)
		goodSW := fix.SubWorkflows[0].ID
		badSW := newUUID() // no such subworkflow -> FK violation in StoreFeature
		ms := fix.Milestones[0].ID

		type item struct{ sw, title string }
		items := []item{
			{goodSW, "SP-Good-1"},
			{badSW, "SP-Bad-FK"},
			{goodSW, "SP-Good-2"},
		}

		res := runBulkTx(s, items, func(_ int, it item) (string, error) {
			f, err := s.CreateFeatureWithID(newUUID(), it.sw, ms, it.title)
			if err != nil {
				return "", err
			}
			return f.ID, nil
		})

		if len(res.Results) != 3 {
			t.Fatalf("want 3 results got %d", len(res.Results))
		}
		if !res.Results[0].OK {
			t.Errorf("item 0 should succeed: %+v", res.Results[0])
		}
		if res.Results[1].OK {
			t.Errorf("item 1 (FK panic) should fail: %+v", res.Results[1])
		}
		if !res.Results[2].OK {
			t.Errorf("item 2 should succeed AFTER the poisoning panic was rolled back: %+v", res.Results[2])
		}

		// Persistence proof: this read fails at mustOK if the tx is poisoned.
		board, err := mcpGetBoard(ctx, s, getBoardArgs{WorkspaceID: ws.ID, ProjectID: fix.Project.ID})
		mustOK(t, err, "getBoard after savepoint-isolated bulk")
		titles := map[string]bool{}
		for _, f := range board.Features {
			titles[f.Title] = true
		}
		if !titles["SP-Good-1"] || !titles["SP-Good-2"] {
			t.Errorf("good features not persisted (tx poisoned?): %v", titles)
		}
		if titles["SP-Bad-FK"] {
			t.Errorf("the FK-violating feature must NOT have persisted")
		}
	})
}
