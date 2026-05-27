package main

// R2 batch of bulk + partial MCP tools for the structural entities (milestones,
// workflows, subworkflows, personas, comments) plus a declarative feature
// reorder. Layered on top of the single-entity tools in mcp.go and reusing the
// shared primitives from mcp_bulk.go:
//
//   - runBulkTx      -- per-item SAVEPOINT isolation (see mcp_bulk.go).
//   - checkBulkSize  -- empty/oversized guardrail (maxBulkItems = 100).
//   - bulkResult     -- 1:1 per-item {index, ok, id, error} envelope.
//
// Same transaction caveat as the rest of the MCP surface: mware Transaction()
// always commits, so handlers MUST surface failure via the returned error and
// MUST NOT panic mid-write. The apply* helpers below mirror applyFeaturePatch:
// pointer fields (omitted = unchanged), enums validated up front, fetch-once for
// a real not-found, and FK move targets pre-validated before the move call so a
// bad id fails as a clean Go error instead of an FK panic in StoreX.

import (
	"context"
	"errors"
)

// ===========================================================================
// PART-011: shared partial-patch logic for structural entities.
// ===========================================================================

// --- Milestone -------------------------------------------------------------

type milestonePatch struct {
	MilestoneID string
	Title       *string
	Color       *string
	Status      *string
	Index       *int
}

// applyMilestonePatch applies ONLY the provided (non-nil) fields to a milestone,
// reusing the existing single-purpose service methods. Enum fields are validated
// up front so an invalid value fails before any write.
func applyMilestonePatch(s Service, workspaceID string, p milestonePatch) (*Milestone, error) {
	if p.MilestoneID == "" {
		return nil, errors.New("milestone_id required")
	}
	if p.Color != nil && !colorIsValid(*p.Color) {
		return nil, errors.New("invalid color")
	}
	if p.Status != nil && *p.Status != "OPEN" && *p.Status != "CLOSED" {
		return nil, errors.New("status must be OPEN or CLOSED")
	}

	cur, err := s.GetRepoObject().GetMilestone(workspaceID, p.MilestoneID)
	if err != nil || cur == nil {
		return nil, errors.New("milestone not found")
	}
	latest := cur

	if p.Title != nil {
		if latest, err = s.RenameMilestone(p.MilestoneID, *p.Title); err != nil {
			return nil, err
		}
	}
	if p.Color != nil {
		if latest, err = s.ChangeColorOnMilestone(p.MilestoneID, *p.Color); err != nil {
			return nil, err
		}
	}
	if p.Status != nil {
		switch *p.Status {
		case "OPEN":
			latest, err = s.OpenMilestone(p.MilestoneID)
		case "CLOSED":
			latest, err = s.CloseMilestone(p.MilestoneID)
		}
		if err != nil {
			return nil, err
		}
	}
	if p.Index != nil {
		if latest, err = s.MoveMilestone(p.MilestoneID, *p.Index); err != nil {
			return nil, err
		}
	}
	return latest, nil
}

// --- Workflow --------------------------------------------------------------

type workflowPatch struct {
	WorkflowID string
	Title      *string
	Color      *string
	Status     *string
	Index      *int
}

func applyWorkflowPatch(s Service, workspaceID string, p workflowPatch) (*Workflow, error) {
	if p.WorkflowID == "" {
		return nil, errors.New("workflow_id required")
	}
	if p.Color != nil && !colorIsValid(*p.Color) {
		return nil, errors.New("invalid color")
	}
	if p.Status != nil && *p.Status != "OPEN" && *p.Status != "CLOSED" {
		return nil, errors.New("status must be OPEN or CLOSED")
	}

	cur, err := s.GetRepoObject().GetWorkflow(workspaceID, p.WorkflowID)
	if err != nil || cur == nil {
		return nil, errors.New("workflow not found")
	}
	latest := cur

	if p.Title != nil {
		if latest, err = s.RenameWorkflow(p.WorkflowID, *p.Title); err != nil {
			return nil, err
		}
	}
	if p.Color != nil {
		if latest, err = s.ChangeColorOnWorkflow(p.WorkflowID, *p.Color); err != nil {
			return nil, err
		}
	}
	if p.Status != nil {
		switch *p.Status {
		case "OPEN":
			latest, err = s.OpenWorkflow(p.WorkflowID)
		case "CLOSED":
			latest, err = s.CloseWorkflow(p.WorkflowID)
		}
		if err != nil {
			return nil, err
		}
	}
	if p.Index != nil {
		if latest, err = s.MoveWorkflow(p.WorkflowID, *p.Index); err != nil {
			return nil, err
		}
	}
	return latest, nil
}

