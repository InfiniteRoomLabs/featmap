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
