package main

// Bulk + unified-partial MCP tools, layered on top of the single-entity tools
// in mcp.go. These share the same auth/workspace-resolution path (registered
// via add() in buildMCPServer) and the same transaction caveat: mware
// Transaction() always commits, so handlers MUST surface failure via the
// returned error and MUST NOT panic mid-write.
//
// Bulk loops are best-effort: each item runs under a per-item recover() so one
// item's failure (validation error or panic) becomes that item's result slot
// rather than aborting the whole request. Results are 1:1 with input order.
//
// Tx-poisoning note: because every item in a bulk call shares ONE postgres
// transaction (see mware), a database-level failure (e.g. an FK violation from
// repo MustExec) aborts the tx and poisons all *subsequent* statements. Two
// layers defend the "others proceed" guarantee:
//   1. runBulkTx wraps each item in a per-item SAVEPOINT and ROLLBACK TO
//      SAVEPOINT on failure -- the general fix that recovers the shared tx from
//      the aborted state regardless of how an item fails.
//   2. The handlers additionally pre-validate FK targets (subworkflow +
//      milestone) with read queries so the common bad-id case fails as a clean
//      Go error and never trips a panic in the first place.

import (
	"context"
	"errors"
	"fmt"
)

// ---------------------------------------------------------------------------
// Shared bulk envelope (BULK-001).
// ---------------------------------------------------------------------------

type bulkItemResult struct {
	Index int    `json:"index"`
	OK    bool   `json:"ok"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error,omitempty"`
}

type bulkResult struct {
	Results []bulkItemResult `json:"results"`
}

// maxBulkItems caps a single bulk request. Oversized batches are rejected up
// front (no writes) -- see checkBulkSize.
const maxBulkItems = 100

// checkBulkSize is the BULK-002 guardrail. Callers MUST run it before entering
// the per-item loop so an oversized/empty batch produces zero writes.
func checkBulkSize(n int) error {
	if n == 0 {
		return errors.New("items must not be empty")
	}
	if n > maxBulkItems {
		return fmt.Errorf("too many items: %d (max %d)", n, maxBulkItems)
	}
	return nil
}

// runBulkTx applies fn to each item, isolating each on a per-item SAVEPOINT of
// the shared request transaction. Results are 1:1 with input order; a failing
// item never aborts the loop AND never poisons the tx for its siblings or the
// final commit.
//
// Why savepoints are load-bearing: mware Transaction() runs the whole bulk call
// in one postgres tx and always commits. Some service writes (StoreFeature ->
// tx.MustExec) panic on a constraint violation, which puts postgres into an
// "aborted transaction" state -- every later statement, including the commit,
// then fails. A per-item recover() alone would report the panic but still lose
// every already-succeeded sibling at commit time. ROLLBACK TO SAVEPOINT recovers
// the tx from the aborted state back to the pre-item checkpoint, so siblings and
// the commit survive. The recover() is the backstop that turns a panic into a
// returned error so we reach the rollback.
//
// fn returns the new/affected entity id on success.
func runBulkTx[T any](s Service, items []T, fn func(i int, item T) (string, error)) bulkResult {
	repo := s.GetRepoObject()
	results := make([]bulkItemResult, len(items))
	for i, item := range items {
		// Fixed prefix + integer index -- never user input (savepoint names
		// cannot be parameterized).
		name := fmt.Sprintf("bulk_item_%d", i)
		if err := repo.Savepoint(name); err != nil {
			results[i] = bulkItemResult{Index: i, OK: false, Error: "savepoint failed: " + err.Error()}
			continue
		}

		var id string
		var ferr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					ferr = fmt.Errorf("panic: %v", r)
				}
			}()
			id, ferr = fn(i, item)
		}()

		if ferr != nil {
			// Roll back this item's (possibly aborted) writes so the shared tx
			// stays valid; release to avoid accumulating savepoints.
			_ = repo.RollbackToSavepoint(name)
			_ = repo.ReleaseSavepoint(name)
			results[i] = bulkItemResult{Index: i, OK: false, Error: ferr.Error()}
			continue
		}
		_ = repo.ReleaseSavepoint(name)
		results[i] = bulkItemResult{Index: i, OK: true, ID: id}
	}
	return bulkResult{Results: results}
}

// ---------------------------------------------------------------------------
// Shared partial-patch logic (PART-010). featurePatch is an internal (NOT
// serialized) struct so both the single update_feature tool and the bulk
// update items can funnel through one apply path.
// ---------------------------------------------------------------------------