// --- SubWorkflow -----------------------------------------------------------

type subWorkflowPatch struct {
	SubWorkflowID string
	Title         *string
	Color         *string
	Status        *string
	ToWorkflowID  *string
	Index         *int
}

func applySubWorkflowPatch(s Service, workspaceID string, p subWorkflowPatch) (*SubWorkflow, error) {
	if p.SubWorkflowID == "" {
		return nil, errors.New("subworkflow_id required")
	}
	if p.Color != nil && !colorIsValid(*p.Color) {
		return nil, errors.New("invalid color")
	}
	if p.Status != nil && *p.Status != "OPEN" && *p.Status != "CLOSED" {
		return nil, errors.New("status must be OPEN or CLOSED")
	}

	cur, err := s.GetRepoObject().GetSubWorkflow(workspaceID, p.SubWorkflowID)
	if err != nil || cur == nil {
		return nil, errors.New("subworkflow not found")
	}
	latest := cur

	if p.Title != nil {
		if latest, err = s.RenameSubWorkflow(p.SubWorkflowID, *p.Title); err != nil {
			return nil, err
		}
	}
	if p.Color != nil {
		if latest, err = s.ChangeColorOnSubWorkflow(p.SubWorkflowID, *p.Color); err != nil {
			return nil, err
		}
	}
	if p.Status != nil {
		switch *p.Status {
		case "OPEN":
			latest, err = s.OpenSubWorkflow(p.SubWorkflowID)
		case "CLOSED":
			latest, err = s.CloseSubWorkflow(p.SubWorkflowID)
		}
		if err != nil {
			return nil, err
		}
	}
	if p.ToWorkflowID != nil || p.Index != nil {
		toWF := cur.WorkflowID
		if p.ToWorkflowID != nil {
			toWF = *p.ToWorkflowID
			// Pre-validate the FK target: MoveSubWorkflow does NOT validate the
			// workflow and would reach StoreSubWorkflow -> tx.MustExec -> FK panic.
			if wf, err := s.GetRepoObject().GetWorkflow(workspaceID, toWF); err != nil || wf == nil {
				return nil, errors.New("workflow not found")
			}
		}
		idx := 0
		if p.Index != nil {
			idx = *p.Index
		} else {
			// Default: append to the end of the target workflow. Count siblings
			// excluding self (MoveSubWorkflow removes self before indexing).
			sibs, _ := s.GetRepoObject().FindSubWorkflowsByWorkflow(workspaceID, toWF)
			for _, sw := range sibs {
				if sw.ID != p.SubWorkflowID {
					idx++
				}
			}
			if idx > 1000 {
				idx = 1000
			}
		}
		if latest, err = s.MoveSubWorkflow(p.SubWorkflowID, toWF, idx); err != nil {
			return nil, err
		}
	}
	return latest, nil
}

// ---------------------------------------------------------------------------
// update_milestone / update_workflow / update_subworkflow (PART-011 tools).
// ---------------------------------------------------------------------------

type updateMilestoneArgs struct {
	WorkspaceID string  `json:"workspace_id"`
	MilestoneID string  `json:"milestone_id"`
	Title       *string `json:"title,omitempty"`
	Color       *string `json:"color,omitempty" jsonschema:"color name; one of: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK"`
	Status      *string `json:"status,omitempty" jsonschema:"OPEN or CLOSED"`
	Index       *int    `json:"index,omitempty" jsonschema:"new 0-based position among sibling milestones; omit to keep current"`
}

func (a updateMilestoneArgs) patch() milestonePatch {
	return milestonePatch{MilestoneID: a.MilestoneID, Title: a.Title, Color: a.Color, Status: a.Status, Index: a.Index}
}

func mcpUpdateMilestone(ctx context.Context, s Service, a updateMilestoneArgs) (*Milestone, error) {
	return applyMilestonePatch(s, a.WorkspaceID, a.patch())
}

