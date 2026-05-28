# Plane Comment Sync (SYNC v1) -- Design

Date: 2026-05-28
Branch: `feat/plane-comment-sync`
Status: approved (pre-implementation)

## Problem

Featmap is the discovery layer of IRL's delivery pipeline (idea -> map -> Plane
-> Gitea/GitHub). Today a discussion about a card happens in two places: on the
Featmap card and on the corresponding Plane work item. People re-post the same
thing in both, or one side silently goes stale. There is no link between a
Featmap card and a Plane work item, and no comment sync.

This is the #1 workflow-critical reason the fork exists. The roadmap captures it
as 15 `SYNC-*` cards (project `5f0687bb-403d-4c1a-88bb-7e2cb6b864ad`, column 8).

## Goal

Mirror comments both directions between a linked Featmap card and a Plane work
item, idempotently and without echo loops, driveable from every surface
(manual / background poll / REST / MCP / CLI). v1 couples **comments only** --
no card is created in Plane, no other field syncs.

### Scope decision (locked)

**Backend / agentic first. Zero React.** The 6 UI cards (SYNC-001/002 settings
panel, 010 link field, 011 badge, 030 button, 040 status panel) are deferred
until after the PLT-001 Vite/React 19 migration, per the roadmap's
UI-after-migration sequencing. Everything in this spec is driveable today via
REST / MCP / CLI. The connection + link config that the UI cards would expose is
delivered here as REST + MCP endpoints; only the visual surfaces wait.

### Out of scope (v1)

- **Comment EDIT sync.** v1 is create-mirror only. Editing an already-imported
  comment does not propagate. This is *safe* (see Echo prevention) -- it simply
  doesn't sync the edit. Deferred to an icebox card (see Follow-ups).
- Card creation/push to Plane; any non-comment field sync.
- Markdown <-> HTML conversion fidelity (v1 stores Plane `comment_html` into the
  Featmap `post` and pushes Featmap markdown as-is; acceptable if it renders
  decently in both UIs). Deferred (see Follow-ups).
- All 6 UI cards (above).

## Plane API ground truth (researched 2026-05-28)

- Base URL: `https://api.plane.so/` (cloud); self-hosted instances use a custom
  domain -- hence the per-connection `base_url` field.
- Auth: **`X-API-Key: <key>` header** (NOT `Authorization: Bearer`). OAuth bearer
  is an alternative the API also accepts, but API-key is what we use.
- Connection test: `GET /api/v1/users/me/`.
- Work-item comments: `GET`/`POST`
  `/api/v1/workspaces/{slug}/projects/{project_id}/work-items/{wi_id}/comments/`.
  (Plane is migrating `issues` -> `work-items`; EOL for `issues` is 2026-03-31,
  so use `work-items`.)
- Comment fields: `id` (uuid), `created_at`, `updated_at`, `comment_html`,
  `comment_stripped`, `actor`, `created_by`, `updated_by`.
- Pagination: cursor-based -- `per_page` (default/max 100), `cursor`
  (`value:offset:is_prev`), response carries `next_cursor` + `next_page_results`.
- **No server-side `updated_at` filter / ordering on the comment list.**
- Rate limit: **60 requests/minute**; `X-RateLimit-Remaining` / `X-RateLimit-Reset`
  (UTC epoch seconds) response headers.

### Design consequence of the API shape

SYNC-023's AC says "request only items changed since `last_pulled_cursor`."
Plane cannot do that server-side. So the pull path **fetches all comments for a
linked work item (paginated), dedupes by `plane_comment_id` via the comment-map,
and uses `last_pulled_cursor` as a client-side `updated_at` watermark** to skip
re-creating unchanged ones. The dedupe-by-external-id is the correctness
mechanism; the watermark is an optimization. Comment counts per card are tens,
not thousands, so fetch-all is cheap -- the 60/min rate limit is the real
constraint, which the 5-minute default poll interval respects.

## Architecture (the spine)

- **One new file `plane.go`** (flat `package main`, like `stripe.go`): the Plane
  REST client (`PlaneClient`) plus the sync engine logic that the service layer
  calls. Self-contained.