type featurePatch struct {
	FeatureID       string
	Title           *string
	Description     *string
	Color           *string
	Status          *string
	ToMilestoneID   *string
	ToSubWorkflowID *string
	Index           *int
}

// applyFeaturePatch applies ONLY the provided (non-nil) fields to a feature,
// reusing the existing single-purpose service methods. workspaceID must already
// be resolved onto the service (resolveWorkspace ran). Enum fields are validated
// up front so an invalid value fails before any write. Returns the latest
// feature state (result of the last applied mutation).
func applyFeaturePatch(s Service, workspaceID string, p featurePatch) (*Feature, error) {
	if p.FeatureID == "" {
		return nil, errors.New("feature_id required")
	}
	if p.Color != nil && !colorIsValid(*p.Color) {
		return nil, errors.New("invalid color")
	}
	if p.Status != nil && *p.Status != "OPEN" && *p.Status != "CLOSED" {
		return nil, errors.New("status must be OPEN or CLOSED")
	}

	// Fetch once: yields a real "not found" (rename's path silently returns
	// nil) and supplies defaults for partial moves.
	cur, err := s.GetRepoObject().GetFeature(workspaceID, p.FeatureID)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, errors.New("feature not found")
	}

	latest := cur

	if p.Title != nil {
		latest, err = s.RenameFeature(p.FeatureID, *p.Title)
		if err != nil {
			return nil, err
		}
	}
	if p.Description != nil {
		latest, err = s.UpdateFeatureDescription(p.FeatureID, *p.Description)
		if err != nil {
			return nil, err
		}
	}
	if p.Color != nil {
		latest, err = s.ChangeColorOnFeature(p.FeatureID, *p.Color)
		if err != nil {
			return nil, err
		}
	}
	if p.Status != nil {
		switch *p.Status {
		case "OPEN":
			latest, err = s.OpenFeature(p.FeatureID)
		case "CLOSED":
			latest, err = s.CloseFeature(p.FeatureID)
		}
		if err != nil {
			return nil, err
		}
	}
	if p.ToMilestoneID != nil || p.ToSubWorkflowID != nil || p.Index != nil {
		toM := cur.MilestoneID
		if p.ToMilestoneID != nil {
			toM = *p.ToMilestoneID
			// Pre-validate the FK target so a bad id fails cleanly here.
			// service.MoveFeature does NOT validate the milestone/subworkflow
			// and would reach StoreFeature -> tx.MustExec -> FK panic.
			if m, err := s.GetRepoObject().GetMilestone(workspaceID, toM); err != nil || m == nil {
				return nil, errors.New("milestone not found")
			}
		}
		toSW := cur.SubWorkflowID
		if p.ToSubWorkflowID != nil {
			toSW = *p.ToSubWorkflowID
			if sw, err := s.GetRepoObject().GetSubWorkflow(workspaceID, toSW); err != nil || sw == nil {
				return nil, errors.New("subworkflow not found")
			}
		}
		idx := 0
		if p.Index != nil {
			idx = *p.Index
		} else {
			// Default: append to the end of the target cell. Count siblings
			// excluding the feature itself (MoveFeature removes self before
			// indexing, so len-including-self would be out of range).
			cell, _ := s.GetRepoObject().FindFeaturesByMilestoneAndSubWorkflow(workspaceID, toM, toSW)
			for _, f := range cell {
				if f.ID != p.FeatureID {
					idx++
				}
			}
			if idx > 1000 {
				idx = 1000
			}
		}
		latest, err = s.MoveFeature(p.FeatureID, toM, toSW, idx)
		if err != nil {
			return nil, err
		}
	}

	return latest, nil
}

// ---------------------------------------------------------------------------
// update_feature (PART-010): unified single-entity partial update.
// ---------------------------------------------------------------------------

type updateFeatureArgs struct {
	WorkspaceID     string  `json:"workspace_id"`
	FeatureID       string  `json:"feature_id"`
	Title           *string `json:"title,omitempty"`
	Description     *string `json:"description,omitempty"`
	Color           *string `json:"color,omitempty" jsonschema:"color name; one of: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK"`
	Status          *string `json:"status,omitempty" jsonschema:"OPEN or CLOSED"`
	ToMilestoneID   *string `json:"to_milestone_id,omitempty" jsonschema:"move target milestone (release) UUID; omit to keep current"`
	ToSubWorkflowID *string `json:"to_subworkflow_id,omitempty" jsonschema:"move target subworkflow (column) UUID; omit to keep current"`
	Index           *int    `json:"index,omitempty" jsonschema:"position within the target cell, 0-based; omit to append to the end when moving"`
}