type updateWorkflowArgs struct {
	WorkspaceID string  `json:"workspace_id"`
	WorkflowID  string  `json:"workflow_id"`
	Title       *string `json:"title,omitempty"`
	Color       *string `json:"color,omitempty" jsonschema:"color name; one of: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK"`
	Status      *string `json:"status,omitempty" jsonschema:"OPEN or CLOSED"`
	Index       *int    `json:"index,omitempty" jsonschema:"new 0-based position among sibling workflows; omit to keep current"`
}

func (a updateWorkflowArgs) patch() workflowPatch {
	return workflowPatch{WorkflowID: a.WorkflowID, Title: a.Title, Color: a.Color, Status: a.Status, Index: a.Index}
}

func mcpUpdateWorkflow(ctx context.Context, s Service, a updateWorkflowArgs) (*Workflow, error) {
	return applyWorkflowPatch(s, a.WorkspaceID, a.patch())
}

type updateSubWorkflowArgs struct {
	WorkspaceID   string  `json:"workspace_id"`
	SubWorkflowID string  `json:"subworkflow_id"`
	Title         *string `json:"title,omitempty"`
	Color         *string `json:"color,omitempty" jsonschema:"color name; one of: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK"`
	Status        *string `json:"status,omitempty" jsonschema:"OPEN or CLOSED"`
	ToWorkflowID  *string `json:"to_workflow_id,omitempty" jsonschema:"move target workflow UUID; omit to keep current workflow"`
	Index         *int    `json:"index,omitempty" jsonschema:"position within the target workflow, 0-based; omit to append to the end when moving"`
}

func (a updateSubWorkflowArgs) patch() subWorkflowPatch {
	return subWorkflowPatch{SubWorkflowID: a.SubWorkflowID, Title: a.Title, Color: a.Color, Status: a.Status, ToWorkflowID: a.ToWorkflowID, Index: a.Index}
}

func mcpUpdateSubWorkflow(ctx context.Context, s Service, a updateSubWorkflowArgs) (*SubWorkflow, error) {
	return applySubWorkflowPatch(s, a.WorkspaceID, a.patch())
}

// ===========================================================================
// BULK-012: bulk_add_comment.
// ===========================================================================

type bulkAddCommentItem struct {
	FeatureID string `json:"feature_id"`
	Body      string `json:"body"`
}

type bulkAddCommentArgs struct {
	WorkspaceID string               `json:"workspace_id"`
	Items       []bulkAddCommentItem `json:"items"`
}

func mcpBulkAddComment(ctx context.Context, s Service, a bulkAddCommentArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkAddCommentItem) (string, error) {
		if item.FeatureID == "" {
			return "", errors.New("feature_id required")
		}
		if item.Body == "" {
			return "", errors.New("body required")
		}
		c, err := s.CreateFeatureCommentWithID(newUUID(), item.FeatureID, item.Body)
		if err != nil {
			return "", err
		}
		return c.ID, nil
	}), nil
}

// ===========================================================================
// BULK-013: bulk structural creates.
// ===========================================================================

type bulkCreateMilestoneItem struct {
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
}

type bulkCreateMilestonesArgs struct {
	WorkspaceID string                    `json:"workspace_id"`
	Items       []bulkCreateMilestoneItem `json:"items"`
}

func mcpBulkCreateMilestones(ctx context.Context, s Service, a bulkCreateMilestonesArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkCreateMilestoneItem) (string, error) {
		if item.Title == "" {
			return "", errors.New("title required")
		}
		if item.ProjectID == "" {
			return "", errors.New("project_id required")
		}
		if p, err := s.GetRepoObject().GetProject(a.WorkspaceID, item.ProjectID); err != nil || p == nil {
			return "", errors.New("project not found")
		}
		m, err := s.CreateMilestoneWithID(newUUID(), item.ProjectID, item.Title)
		if err != nil {
			return "", err
		}
		return m.ID, nil
	}), nil
}

type bulkCreateWorkflowItem struct {
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
}

type bulkCreateWorkflowsArgs struct {
	WorkspaceID string                   `json:"workspace_id"`
	Items       []bulkCreateWorkflowItem `json:"items"`
}

