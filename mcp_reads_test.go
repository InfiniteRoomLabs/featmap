package main

import (
	"context"
	"testing"
)

func Test_mcpGetFeature(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member) {
		fx := newProjectFixture(t, s)
		target := fx.Features[0] // the one with a seeded comment

		// Without comments.
		res, err := mcpGetFeature(ctx, s, getFeatureArgs{
			WorkspaceID: ws.ID, FeatureID: target.ID, IncludeComments: false,
		})
		mustOK(t, err, "mcpGetFeature")
		if res.Feature == nil || res.Feature.ID != target.ID {
			t.Fatalf("expected feature %s, got %+v", target.ID, res.Feature)
		}
		if res.Comments != nil {
			t.Fatalf("expected no comments when IncludeComments=false, got %d", len(res.Comments))
		}

		// With comments -- fixture seeds exactly one on Features[0].
		res2, err := mcpGetFeature(ctx, s, getFeatureArgs{
			WorkspaceID: ws.ID, FeatureID: target.ID, IncludeComments: true,
		})
		mustOK(t, err, "mcpGetFeature include_comments")
		if len(res2.Comments) != 1 {
			t.Fatalf("expected 1 comment, got %d", len(res2.Comments))
		}

		// Unknown id -> clean error, no panic.
		if _, err := mcpGetFeature(ctx, s, getFeatureArgs{
			WorkspaceID: ws.ID, FeatureID: newUUID(),
		}); err == nil {
			t.Fatalf("expected error for unknown feature id")
		}
	})
}

func Test_mcpQueryBoard(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member) {
		fx := newProjectFixture(t, s)

		// Projection: features in M1, return id+title stubs. Titles are "F-M1-SW1" etc.
		res, err := mcpQueryBoard(ctx, s, queryBoardArgs{
			WorkspaceID: ws.ID, ProjectID: fx.Project.ID,
			Filter: `.features[] | select(.title | startswith("F-M1-")) | {id, title}`,
		})
		mustOK(t, err, "mcpQueryBoard projection")
		got, ok := res.Results.([]any)
		if !ok {
			t.Fatalf("expected []any results, got %T", res.Results)
		}
		if len(got) != 3 { // M1 x {SW1,SW2,SW3}
			t.Fatalf("expected 3 M1 features, got %d", len(got))
		}

		// Single-id select returns exactly one card.
		one, err := mcpQueryBoard(ctx, s, queryBoardArgs{
			WorkspaceID: ws.ID, ProjectID: fx.Project.ID,
			Filter: `.features[] | select(.id == "` + fx.Features[0].ID + `")`,
		})
		mustOK(t, err, "mcpQueryBoard single")
		if oneGot := one.Results.([]any); len(oneGot) != 1 {
			t.Fatalf("expected 1 feature, got %d", len(oneGot))
		}

		// No-match filter -> results should be [], not null.
		noMatch, err := mcpQueryBoard(ctx, s, queryBoardArgs{
			WorkspaceID: ws.ID, ProjectID: fx.Project.ID,
			Filter: `.features[] | select(.title == "nonexistent-___")`,
		})
		mustOK(t, err, "mcpQueryBoard no-match")
		if got := noMatch.Results.([]any); len(got) != 0 {
			t.Fatalf("expected empty results, got %d", len(got))
		}

		// Malformed filter -> parse error, no data.
		if _, err := mcpQueryBoard(ctx, s, queryBoardArgs{
			WorkspaceID: ws.ID, ProjectID: fx.Project.ID,
			Filter: `.features[ | select(`,
		}); err == nil {
			t.Fatalf("expected parse error for malformed filter")
		}

		// Empty filter -> required error.
		if _, err := mcpQueryBoard(ctx, s, queryBoardArgs{
			WorkspaceID: ws.ID, ProjectID: fx.Project.ID, Filter: "   ",
		}); err == nil {
			t.Fatalf("expected 'filter is required' error")
		}

		// Unknown project -> not found.
		if _, err := mcpQueryBoard(ctx, s, queryBoardArgs{
			WorkspaceID: ws.ID, ProjectID: newUUID(), Filter: ".project",
		}); err == nil {
			t.Fatalf("expected project not found error")
		}
	})
}
