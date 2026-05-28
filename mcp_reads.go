package main

// MCP scoped-read tools: get_feature (typed single-card drill) and query_board
// (gojq filter/projection over the full board). These keep an agent from having
// to fetch the entire board (hundreds of KB) just to read a slice.
//
// Reads are read-only, so the always-commit Transaction() tx-poisoning gotcha
// does not apply -- handlers simply return errors, never panic.

import (
	"context"
	"errors"
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