func mcpBulkCreateWorkflows(ctx context.Context, s Service, a bulkCreateWorkflowsArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkCreateWorkflowItem) (string, error) {
		if item.Title == "" {
			return "", errors.New("title required")
		}
		if item.ProjectID == "" {
			return "", errors.New("project_id required")
		}
		if p, err := s.GetRepoObject().GetProject(a.WorkspaceID, item.ProjectID); err != nil || p == nil {
			return "", errors.New("project not found")
		}
		w, err := s.CreateWorkflowWithID(newUUID(), item.ProjectID, item.Title)
		if err != nil {
			return "", err
		}
		return w.ID, nil
	}), nil
}

type bulkCreateSubWorkflowItem struct {
	WorkflowID string `json:"workflow_id"`
	Title      string `json:"title"`
}

type bulkCreateSubWorkflowsArgs struct {
	WorkspaceID string                      `json:"workspace_id"`
	Items       []bulkCreateSubWorkflowItem `json:"items"`
}

func mcpBulkCreateSubWorkflows(ctx context.Context, s Service, a bulkCreateSubWorkflowsArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkCreateSubWorkflowItem) (string, error) {
		if item.Title == "" {
			return "", errors.New("title required")
		}
		if item.WorkflowID == "" {
			return "", errors.New("workflow_id required")
		}
		if wf, err := s.GetRepoObject().GetWorkflow(a.WorkspaceID, item.WorkflowID); err != nil || wf == nil {
			return "", errors.New("workflow not found")
		}
		sw, err := s.CreateSubWorkflowWithID(newUUID(), item.WorkflowID, item.Title)
		if err != nil {
			return "", err
		}
		return sw.ID, nil
	}), nil
}

type bulkCreatePersonaItem struct {
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	Avatar      string `json:"avatar" jsonschema:"avatar slug; one of: avatar00, avatar01, avatar02, avatar03, avatar04, avatar05, avatar06, avatar07, avatar08"`
	Role        string `json:"role,omitempty"`
	Description string `json:"description,omitempty"`
}

type bulkCreatePersonasArgs struct {
	WorkspaceID string                  `json:"workspace_id"`
	Items       []bulkCreatePersonaItem `json:"items"`
}

func mcpBulkCreatePersonas(ctx context.Context, s Service, a bulkCreatePersonasArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkCreatePersonaItem) (string, error) {
		if item.ProjectID == "" {
			return "", errors.New("project_id required")
		}
		if p, err := s.GetRepoObject().GetProject(a.WorkspaceID, item.ProjectID); err != nil || p == nil {
			return "", errors.New("project not found")
		}
		// CreatePersonaWithID validates avatar/name/role/description.
		p, err := s.CreatePersonaWithID(newUUID(), item.ProjectID, item.Avatar, item.Name, item.Role, item.Description, "", "")
		if err != nil {
			return "", err
		}
		return p.ID, nil
	}), nil
}

// ===========================================================================
// BULK-014: bulk attach / detach personas.
// ===========================================================================

type bulkAttachPersonaItem struct {
	PersonaID  string `json:"persona_id"`
	WorkflowID string `json:"workflow_id"`
}

type bulkAttachPersonasArgs struct {
	WorkspaceID string                  `json:"workspace_id"`
	Items       []bulkAttachPersonaItem `json:"items"`
}

func mcpBulkAttachPersonas(ctx context.Context, s Service, a bulkAttachPersonasArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkAttachPersonaItem) (string, error) {
		if item.PersonaID == "" {
			return "", errors.New("persona_id required")
		}
		if item.WorkflowID == "" {
			return "", errors.New("workflow_id required")
		}
		// Pre-validate both FK targets so a bad id fails cleanly.
		if p, err := s.GetRepoObject().GetPersona(a.WorkspaceID, item.PersonaID); err != nil || p == nil {
			return "", errors.New("persona not found")
		}
		if wf, err := s.GetRepoObject().GetWorkflow(a.WorkspaceID, item.WorkflowID); err != nil || wf == nil {
			return "", errors.New("workflow not found")
		}
		wp, err := s.CreateWorkflowPersonaWithID(newUUID(), item.WorkflowID, item.PersonaID)
		if err != nil {
			return "", err
		}
		return wp.ID, nil
	}), nil
}

