package main

import (
	"context"
	"errors"
	"time"
)

type planeConnectionArgs struct {
	WorkspaceID     string `json:"workspace_id" jsonschema:"the workspace UUID"`
	ProjectID       string `json:"project_id" jsonschema:"the featmap project UUID"`
	BaseURL         string `json:"base_url" jsonschema:"Plane base URL, e.g. https://api.plane.so"`
	PlaneWorkspace  string `json:"plane_workspace" jsonschema:"Plane workspace slug"`
	APIKey          string `json:"api_key" jsonschema:"Plane API key (stored encrypted; write-only)"`
	WatchedProjects string `json:"watched_projects" jsonschema:"comma-separated Plane project ids to watch"`
}

func mcpSetPlaneConnection(ctx context.Context, s Service, a planeConnectionArgs) (*PlaneConnection, error) {
	if a.APIKey == "" {
		return nil, errors.New("api_key is required")
	}
	return s.SetPlaneConnection(a.ProjectID, a.BaseURL, a.PlaneWorkspace, a.APIKey, a.WatchedProjects)
}

type planeProjectArgs struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"the workspace UUID"`
	ProjectID   string `json:"project_id" jsonschema:"the featmap project UUID"`
}

func mcpGetPlaneConnection(ctx context.Context, s Service, a planeProjectArgs) (*PlaneConnection, error) {
	return s.GetPlaneConnection(a.ProjectID)
}

func mcpTestPlaneConnection(ctx context.Context, s Service, a planeProjectArgs) (okResult, error) {
	if err := s.TestPlaneConnection(a.ProjectID); err != nil {
		return okResult{}, err
	}
	return okResult{OK: true}, nil
}

type planeLinkArgs struct {
	WorkspaceID     string `json:"workspace_id" jsonschema:"the workspace UUID"`
	FeatureID       string `json:"feature_id" jsonschema:"the featmap feature (card) UUID"`
	PlaneProjectID  string `json:"plane_project_id" jsonschema:"the Plane project id the work item lives in"`
	PlaneWorkItemID string `json:"plane_work_item_id" jsonschema:"the Plane work item id to link"`
}

func mcpLinkFeatureToPlane(ctx context.Context, s Service, a planeLinkArgs) (*PlaneLink, error) {
	return s.LinkFeatureToPlane(a.FeatureID, a.PlaneProjectID, a.PlaneWorkItemID)
}

type planeUnlinkArgs struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"the workspace UUID"`
	FeatureID   string `json:"feature_id" jsonschema:"the featmap feature (card) UUID"`
}

func mcpUnlinkFeatureFromPlane(ctx context.Context, s Service, a planeUnlinkArgs) (okResult, error) {
	if err := s.UnlinkFeatureFromPlane(a.FeatureID); err != nil {
		return okResult{}, err
	}
	return okResult{OK: true}, nil
}

type planeSyncArgs struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"the workspace UUID"`
	ProjectID   string `json:"project_id" jsonschema:"the featmap project UUID to sync"`
	FeatureID   string `json:"feature_id" jsonschema:"optional: sync only this card's link"`
}

func mcpPlaneSync(ctx context.Context, s Service, a planeSyncArgs) (*SyncResult, error) {
	if a.FeatureID != "" {
		link, err := s.GetPlaneLinkByFeature(a.FeatureID)
		if err != nil {
			return nil, err
		}
		pushed, pulled, serr := s.SyncLink(ctx, link)
		now := time.Now().UTC()
		link.LastSyncedAt = &now
		status := string(StatusOK)
		errStr := ""
		if serr != nil {
			link.LastStatus = string(StatusError)
			link.LastError = serr.Error()
			status = string(StatusError)
			errStr = serr.Error()
		} else {
			link.LastStatus = string(StatusOK)
			link.LastError = ""
		}
		s.GetRepoObject().StorePlaneLink(link) // persist status + advanced cursor
		return &SyncResult{Pushed: pushed, Pulled: pulled, PerLink: []LinkSyncResult{
			{LinkID: link.ID, FeatureID: a.FeatureID, Status: status, Error: errStr, Pushed: pushed, Pulled: pulled},
		}}, serr
	}
	return s.SyncProject(ctx, a.ProjectID)
}
