package main

// Plane integration: REST client, credential crypto, and the value-typed enums
// backing plane_comment_origin / plane_sync_status. The sync engine itself lives
// on the service (service.go); this file is the Plane-facing edge + helpers.

// CommentOrigin mirrors the plane_comment_origin Postgres enum.
type CommentOrigin string

const (
	OriginFeatmap CommentOrigin = "featmap"
	OriginPlane   CommentOrigin = "plane"
)

func (o CommentOrigin) Valid() bool { return o == OriginFeatmap || o == OriginPlane }

// SyncStatus mirrors the plane_sync_status Postgres enum.
type SyncStatus string

const (
	StatusOK      SyncStatus = "ok"
	StatusError   SyncStatus = "error"
	StatusPending SyncStatus = "pending"
)

func (s SyncStatus) Valid() bool {
	return s == StatusOK || s == StatusError || s == StatusPending
}