type bulkDetachPersonaItem struct {
	WorkflowPersonaID string `json:"workflow_persona_id"`
}

type bulkDetachPersonasArgs struct {
	WorkspaceID string                  `json:"workspace_id"`
	Items       []bulkDetachPersonaItem `json:"items"`
}

func mcpBulkDetachPersonas(ctx context.Context, s Service, a bulkDetachPersonasArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkDetachPersonaItem) (string, error) {
		if item.WorkflowPersonaID == "" {
			return "", errors.New("workflow_persona_id required")
		}
		// Pre-validate so a bogus id surfaces an error rather than a silent
		// no-op DELETE that the envelope would report as ok.
		if wp, err := s.GetRepoObject().GetWorkflowPersona(a.WorkspaceID, item.WorkflowPersonaID); err != nil || wp == nil {
			return "", errors.New("workflow persona not found")
		}
		if err := s.DeleteWorkflowPersona(item.WorkflowPersonaID); err != nil {
			return "", err
		}
		return item.WorkflowPersonaID, nil
	}), nil
}

// ===========================================================================
// BULK-015: bulk partial structural updates (wrap the PART-011 apply* funcs).
// ===========================================================================

type bulkUpdateMilestoneItem struct {
	MilestoneID string  `json:"milestone_id"`
	Title       *string `json:"title,omitempty"`
	Color       *string `json:"color,omitempty" jsonschema:"color name; one of: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK"`
	Status      *string `json:"status,omitempty" jsonschema:"OPEN or CLOSED"`
	Index       *int    `json:"index,omitempty"`
}

func (it bulkUpdateMilestoneItem) patch() milestonePatch {
	return milestonePatch{MilestoneID: it.MilestoneID, Title: it.Title, Color: it.Color, Status: it.Status, Index: it.Index}
}

type bulkUpdateMilestonesArgs struct {
	WorkspaceID string                    `json:"workspace_id"`
	Items       []bulkUpdateMilestoneItem `json:"items"`
}

func mcpBulkUpdateMilestones(ctx context.Context, s Service, a bulkUpdateMilestonesArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkUpdateMilestoneItem) (string, error) {
		m, err := applyMilestonePatch(s, a.WorkspaceID, item.patch())
		if err != nil {
			return "", err
		}
		if m == nil {
			return "", errors.New("milestone not found")
		}
		return m.ID, nil
	}), nil
}

type bulkUpdateWorkflowItem struct {
	WorkflowID string  `json:"workflow_id"`
	Title      *string `json:"title,omitempty"`
	Color      *string `json:"color,omitempty" jsonschema:"color name; one of: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK"`
	Status     *string `json:"status,omitempty" jsonschema:"OPEN or CLOSED"`
	Index      *int    `json:"index,omitempty"`
}

func (it bulkUpdateWorkflowItem) patch() workflowPatch {
	return workflowPatch{WorkflowID: it.WorkflowID, Title: it.Title, Color: it.Color, Status: it.Status, Index: it.Index}
}

type bulkUpdateWorkflowsArgs struct {
	WorkspaceID string                   `json:"workspace_id"`
	Items       []bulkUpdateWorkflowItem `json:"items"`
}

func mcpBulkUpdateWorkflows(ctx context.Context, s Service, a bulkUpdateWorkflowsArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkUpdateWorkflowItem) (string, error) {
		w, err := applyWorkflowPatch(s, a.WorkspaceID, item.patch())
		if err != nil {
			return "", err
		}
		if w == nil {
			return "", errors.New("workflow not found")
		}
		return w.ID, nil
	}), nil
}

type bulkUpdateSubWorkflowItem struct {
	SubWorkflowID string  `json:"subworkflow_id"`
	Title         *string `json:"title,omitempty"`
	Color         *string `json:"color,omitempty" jsonschema:"color name; one of: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK"`
	Status        *string `json:"status,omitempty" jsonschema:"OPEN or CLOSED"`
	ToWorkflowID  *string `json:"to_workflow_id,omitempty"`
	Index         *int    `json:"index,omitempty"`
}