func (a updateFeatureArgs) patch() featurePatch {
	return featurePatch{
		FeatureID:       a.FeatureID,
		Title:           a.Title,
		Description:     a.Description,
		Color:           a.Color,
		Status:          a.Status,
		ToMilestoneID:   a.ToMilestoneID,
		ToSubWorkflowID: a.ToSubWorkflowID,
		Index:           a.Index,
	}
}

func mcpUpdateFeature(ctx context.Context, s Service, a updateFeatureArgs) (*Feature, error) {
	return applyFeaturePatch(s, a.WorkspaceID, a.patch())
}

// ---------------------------------------------------------------------------
// bulk_create_features (BULK-010).
// ---------------------------------------------------------------------------

type bulkCreateFeatureItem struct {
	SubWorkflowID string `json:"subworkflow_id" jsonschema:"target subworkflow (column) UUID"`
	MilestoneID   string `json:"milestone_id" jsonschema:"target milestone (release) UUID"`
	Title         string `json:"title"`
}

type bulkCreateFeaturesArgs struct {
	WorkspaceID string                  `json:"workspace_id"`
	Items       []bulkCreateFeatureItem `json:"items"`
}

func mcpBulkCreateFeatures(ctx context.Context, s Service, a bulkCreateFeaturesArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkCreateFeatureItem) (string, error) {
		if item.Title == "" {
			return "", errors.New("title required")
		}
		if item.SubWorkflowID == "" {
			return "", errors.New("subworkflow_id required")
		}
		if item.MilestoneID == "" {
			return "", errors.New("milestone_id required")
		}
		// Pre-validate FK targets so a bad id fails cleanly instead of
		// triggering an FK panic in StoreFeature that would poison the shared
		// transaction for every later item.
		if sw, err := s.GetRepoObject().GetSubWorkflow(a.WorkspaceID, item.SubWorkflowID); err != nil || sw == nil {
			return "", errors.New("subworkflow not found")
		}
		if m, err := s.GetRepoObject().GetMilestone(a.WorkspaceID, item.MilestoneID); err != nil || m == nil {
			return "", errors.New("milestone not found")
		}
		f, err := s.CreateFeatureWithID(newUUID(), item.SubWorkflowID, item.MilestoneID, item.Title)
		if err != nil {
			return "", err
		}
		return f.ID, nil
	}), nil
}

// ---------------------------------------------------------------------------
// bulk_update_features (BULK-011): per-item PART-010 partial update.
// ---------------------------------------------------------------------------

type bulkUpdateFeatureItem struct {
	FeatureID       string  `json:"feature_id"`
	Title           *string `json:"title,omitempty"`
	Description     *string `json:"description,omitempty"`
	Color           *string `json:"color,omitempty" jsonschema:"color name; one of: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK"`
	Status          *string `json:"status,omitempty" jsonschema:"OPEN or CLOSED"`
	ToMilestoneID   *string `json:"to_milestone_id,omitempty"`
	ToSubWorkflowID *string `json:"to_subworkflow_id,omitempty"`
	Index           *int    `json:"index,omitempty"`
}

func (it bulkUpdateFeatureItem) patch() featurePatch {
	return featurePatch{
		FeatureID:       it.FeatureID,
		Title:           it.Title,
		Description:     it.Description,
		Color:           it.Color,
		Status:          it.Status,
		ToMilestoneID:   it.ToMilestoneID,
		ToSubWorkflowID: it.ToSubWorkflowID,
		Index:           it.Index,
	}
}

type bulkUpdateFeaturesArgs struct {
	WorkspaceID string                  `json:"workspace_id"`
	Items       []bulkUpdateFeatureItem `json:"items"`
}

func mcpBulkUpdateFeatures(ctx context.Context, s Service, a bulkUpdateFeaturesArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkUpdateFeatureItem) (string, error) {
		f, err := applyFeaturePatch(s, a.WorkspaceID, item.patch())
		if err != nil {
			return "", err
		}
		if f == nil {
			return "", errors.New("feature not found")
		}
		return f.ID, nil
	}), nil
}
