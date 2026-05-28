package main

// MCP scoped-read tools: get_feature (typed single-card drill) and query_board
// (gojq filter/projection over the full board). These keep an agent from having
// to fetch the entire board (hundreds of KB) just to read a slice.
//
// Reads are read-only, so the always-commit Transaction() tx-poisoning gotcha
// does not apply -- handlers simply return errors, never panic.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/itchyny/gojq"
)

// --- get_feature ---------------------------------------------------------

type getFeatureArgs struct {
	WorkspaceID     string `json:"workspace_id" jsonschema:"the workspace UUID"`
	FeatureID       string `json:"feature_id" jsonschema:"the feature (card) UUID to read"`
	IncludeComments bool   `json:"include_comments" jsonschema:"if true, also return the card's comments (oldest first)"`
}

type featureResult struct {
	Feature  *Feature          `json:"feature"`
	Comments []*FeatureComment `json:"comments,omitempty"`
}

func mcpGetFeature(ctx context.Context, s Service, a getFeatureArgs) (*featureResult, error) {
	f, err := s.GetFeature(a.FeatureID)
	if err != nil {
		return nil, errors.New("feature not found")
	}
	res := &featureResult{Feature: f}
	if a.IncludeComments {
		res.Comments = s.GetFeatureCommentsByFeature(a.FeatureID)
	}
	return res, nil
}

// --- query_board ---------------------------------------------------------

// queryBoardFilterDoc is embedded in the tool description so the model sees the
// board shape AND worked examples right where it writes the filter.
const queryBoardFilterDoc = `A jq (gojq) program run over the FULL board JSON. ` +
	`Returns matched values wrapped as {"results":[...]}. ` +
	`Board shape: {project, milestones:[{id,title,color,status}], ` +
	`workflows:[{id,title}], subWorkflows:[{id,title,workflowId}], ` +
	`features:[{id,title,description,color,status,milestoneId,subWorkflowId,rank,estimate}], ` +
	`featureComments:[{id,featureId,post}], personas:[...], workflowPersonas:[...]}. ` +
	`Examples -- ` +
	`stubs for a prefix: ".features[] | select(.title|startswith(\"SYNC-\")) | {id,title,status,color}" ; ` +
	`one card body: ".features[] | select(.id==\"<uuid>\")" ; ` +
	`a card's comments: ".featureComments[] | select(.featureId==\"<uuid>\") | .post". ` +
	`Note: regex uses Go RE2 (no lookaround/backreferences).`

type queryBoardArgs struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"the workspace UUID"`
	ProjectID   string `json:"project_id" jsonschema:"the project UUID whose board to query"`
	Filter      string `json:"filter" jsonschema:"REQUIRED jq program; see description"`
}

// queryBoardResult wraps arbitrary jq output. Results is `any` so the SDK derives
// a permissive schema -- the {results:...} envelope validates, the inner content
// is unconstrained (caller-controlled projection).
type queryBoardResult struct {
	Results any `json:"results"`
}

func mcpQueryBoard(ctx context.Context, s Service, a queryBoardArgs) (*queryBoardResult, error) {
	if strings.TrimSpace(a.Filter) == "" {
		return nil, errors.New("filter is required (use get_board for the full untyped board)")
	}
	project := s.GetProject(a.ProjectID)
	if project == nil {
		return nil, errors.New("project not found")
	}
	board := boardResult{
		Project:          project,
		Milestones:       s.GetMilestonesByProject(a.ProjectID),
		Workflows:        s.GetWorkflowsByProject(a.ProjectID),
		SubWorkflows:     s.GetSubWorkflowsByProject(a.ProjectID),
		Features:         s.GetFeaturesByProject(a.ProjectID),
		FeatureComments:  s.GetFeatureCommentsByProject(a.ProjectID),
		Personas:         s.GetPersonasByProject(a.ProjectID),
		WorkflowPersonas: s.GetWorkflowPersonasByProject(a.ProjectID),
	}
	out, err := runBoardFilter(ctx, board, a.Filter)
	if err != nil {
		return nil, err
	}
	return &queryBoardResult{Results: out}, nil
}

// runBoardFilter marshals the board to generic JSON, compiles the jq program,
// runs it, and collects all emitted values. Parse/compile failures and runtime
// errors are returned as errors (never panics).
func runBoardFilter(ctx context.Context, board boardResult, filter string) ([]any, error) {
	query, err := gojq.Parse(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter: %w", err)
	}
	code, err := gojq.Compile(query)
	if err != nil {
		return nil, fmt.Errorf("invalid filter: %w", err)
	}

	// gojq operates on generic interface{} data, not Go structs.
	// Round-trip through JSON to convert typed Go structs into the map/slice/scalar
	// tree that gojq expects. Acceptable overhead for the sizes this tool handles.
	raw, err := json.Marshal(board)
	if err != nil {
		return nil, fmt.Errorf("marshaling board: %w", err)
	}
	var input any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("decoding board: %w", err)
	}

	results := []any{}
	iter := code.RunWithContext(ctx, input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if e, ok := v.(error); ok {
			if he, halt := e.(*gojq.HaltError); halt && he.Value() == nil {
				break
			}
			return nil, fmt.Errorf("filter error: %w", e)
		}
		results = append(results, v)
	}
	return results, nil
}