func (it bulkUpdateSubWorkflowItem) patch() subWorkflowPatch {
	return subWorkflowPatch{SubWorkflowID: it.SubWorkflowID, Title: it.Title, Color: it.Color, Status: it.Status, ToWorkflowID: it.ToWorkflowID, Index: it.Index}
}

type bulkUpdateSubWorkflowsArgs struct {
	WorkspaceID string                      `json:"workspace_id"`
	Items       []bulkUpdateSubWorkflowItem `json:"items"`
}

func mcpBulkUpdateSubWorkflows(ctx context.Context, s Service, a bulkUpdateSubWorkflowsArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkUpdateSubWorkflowItem) (string, error) {
		sw, err := applySubWorkflowPatch(s, a.WorkspaceID, item.patch())
		if err != nil {
			return "", err
		}
		if sw == nil {
			return "", errors.New("subworkflow not found")
		}
		return sw.ID, nil
	}), nil
}

// ===========================================================================
// BULK-016: bulk_reorder_features.
// ===========================================================================

type bulkReorderFeaturesArgs struct {
	WorkspaceID   string   `json:"workspace_id"`
	MilestoneID   string   `json:"milestone_id" jsonschema:"the milestone (release row) UUID of the target cell"`
	SubWorkflowID string   `json:"subworkflow_id" jsonschema:"the subworkflow (column) UUID of the target cell"`
	FeatureIDs    []string `json:"feature_ids" jsonschema:"feature UUIDs in the desired final top-to-bottom order; every id must already belong to the target cell"`
}

type reorderResult struct {
	Features []*Feature `json:"features"`
}

func mcpBulkReorderFeatures(ctx context.Context, s Service, a bulkReorderFeaturesArgs) (reorderResult, error) {
	if a.MilestoneID == "" || a.SubWorkflowID == "" {
		return reorderResult{}, errors.New("milestone_id and subworkflow_id required")
	}
	if err := checkBulkSize(len(a.FeatureIDs)); err != nil {
		return reorderResult{}, err
	}
	features, err := s.ReorderFeatures(a.MilestoneID, a.SubWorkflowID, a.FeatureIDs)
	if err != nil {
		return reorderResult{}, err
	}
	return reorderResult{Features: features}, nil
}

// ===========================================================================
// BULK-017: bulk delete features / personas.
// ===========================================================================

type bulkDeleteFeatureItem struct {
	FeatureID string `json:"feature_id"`
}

type bulkDeleteFeaturesArgs struct {
	WorkspaceID string                  `json:"workspace_id"`
	Items       []bulkDeleteFeatureItem `json:"items"`
}

func mcpBulkDeleteFeatures(ctx context.Context, s Service, a bulkDeleteFeaturesArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkDeleteFeatureItem) (string, error) {
		if item.FeatureID == "" {
			return "", errors.New("feature_id required")
		}
		// Pre-validate so a bogus id surfaces an error rather than a silent
		// no-op DELETE reported as ok.
		if f, err := s.GetRepoObject().GetFeature(a.WorkspaceID, item.FeatureID); err != nil || f == nil {
			return "", errors.New("feature not found")
		}
		if err := s.DeleteFeature(item.FeatureID); err != nil {
			return "", err
		}
		return item.FeatureID, nil
	}), nil
}

type bulkDeletePersonaItem struct {
	PersonaID string `json:"persona_id"`
}

type bulkDeletePersonasArgs struct {
	WorkspaceID string                  `json:"workspace_id"`
	Items       []bulkDeletePersonaItem `json:"items"`
}

func mcpBulkDeletePersonas(ctx context.Context, s Service, a bulkDeletePersonasArgs) (bulkResult, error) {
	if err := checkBulkSize(len(a.Items)); err != nil {
		return bulkResult{}, err
	}
	return runBulkTx(s, a.Items, func(_ int, item bulkDeletePersonaItem) (string, error) {
		if item.PersonaID == "" {
			return "", errors.New("persona_id required")
		}
		if p, err := s.GetRepoObject().GetPersona(a.WorkspaceID, item.PersonaID); err != nil || p == nil {
			return "", errors.New("persona not found")
		}
		// DeletePersona cascades workflow attachments (same as delete_persona).
		if err := s.DeletePersona(item.PersonaID); err != nil {
			return "", err
		}
		return item.PersonaID, nil
	}), nil
}