- **`SyncLink()` is the one brain.** Every trigger -- manual (SYNC-030),
  background poll (031), REST (032), MCP (033), CLI (034) -- calls the same
  `service.SyncProject(projectID)` which loops the project's links and calls
  `SyncLink(link)` on each. One code path, many doors. No logic duplication.
- **`SyncLink` does both directions per call:** push queued/failed local
  comments to Plane (SYNC-020), then pull Plane comments to local (SYNC-021),
  both gated by origin stamping (SYNC-022).
- **Poller (SYNC-031)** is the first background job in the codebase: a single
  goroutine started in `main.go` if >=1 connection exists, `time.NewTicker`,
  iterates active links calling `SyncLink`, respects rate-limit headers, one bad
  link never halts the others.

## Schema (one forward-only migration `24_plane_sync.up.sql`)

Postgres ENUM types for value safety (belt: PG rejects bad values; suspenders:
Go won't compile a typo). NOTE: this intentionally deviates from the existing
codebase convention (e.g. `features.color`/`status` are plain `varchar` +
app-level `colorIsValid()`); see the Follow-up to sweep the rest of the codebase.

```sql
CREATE TYPE plane_comment_origin AS ENUM ('featmap', 'plane');
CREATE TYPE plane_sync_status    AS ENUM ('ok', 'error', 'pending');

-- per-project Plane connection (SYNC-001/002/003)
plane_connections
  workspace_id      uuid not null
  project_id        uuid not null            -- featmap project
  id                uuid not null
  base_url          text not null            -- https://api.plane.so or self-host
  plane_workspace   text not null            -- Plane workspace slug
  api_key_cipher    bytea not null           -- AES-256-GCM ciphertext (SYNC-003)
  api_key_nonce     bytea not null           -- per-record GCM nonce
  api_key_hint      text not null            -- last-4 only, for display
  watched_projects  text not null default '' -- CSV of Plane project ids (SYNC-002)
  created_at        timestamptz not null
  last_modified     timestamptz not null
  primary key (workspace_id, id)
  unique (workspace_id, project_id)          -- one connection per featmap project

-- feature <-> Plane work item (SYNC-010/011/023/040)
plane_links
  workspace_id        uuid not null
  id                  uuid not null
  project_id          uuid not null
  feature_id          uuid not null
  plane_project_id    text not null
  plane_work_item_id  text not null
  last_pulled_cursor  text not null default ''   -- client-side updated_at watermark
  last_synced_at      timestamptz
  last_status         plane_sync_status not null default 'pending'
  last_error          text not null default ''
  primary key (workspace_id, id)
  unique (workspace_id, feature_id)              -- a card links to <=1 work item

-- comment dedupe + origin (SYNC-020/021/022; edit-ready via plane_updated_at)
plane_comment_map
  workspace_id       uuid not null
  id                 uuid not null
  link_id            uuid not null
  featmap_comment_id uuid                         -- the local comment
  plane_comment_id   text                         -- null until pushed
  origin             plane_comment_origin not null
  plane_updated_at   timestamptz                  -- edit-readiness hook (R2)
  created_at         timestamptz not null
  primary key (workspace_id, id)
  unique (workspace_id, plane_comment_id)         -- dedupe-by-external-id (SYNC-022)
```

`api_key_cipher` / `api_key_nonce` are never serialized into any JSON response,
MCP payload, or log line (SYNC-003); only `api_key_hint` is shown.

## Credential encryption (SYNC-003)

AES-256-GCM with a dedicated `planeEncryptionKey` conf.json field (32-byte
base64), separate from `jwtSecret` so rotating it does not invalidate sessions.
Encrypt the Plane API key before storing the ciphertext + per-record nonce;
decrypt in-memory only when constructing a `PlaneClient`. Stdlib
`crypto/aes` + `crypto/cipher`, no external dependency. A DB dump leaks only
ciphertext. If `planeEncryptionKey` is absent, the Plane integration is disabled
(connections cannot be created) rather than storing plaintext.

## Sync engine (`SyncLink`, both directions)

`PlaneClient` (in `plane.go`): `X-API-Key` header; `TestConnection()`,
`ListComments(workItemID, cursor)` (follows `next_cursor` to completion),
`CreateComment(workItemID, html)`. Reads `X-RateLimit-Remaining`; on HTTP 429
backs off until `X-RateLimit-Reset`.

```
service.SyncLink(link):

  PUSH (featmap -> Plane)  SYNC-020/022:
    1. load plane_comment_map rows for link
    2. push set = feature's comments with NO map row
         (a pulled comment already has origin='plane' row -> excluded; a failed
          earlier push has an origin='featmap' row with plane_comment_id NULL ->
          included for retry)
    3. for each: POST to Plane
         success: upsert map {origin:'featmap', featmap_comment_id,
                   plane_comment_id, plane_updated_at}
         failure: leave plane_comment_id NULL (stays queued), record error,
                   CONTINUE -- never block/lose the local comment, never panic

  PULL (Plane -> featmap)  SYNC-021/022:
    4. ListComments(wid) paginated -> all Plane comments
    5. for each Plane comment:
         skip if plane_comment_id already in map        (echo- + edit-safe)
         skip if updated_at <= link.last_pulled_cursor  (watermark optimization)
         else create featmap comment (CreatedByName = "Plane: <actor>") and
              upsert map {origin:'plane', featmap_comment_id, plane_comment_id,
              plane_updated_at}
    6. advance link.last_pulled_cursor = max(updated_at seen) ONLY on full pull
       success (a failure leaves it unmoved so nothing is skipped) SYNC-023
    7. write last_synced_at / last_status / last_error  SYNC-040
```

### Echo prevention (SYNC-022, load-bearing)

- Push set = comments with **no map row**; a pulled comment is mapped
  (origin='plane') so it is never pushed back.
- Pull skips any `plane_comment_id` already in the map; a pushed comment (whose
  returned id we recorded) is never re-imported.
- Idempotent **by external id, not by text** -- survives any number of runs:
  exactly one copy on each side.

Provenance and dedupe are the SAME mechanism: "did this come from Plane?" =
`feature_comments LEFT JOIN plane_comment_map ON featmap_comment_id`, check
`origin`. No `origin` column is added to the existing `feature_comments` table;
`CreatedByName = "Plane: <actor>"` is cosmetic attribution only.

### Transaction safety

All writes go through the request's repo tx. Per-comment failures are captured
and the loop continues; handlers never panic mid-write (the `mware.go`
always-commit gotcha -- a panic would commit partial state). Mirrors the
established bulk-tool discipline.

## Triggers (one brain, many doors)

All call `service.SyncProject(projectID)` -> loop links -> `SyncLink`.

- **SYNC-030 Manual** (service method now; UI deferred): per-link `SyncLink`,
  debounced via an in-memory `sync.Map` of in-flight link ids (a second
  concurrent call for the same link no-ops).
- **SYNC-031 Poller:** goroutine in `main.go`, `time.NewTicker(interval)` from
  conf `planePollInterval` (default `"5m"`, `"0"`/empty = disabled). Sequential
  link iteration, per-link error isolated, rate-limit headers respected. Started
  only if >=1 connection exists. Hitting the REST endpoint on an external cron
  with the poller disabled yields identical behavior.
- **SYNC-032 REST:** `POST /v1/projects/{id}/plane/sync` in the `workspaceAPI`
  group (existing bearer auth), optional `?feature_id=` to scope to one link.
  Returns `{pushed, pulled, perLink:[{linkId, status, error}]}`. 401 unauth /
  409 misconfigured, never 500.
- **SYNC-033 MCP:** `plane_sync(workspace_id, project_id, feature_id?)` tool ->
  same service -> structured result. Auto-registered via `add()` (registration
  guard covers it); failures via returned error, never panic. Takes the fork to
  51 tools.
- **SYNC-034 CLI:** `featmap plane sync --project <id> [--feature <id>] [--json]`
  -- thin wrapper over the REST endpoint; human output by default, `--json` for
  machines; non-zero exit on failure. New subcommand in the binary's arg
  handling.

Also delivered (config surface the deferred UI would drive):
- **SYNC-001/002 connection + watched-projects config** as REST
  (`POST/PUT/GET /v1/projects/{id}/plane/connection`) + MCP tools
  (`set_plane_connection`, `get_plane_connection`, `test_plane_connection`),
  including the live "test connection" call. (Exact tool names finalized in the
  plan.)
- **SYNC-010 link** as REST + MCP (`link_feature_to_plane`,
  `unlink_feature_from_plane`), validating the work item exists via the Plane API.

## Error handling

- Plane unreachable / 401 / 429 -> `link.last_status='error'` + `last_error`
  recorded; local data untouched; cursor unmoved; user never blocked
  (SYNC-020/040). 429 -> back off, respect `X-RateLimit-Reset`.
- Missing / misconfigured connection -> REST 409; MCP/CLI clear machine-readable
  error.
- Connection test (SYNC-001) -> specific 401 / unreachable / malformed-URL,
  never generic 500.
- Credential decryption failure -> surfaced as a connection error; key material
  never logged (SYNC-003).
- Every enum write validated by `Valid()` before the query (and rejected by PG
  if it somehow slips).
- All mutations through the repo tx; per-item failures captured and continued;
  never panic mid-write.

## Testing (testcontainers Postgres; handlers/services called directly)

- **`PlaneClient` against an `httptest.Server`** faking Plane: comment
  list/create, 401, 429 + reset header, multi-page pagination. No real Plane.
- **Echo-prevention regression:** push-then-pull does not re-import own comment;
  pull-then-push does not re-send origin='plane' comment; N runs -> exactly one
  copy each side.
- **Cursor:** advances only on full pull success; a mid-pull failure leaves it
  unmoved (nothing skipped on the next run).
- **Crypto:** encrypt -> store -> load -> decrypt round-trips; ciphertext !=
  plaintext; hint = last 4; missing key disables connection creation.
- **Enums:** `Valid()` unit tests; a bad enum value is rejected by PG.
- **Per-link isolation:** one link erroring in `SyncProject` does not abort the
  others; each link's status reflects its own outcome.
- **MCP `plane_sync`** (+ connection/link tools): registered (registration
  guard) and client-safe (the `TestMCPOutputSchemasAreClientSafe` guard).

## Verification (live, after build)

With the docker stack rebuilt and `/mcp` reconnected, against a real Plane
project: set a connection, link a card, post a Featmap comment -> appears on the
Plane work item; post a Plane comment -> appears on the card after `plane_sync`;
run sync repeatedly -> no duplicates, no echo. Confirm a DB dump shows only
ciphertext for the key.

## Follow-ups (deferred -- capture as icebox cards during implementation)

1. **Sync comment EDITS (settling-window conflict resolution).** Plane emits
   `updated_at` on edit, and `plane_comment_map.plane_updated_at` already stores
   it, so edit detection is `plane.updated_at > map.plane_updated_at`. The hard
   part is the bidirectional write-write conflict / lost-update race (sync acts
   on stale data just after an edit on the other side -- a stale-read/TOCTOU
   race; game netcode uses rollback, inapplicable here). Mitigation: a
   quiescence / settling window (skip reconciling a record edited within the
   last X seconds). Real feature, not a tweak -> R2/icebox.
2. **Codebase value-safety sweep.** Audit the codebase for places that would
   benefit from the same value safety applied here -- e.g. replace plain
   `varchar` + app-level validators (`features.color`, `features.status`,
   subworkflow/workflow/milestone color+status, etc.) with Postgres ENUM types +
   Go typed-string consts. This spec introduces the pattern; the sweep applies it
   consistently.
3. **Markdown <-> HTML conversion fidelity** between Featmap `post` (markdown)
   and Plane `comment_html`. v1 passes content through as-is.

## Story coverage

SYNC-001 connection config (REST/MCP) | SYNC-002 watched projects | SYNC-003
AES-256-GCM cred encryption | SYNC-010 link (REST/MCP) | SYNC-011 badge -> UI,
deferred (data via SYNC-040 fields) | SYNC-020 push | SYNC-021 pull | SYNC-022
echo prevention | SYNC-023 sync-state schema + watermark | SYNC-030 manual
(service now, UI deferred) | SYNC-031 poller | SYNC-032 REST | SYNC-033 MCP |
SYNC-034 CLI | SYNC-040 status fields (data now, panel UI deferred).
