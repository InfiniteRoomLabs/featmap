# Plane Comment Sync (SYNC v1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mirror comments both directions between a linked Featmap card and a Plane work item -- idempotently, echo-safe, driveable from manual / poll / REST / MCP / CLI -- with zero React (backend/agentic only).

**Architecture:** A new `plane.go` holds the Plane REST client + AES-256-GCM credential crypto. Three new tables (`plane_connections`, `plane_links`, `plane_comment_map`) with Postgres ENUMs carry connection, link, and comment-dedupe state. `service.SyncLink()` is the one engine every trigger calls: push local->Plane comments, pull Plane->local, dedupe by external id. Triggers: a background poller goroutine, a REST endpoint, MCP tools, and a CLI subcommand.

**Tech Stack:** Go 1.25, chi router, sqlx, golang-migrate (go-bindata embedded), `crypto/aes`+`crypto/cipher` (stdlib), `net/http` Plane client, testcontainers-go (Postgres) + `httptest` (fake Plane) for tests. No new external dependency.

---

## Context the engineer needs (read before starting)

- **Flat `package main`** in repo root. New code = new top-level `*.go` file. Build/test with `.` NOT `./...` (gitignored `data/` dir breaks the glob).
- **`git add` and `git commit` are SEPARATE Bash commands** (a hook enforces it; never `&&`-chain).
- **The always-commit gotcha:** `mware.go Transaction()` commits regardless of handler outcome. Repo writes use `tx.MustExec` (PANICS on SQL error). A panic mid-write commits partial state. **Every sync mutation must return errors, never panic.** For loops, capture per-item errors and continue (mirror `runBulkTx` discipline in `mcp_bulk.go`).
- **Migrations are forward-only**, in `migrations/*.up.sql`, embedded via go-bindata. Latest is `23_api_keys.up.sql`. After adding `24_*.up.sql` you MUST run `./build/generate.sh` to regenerate `migrations/bindata.go`, or the new migration won't be embedded. (generate.sh needs `go-bindata` v4 on PATH: `go install github.com/kevinburke/go-bindata/v4/...@v4.0.2`.)
- **Service is an interface** (`service.go`): a new method = add to the `Service` interface block AND the `*service` impl. Per-request state: `s.Member.WorkspaceID`, `s.Acc.Name`. Workspace scoping uses `s.Member.WorkspaceID` in every repo call.
- **Repository is an interface** (`repo.go`): new method = add to `Repository` interface block AND `*repo` impl. Reads use `a.tx.Get`/`a.tx.Select`; writes use `a.tx.MustExec`. `errors` in repo.go is `github.com/pkg/errors` (use `errors.Wrap`).
- **REST handler pattern** (`workspace-api.go`): `func h(w http.ResponseWriter, r *http.Request)` calling `GetEnv(r).Service.X()`; respond with `render.JSON(w, r, value)` or on error `render.Render(w, r, ErrInvalidRequest(err))`. Request bodies: a struct with a `Bind(r) error` method + `render.Bind(r, data)`.
- **MCP tool pattern** (`mcp.go`): package-level `func mcpXxx(ctx context.Context, s Service, a XxxArgs) (Out, error)`, registered in `buildMCPServer()` via `add(srv, "name", "desc", func(a XxxArgs) string { return a.WorkspaceID }, mcpXxx)`. `add` wraps with auth + `resolveWorkspace`. Two static guards run DB-free under `SKIP_DB_TESTS=1`: `TestMCPRegistrationCompleteness` (every handler registered) and `TestMCPOutputSchemasAreClientSafe` (no `any`-field boolean subschema -- if a tool returns caller-shaped data, give the HANDLER an `any` return type, not a struct with an `any` field).
- **Test harness** (`mcp_testmain_test.go`, `mcp_helpers_test.go`): `runInTx(t, func(t, ctx, s, acc, ws, member){...})` gives a service bound to a rolled-back tx; `newProjectFixture(t, s)` builds a project/milestones/workflow/subworkflows/features/persona/comment. `SKIP_DB_TESTS=1` skips the DB harness. Run a single test: `go test -run '^TestName$' .`
- **conf load** (`main.go` `readConfiguration()`): opens `conf.json`, unmarshals into `Configuration` struct. Add new fields there. The runtime `conf.json` is gitignored scratch at repo root; `config/conf.json` is the sample.
- **`main()` flow:** reads config -> connects DB -> applies migrations -> builds router -> `ListenAndServe`. The poller goroutine starts after migrations; the CLI subcommand branch goes at the very top of `main()` before the router.
- **Rebuild the running container** to load changes: `docker compose up -d --build` from repo root, then the USER runs `/mcp` to reconnect (no Claude restart needed for new MCP tools).

## File structure

- **Create `plane.go`** -- `PlaneClient` (REST wrapper, rate-limit aware), credential crypto (`encryptPlaneKey`/`decryptPlaneKey`), and the enum typed-string consts. One cohesive integration unit, like `stripe.go`.
- **Create `plane_test.go`** -- crypto round-trip + `PlaneClient` against `httptest.Server`.
- **Create `migrations/24_plane_sync.up.sql`** -- 2 enum types + 3 tables.
- **Create `plane-api.go`** -- REST handlers (connection CRUD, link/unlink, sync trigger).
- **Create `plane_sync_test.go`** -- `SyncLink`/`SyncProject` engine tests (echo, cursor, isolation) against fake Plane + real PG.
- **Modify `model.go`** -- `PlaneConnection`, `PlaneLink`, `PlaneCommentMap` structs.
- **Modify `repo.go`** -- CRUD for the 3 tables.
- **Modify `service.go`** -- connection/link management + `SyncLink`/`SyncProject`.
- **Modify `mcp.go`** -- register the Plane MCP tools (in `mcp_reads.go`-style new file `mcp_plane.go` to keep `mcp.go` lean).
- **Create `mcp_plane.go`** -- Plane MCP tool handlers + arg/result structs.
- **Modify `main.go`** -- conf fields, poller goroutine, CLI subcommand branch.
- **Modify `readme.md`, `CLAUDE.md`, `CHANGELOG.md`** -- docs (50 -> N tools, Plane section).

---

## Task 1: Schema + enums + models

**Files:**
- Create: `migrations/24_plane_sync.up.sql`
- Modify: `model.go`, `plane.go` (new, enum consts only this task)
- Regenerate: `migrations/bindata.go` via `./build/generate.sh`

- [ ] **Step 1: Write the migration**

Create `migrations/24_plane_sync.up.sql`:

```sql
CREATE TYPE plane_comment_origin AS ENUM ('featmap', 'plane');
CREATE TYPE plane_sync_status AS ENUM ('ok', 'error', 'pending');

CREATE TABLE plane_connections (
    workspace_id     uuid NOT NULL,
    id               uuid NOT NULL,
    project_id       uuid NOT NULL,
    base_url         text NOT NULL,
    plane_workspace  text NOT NULL,
    api_key_cipher   bytea NOT NULL,
    api_key_nonce    bytea NOT NULL,
    api_key_hint     text NOT NULL,
    watched_projects text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL,
    last_modified    timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, project_id)
);

CREATE TABLE plane_links (
    workspace_id       uuid NOT NULL,
    id                 uuid NOT NULL,
    project_id         uuid NOT NULL,
    feature_id         uuid NOT NULL,
    plane_project_id   text NOT NULL,
    plane_work_item_id text NOT NULL,
    last_pulled_cursor text NOT NULL DEFAULT '',
    last_synced_at     timestamptz,
    last_status        plane_sync_status NOT NULL DEFAULT 'pending',
    last_error         text NOT NULL DEFAULT '',
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, feature_id)
);

CREATE TABLE plane_comment_map (
    workspace_id       uuid NOT NULL,
    id                 uuid NOT NULL,
    link_id            uuid NOT NULL,
    featmap_comment_id uuid,
    plane_comment_id   text,
    origin             plane_comment_origin NOT NULL,
    plane_updated_at   timestamptz,
    created_at         timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, plane_comment_id)
);
```

- [ ] **Step 2: Add model structs**

In `model.go`, after the `FeatureCommentOwner` struct, add:

```go
// PlaneConnection -- per-project link to a Plane instance (SYNC-001/002/003).
type PlaneConnection struct {
	WorkspaceID     string    `db:"workspace_id" json:"workspaceId"`
	ID              string    `db:"id" json:"id"`
	ProjectID       string    `db:"project_id" json:"projectId"`
	BaseURL         string    `db:"base_url" json:"baseUrl"`
	PlaneWorkspace  string    `db:"plane_workspace" json:"planeWorkspace"`
	APIKeyCipher    []byte    `db:"api_key_cipher" json:"-"`
	APIKeyNonce     []byte    `db:"api_key_nonce" json:"-"`
	APIKeyHint      string    `db:"api_key_hint" json:"apiKeyHint"`
	WatchedProjects string    `db:"watched_projects" json:"watchedProjects"`
	CreatedAt       time.Time `db:"created_at" json:"createdAt"`
	LastModified    time.Time `db:"last_modified" json:"lastModified"`
}

// PlaneLink -- a feature card linked to a Plane work item (SYNC-010/023/040).
type PlaneLink struct {
	WorkspaceID     string     `db:"workspace_id" json:"workspaceId"`
	ID              string     `db:"id" json:"id"`
	ProjectID       string     `db:"project_id" json:"projectId"`
	FeatureID       string     `db:"feature_id" json:"featureId"`
	PlaneProjectID  string     `db:"plane_project_id" json:"planeProjectId"`
	PlaneWorkItemID string     `db:"plane_work_item_id" json:"planeWorkItemId"`
	LastPulledCursor string    `db:"last_pulled_cursor" json:"lastPulledCursor"`
	LastSyncedAt    *time.Time `db:"last_synced_at" json:"lastSyncedAt"`
	LastStatus      string     `db:"last_status" json:"lastStatus"`
	LastError       string     `db:"last_error" json:"lastError"`
}

// PlaneCommentMap -- maps Featmap<->Plane comments + origin (SYNC-020/021/022).
type PlaneCommentMap struct {
	WorkspaceID      string     `db:"workspace_id" json:"workspaceId"`
	ID               string     `db:"id" json:"id"`
	LinkID           string     `db:"link_id" json:"linkId"`
	FeatmapCommentID *string    `db:"featmap_comment_id" json:"featmapCommentId"`
	PlaneCommentID   *string    `db:"plane_comment_id" json:"planeCommentId"`
	Origin           string     `db:"origin" json:"origin"`
	PlaneUpdatedAt   *time.Time `db:"plane_updated_at" json:"planeUpdatedAt"`
	CreatedAt        time.Time  `db:"created_at" json:"createdAt"`
}
```

(`time` is already imported in model.go.)

- [ ] **Step 3: Create `plane.go` with the enum consts**

Create `plane.go`:

```go
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
```

- [ ] **Step 4: Regenerate bindata and build**

Run:
```bash
./build/generate.sh && go build .
```
Expected: builds clean. `migrations/bindata.go` now embeds `24_plane_sync.up.sql`. If `generate.sh` errors with `go-bindata: command not found`, install it: `go install github.com/kevinburke/go-bindata/v4/...@v4.0.2` then retry.

- [ ] **Step 5: Verify the migration applies (testcontainers)**

Run:
```bash
go test -run '^TestMain$' . 2>&1 | tail -5 || go test -run '^Test_mcpGetFeature$' .
```
Expected: PASS -- `Test_mcpGetFeature` (or any DB test) forces `TestMain` to apply all migrations including `24`. A bad enum/table SQL fails migration here.

- [ ] **Step 6: Commit**

```bash
git add migrations/24_plane_sync.up.sql migrations/bindata.go model.go plane.go
git commit -m "feat(plane): schema + enums + models for comment sync"
```

---

## Task 2: Credential crypto (SYNC-003)

**Files:**
- Modify: `plane.go` (add crypto funcs), `main.go` (conf field)
- Create/modify: `plane_test.go`

- [ ] **Step 1: Add the conf field**

In `main.go`, in the `Configuration` struct, after `JWTSecret`, add:

```go
	PlaneEncryptionKey  string `json:"planeEncryptionKey"`
	PlanePollInterval   string `json:"planePollInterval"`
```

Add both to the sample `config/conf.json` too (empty strings):

```json
  "planeEncryptionKey": "",
  "planePollInterval": "5m",
```

- [ ] **Step 2: Write the failing crypto test**

Create `plane_test.go`:

```go
package main

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func testKeyB64(t *testing.T) string {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return base64.StdEncoding.EncodeToString(k)
}

func Test_planeKeyCrypto_roundtrip(t *testing.T) {
	key := testKeyB64(t)
	plaintext := "plane_api_key_abc123"

	cipher, nonce, err := encryptPlaneKey(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if string(cipher) == plaintext {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := decryptPlaneKey(key, cipher, nonce)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("roundtrip mismatch: got %q want %q", got, plaintext)
	}
}

func Test_planeKeyCrypto_missingKey(t *testing.T) {
	if _, _, err := encryptPlaneKey("", "x"); err == nil {
		t.Fatal("expected error for empty encryption key")
	}
}

func Test_planeKeyCrypto_wrongKeyFails(t *testing.T) {
	cipher, nonce, err := encryptPlaneKey(testKeyB64(t), "secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := decryptPlaneKey(testKeyB64(t), cipher, nonce); err == nil {
		t.Fatal("expected decrypt failure with wrong key")
	}
}
```

- [ ] **Step 2b: Run it, expect FAIL**

Run: `SKIP_DB_TESTS=1 go test -run '^Test_planeKeyCrypto' .`
Expected: FAIL -- `encryptPlaneKey`/`decryptPlaneKey` undefined.

- [ ] **Step 3: Implement the crypto in `plane.go`**

Add to `plane.go` imports and body:

```go
import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// decodeKey turns the base64 conf key into a 32-byte AES-256 key.
func decodeKey(b64 string) ([]byte, error) {
	if b64 == "" {
		return nil, errors.New("planeEncryptionKey not configured")
	}
	k, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("planeEncryptionKey is not valid base64")
	}
	if len(k) != 32 {
		return nil, errors.New("planeEncryptionKey must decode to 32 bytes (AES-256)")
	}
	return k, nil
}

// encryptPlaneKey AES-256-GCM encrypts a Plane API key. Returns ciphertext +
// the per-record nonce. The encryption key is the base64 conf value.
func encryptPlaneKey(keyB64, plaintext string) (ciphertext, nonce []byte, err error) {
	key, err := decodeKey(keyB64)
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return ciphertext, nonce, nil
}

// decryptPlaneKey reverses encryptPlaneKey.
func decryptPlaneKey(keyB64 string, ciphertext, nonce []byte) (string, error) {
	key, err := decodeKey(keyB64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("failed to decrypt plane key (wrong key or corrupt data)")
	}
	return string(plaintext), nil
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `SKIP_DB_TESTS=1 go test -run '^Test_planeKeyCrypto' .`
Expected: PASS (all 3).

- [ ] **Step 5: Commit**

```bash
git add plane.go plane_test.go main.go config/conf.json
git commit -m "feat(plane): AES-256-GCM credential crypto + conf keys"
```

---

## Task 3: Plane REST client (`PlaneClient`)

**Files:**
- Modify: `plane.go`, `plane_test.go`

- [ ] **Step 1: Write failing client tests (fake Plane via httptest)**

Append to `plane_test.go`:

```go
import (
	"net/http"
	"net/http/httptest"
)

func Test_PlaneClient_listComments_paginates(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "k" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if page == 0 {
			page++
			_, _ = w.Write([]byte(`{"results":[{"id":"c1","comment_html":"<p>a</p>","updated_at":"2026-05-01T00:00:00Z","actor":"u1"}],"next_cursor":"x","next_page_results":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"id":"c2","comment_html":"<p>b</p>","updated_at":"2026-05-02T00:00:00Z","actor":"u2"}],"next_cursor":"","next_page_results":false}`))
	}))
	defer srv.Close()

	c := &PlaneClient{BaseURL: srv.URL, APIKey: "k", PlaneWorkspace: "ws", HTTP: srv.Client()}
	comments, err := c.ListComments("proj", "wi")
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments across pages, got %d", len(comments))
	}
	if comments[0].ID != "c1" || comments[1].ID != "c2" {
		t.Fatalf("unexpected ids: %+v", comments)
	}
}

func Test_PlaneClient_createComment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":"new1","comment_html":"<p>hi</p>","updated_at":"2026-05-03T00:00:00Z","actor":"me"}`))
	}))
	defer srv.Close()

	c := &PlaneClient{BaseURL: srv.URL, APIKey: "k", PlaneWorkspace: "ws", HTTP: srv.Client()}
	cm, err := c.CreateComment("proj", "wi", "<p>hi</p>")
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if cm.ID != "new1" {
		t.Fatalf("expected id new1, got %q", cm.ID)
	}
}

func Test_PlaneClient_testConnection_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	c := &PlaneClient{BaseURL: srv.URL, APIKey: "bad", HTTP: srv.Client()}
	if err := c.TestConnection(); err == nil {
		t.Fatal("expected 401 error from TestConnection")
	}
}
```

- [ ] **Step 1b: Run, expect FAIL**

Run: `SKIP_DB_TESTS=1 go test -run '^Test_PlaneClient' .`
Expected: FAIL -- `PlaneClient` undefined.

- [ ] **Step 2: Implement `PlaneClient` in `plane.go`**

Add imports `encoding/json`, `fmt`, `net/http`, `net/url`, `strings`, `time`, `strconv` and:

```go
// PlaneComment is the subset of a Plane work-item comment we use.
type PlaneComment struct {
	ID          string    `json:"id"`
	CommentHTML string    `json:"comment_html"`
	UpdatedAt   time.Time `json:"updated_at"`
	Actor       string    `json:"actor"`
}

// PlaneClient is a thin Plane REST client. Auth via the X-API-Key header.
type PlaneClient struct {
	BaseURL        string // e.g. https://api.plane.so
	APIKey         string
	PlaneWorkspace string // workspace slug
	HTTP           *http.Client
}

func (c *PlaneClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *PlaneClient) do(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient().Do(req)
}

// TestConnection calls GET /api/v1/users/me/ (SYNC-001).
func (c *PlaneClient) TestConnection() error {
	resp, err := c.do("GET", "/api/v1/users/me/", nil)
	if err != nil {
		return fmt.Errorf("plane unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return errors.New("plane rejected the API key (401)")
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("plane returned status %d", resp.StatusCode)
	}
	return nil
}

type planeCommentList struct {
	Results         []PlaneComment `json:"results"`
	NextCursor      string         `json:"next_cursor"`
	NextPageResults bool           `json:"next_page_results"`
}

func (c *PlaneClient) commentsPath(planeProjectID, workItemID string) string {
	return fmt.Sprintf("/api/v1/workspaces/%s/projects/%s/work-items/%s/comments/",
		url.PathEscape(c.PlaneWorkspace), url.PathEscape(planeProjectID), url.PathEscape(workItemID))
}

// ListComments fetches all comments for a work item, following cursor pages.
func (c *PlaneClient) ListComments(planeProjectID, workItemID string) ([]PlaneComment, error) {
	var all []PlaneComment
	cursor := ""
	for {
		p := c.commentsPath(planeProjectID, workItemID) + "?per_page=100"
		if cursor != "" {
			p += "&cursor=" + url.QueryEscape(cursor)
		}
		resp, err := c.do("GET", p, nil)
		if err != nil {
			return nil, fmt.Errorf("plane unreachable: %w", err)
		}
		if resp.StatusCode == 429 {
			resp.Body.Close()
			return nil, c.rateLimited(resp)
		}
		if resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, fmt.Errorf("plane list comments status %d", resp.StatusCode)
		}
		var page planeCommentList
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()
		all = append(all, page.Results...)
		if !page.NextPageResults || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return all, nil
}

// CreateComment posts a comment (comment_html) and returns the created comment.
func (c *PlaneClient) CreateComment(planeProjectID, workItemID, html string) (*PlaneComment, error) {
	payload, _ := json.Marshal(map[string]string{"comment_html": html})
	resp, err := c.do("POST", c.commentsPath(planeProjectID, workItemID), strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("plane unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		return nil, c.rateLimited(resp)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("plane create comment status %d", resp.StatusCode)
	}
	var cm PlaneComment
	if err := json.NewDecoder(resp.Body).Decode(&cm); err != nil {
		return nil, err
	}
	return &cm, nil
}

func (c *PlaneClient) rateLimited(resp *http.Response) error {
	reset := resp.Header.Get("X-RateLimit-Reset")
	if reset != "" {
		if epoch, err := strconv.ParseInt(reset, 10, 64); err == nil {
			return fmt.Errorf("plane rate limited; resets at %s", time.Unix(epoch, 0).UTC().Format(time.RFC3339))
		}
	}
	return errors.New("plane rate limited (429)")
}
```

(Note: `errors`, `io`, `base64`, `aes`, `cipher`, `rand` already imported from Task 2. Add only the new ones: `encoding/json`, `fmt`, `net/http`, `net/url`, `strings`, `time`, `strconv`. Remove duplicates.)

- [ ] **Step 3: Run tests, expect PASS**

Run: `SKIP_DB_TESTS=1 go test -run '^Test_PlaneClient' .`
Expected: PASS (all 3). Then `go build .` clean.

- [ ] **Step 4: Commit**

```bash
git add plane.go plane_test.go
git commit -m "feat(plane): REST client (test-conn, paginated list, create comment)"
```

---

## Task 4: Repo CRUD for the 3 tables

**Files:**
- Modify: `repo.go`

- [ ] **Step 1: Add interface declarations**

In `repo.go`, in the `Repository` interface block (near the other Find/Store/Get groups), add:

```go
	GetPlaneConnectionByProject(workspaceID string, projectID string) (*PlaneConnection, error)
	StorePlaneConnection(x *PlaneConnection)
	DeletePlaneConnection(workspaceID string, id string)
	FindPlaneConnections(workspaceID string) ([]*PlaneConnection, error)
	FindAllPlaneConnections() ([]*PlaneConnection, error)

	GetPlaneLink(workspaceID string, id string) (*PlaneLink, error)
	GetPlaneLinkByFeature(workspaceID string, featureID string) (*PlaneLink, error)
	FindPlaneLinksByProject(workspaceID string, projectID string) ([]*PlaneLink, error)
	StorePlaneLink(x *PlaneLink)
	DeletePlaneLink(workspaceID string, id string)

	FindPlaneCommentMapByLink(workspaceID string, linkID string) ([]*PlaneCommentMap, error)
	StorePlaneCommentMap(x *PlaneCommentMap)
```

- [ ] **Step 2: Add implementations**

In `repo.go`, near the end (before the closing of the file's method set), add:

```go
// --- Plane connections ---

func (a *repo) GetPlaneConnectionByProject(workspaceID, projectID string) (*PlaneConnection, error) {
	x := &PlaneConnection{}
	if err := a.tx.Get(x, "SELECT * FROM plane_connections WHERE workspace_id=$1 AND project_id=$2", workspaceID, projectID); err != nil {
		return nil, errors.Wrap(err, "not found")
	}
	return x, nil
}

func (a *repo) FindPlaneConnections(workspaceID string) ([]*PlaneConnection, error) {
	x := []*PlaneConnection{}
	if err := a.tx.Select(&x, "SELECT * FROM plane_connections WHERE workspace_id=$1", workspaceID); err != nil {
		return nil, errors.Wrap(err, "not found")
	}
	return x, nil
}

// FindAllPlaneConnections is for the poller, which runs outside a workspace scope.
func (a *repo) FindAllPlaneConnections() ([]*PlaneConnection, error) {
	x := []*PlaneConnection{}
	if err := a.tx.Select(&x, "SELECT * FROM plane_connections"); err != nil {
		return nil, errors.Wrap(err, "not found")
	}
	return x, nil
}

func (a *repo) StorePlaneConnection(x *PlaneConnection) {
	a.tx.MustExec(`INSERT INTO plane_connections
		(workspace_id, id, project_id, base_url, plane_workspace, api_key_cipher, api_key_nonce, api_key_hint, watched_projects, created_at, last_modified)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (workspace_id, id) DO UPDATE SET
		  base_url=$4, plane_workspace=$5, api_key_cipher=$6, api_key_nonce=$7, api_key_hint=$8, watched_projects=$9, last_modified=$11`,
		x.WorkspaceID, x.ID, x.ProjectID, x.BaseURL, x.PlaneWorkspace, x.APIKeyCipher, x.APIKeyNonce, x.APIKeyHint, x.WatchedProjects, x.CreatedAt, x.LastModified)
}

func (a *repo) DeletePlaneConnection(workspaceID, id string) {
	a.tx.MustExec("DELETE FROM plane_connections WHERE workspace_id=$1 AND id=$2", workspaceID, id)
}

// --- Plane links ---

func (a *repo) GetPlaneLink(workspaceID, id string) (*PlaneLink, error) {
	x := &PlaneLink{}
	if err := a.tx.Get(x, "SELECT * FROM plane_links WHERE workspace_id=$1 AND id=$2", workspaceID, id); err != nil {
		return nil, errors.Wrap(err, "not found")
	}
	return x, nil
}

func (a *repo) GetPlaneLinkByFeature(workspaceID, featureID string) (*PlaneLink, error) {
	x := &PlaneLink{}
	if err := a.tx.Get(x, "SELECT * FROM plane_links WHERE workspace_id=$1 AND feature_id=$2", workspaceID, featureID); err != nil {
		return nil, errors.Wrap(err, "not found")
	}
	return x, nil
}

func (a *repo) FindPlaneLinksByProject(workspaceID, projectID string) ([]*PlaneLink, error) {
	x := []*PlaneLink{}
	if err := a.tx.Select(&x, "SELECT * FROM plane_links WHERE workspace_id=$1 AND project_id=$2", workspaceID, projectID); err != nil {
		return nil, errors.Wrap(err, "not found")
	}
	return x, nil
}

func (a *repo) StorePlaneLink(x *PlaneLink) {
	a.tx.MustExec(`INSERT INTO plane_links
		(workspace_id, id, project_id, feature_id, plane_project_id, plane_work_item_id, last_pulled_cursor, last_synced_at, last_status, last_error)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (workspace_id, id) DO UPDATE SET
		  plane_project_id=$5, plane_work_item_id=$6, last_pulled_cursor=$7, last_synced_at=$8, last_status=$9, last_error=$10`,
		x.WorkspaceID, x.ID, x.ProjectID, x.FeatureID, x.PlaneProjectID, x.PlaneWorkItemID, x.LastPulledCursor, x.LastSyncedAt, x.LastStatus, x.LastError)
}

func (a *repo) DeletePlaneLink(workspaceID, id string) {
	a.tx.MustExec("DELETE FROM plane_links WHERE workspace_id=$1 AND id=$2", workspaceID, id)
}

// --- Plane comment map ---

func (a *repo) FindPlaneCommentMapByLink(workspaceID, linkID string) ([]*PlaneCommentMap, error) {
	x := []*PlaneCommentMap{}
	if err := a.tx.Select(&x, "SELECT * FROM plane_comment_map WHERE workspace_id=$1 AND link_id=$2", workspaceID, linkID); err != nil {
		return nil, errors.Wrap(err, "not found")
	}
	return x, nil
}

func (a *repo) StorePlaneCommentMap(x *PlaneCommentMap) {
	a.tx.MustExec(`INSERT INTO plane_comment_map
		(workspace_id, id, link_id, featmap_comment_id, plane_comment_id, origin, plane_updated_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (workspace_id, id) DO UPDATE SET
		  featmap_comment_id=$4, plane_comment_id=$5, origin=$6, plane_updated_at=$7`,
		x.WorkspaceID, x.ID, x.LinkID, x.FeatmapCommentID, x.PlaneCommentID, x.Origin, x.PlaneUpdatedAt, x.CreatedAt)
}
```

- [ ] **Step 3: Build (interface satisfaction check)**

Run: `go build .`
Expected: clean. If `*repo does not implement Repository`, an impl is missing/misnamed vs the interface decls.

- [ ] **Step 4: Commit**

```bash
git add repo.go
git commit -m "feat(plane): repo CRUD for connections, links, comment map"
```

---

## Task 5: Service -- connection management (SYNC-001/002/003)

**Files:**
- Modify: `service.go`
- Create/modify: `plane_sync_test.go`

- [ ] **Step 1: Add a config accessor the service needs**

The service reads `planeEncryptionKey` from config. `service.go` holds it in the unexported field `s.config` (set via `SetConfig`, returned via `GetConfig`). Use `s.config.PlaneEncryptionKey` (lowercase `config` -- verified in service.go).

- [ ] **Step 2: Add Service interface methods**

In `service.go` `Service` interface, add:

```go
	GetPlaneConnection(projectID string) (*PlaneConnection, error)
	SetPlaneConnection(projectID, baseURL, planeWorkspace, apiKey, watchedProjects string) (*PlaneConnection, error)
	TestPlaneConnection(projectID string) error
	planeClientForConnection(conn *PlaneConnection) (*PlaneClient, error)
```

(The lowercase `planeClientForConnection` is unexported -- it stays out of the interface. Put it as an unexported method on `*service` only, not in the interface. Keep only the three exported ones in the interface.)

- [ ] **Step 3: Implement in `service.go`**

```go
func (s *service) GetPlaneConnection(projectID string) (*PlaneConnection, error) {
	return s.r.GetPlaneConnectionByProject(s.Member.WorkspaceID, projectID)
}

func (s *service) SetPlaneConnection(projectID, baseURL, planeWorkspace, apiKey, watchedProjects string) (*PlaneConnection, error) {
	cipher, nonce, err := encryptPlaneKey(s.config.PlaneEncryptionKey, apiKey)
	if err != nil {
		return nil, err
	}
	hint := apiKey
	if len(hint) > 4 {
		hint = hint[len(hint)-4:]
	}
	now := time.Now().UTC()
	existing, _ := s.r.GetPlaneConnectionByProject(s.Member.WorkspaceID, projectID)
	id := newUUID()
	created := now
	if existing != nil {
		id = existing.ID
		created = existing.CreatedAt
	}
	conn := &PlaneConnection{
		WorkspaceID: s.Member.WorkspaceID, ID: id, ProjectID: projectID,
		BaseURL: baseURL, PlaneWorkspace: planeWorkspace,
		APIKeyCipher: cipher, APIKeyNonce: nonce, APIKeyHint: hint,
		WatchedProjects: watchedProjects, CreatedAt: created, LastModified: now,
	}
	s.r.StorePlaneConnection(conn)
	return conn, nil
}

func (s *service) planeClientForConnection(conn *PlaneConnection) (*PlaneClient, error) {
	key, err := decryptPlaneKey(s.config.PlaneEncryptionKey, conn.APIKeyCipher, conn.APIKeyNonce)
	if err != nil {
		return nil, err
	}
	return &PlaneClient{BaseURL: conn.BaseURL, APIKey: key, PlaneWorkspace: conn.PlaneWorkspace}, nil
}

func (s *service) TestPlaneConnection(projectID string) error {
	conn, err := s.r.GetPlaneConnectionByProject(s.Member.WorkspaceID, projectID)
	if err != nil {
		return errors.New("no plane connection for project")
	}
	c, err := s.planeClientForConnection(conn)
	if err != nil {
		return err
	}
	return c.TestConnection()
}
```

(Confirm `newUUID()` exists -- it's used across mcp.go. `errors` in service.go is stdlib `errors` or pkg/errors; match existing usage in the file. `time` is imported.)

- [ ] **Step 4: Write the failing test**

Create `plane_sync_test.go`:

```go
package main

import (
	"context"
	"testing"
)

func Test_SetGetPlaneConnection_encrypts(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member) {
		// service config must carry an encryption key for this test
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t)})
		fx := newProjectFixture(t, s)

		conn, err := s.SetPlaneConnection(fx.Project.ID, "https://api.plane.so", "ws-slug", "secret_key_1234", "p1,p2")
		mustOK(t, err, "SetPlaneConnection")
		if conn.APIKeyHint != "1234" {
			t.Fatalf("hint should be last 4, got %q", conn.APIKeyHint)
		}
		if string(conn.APIKeyCipher) == "secret_key_1234" {
			t.Fatal("api key stored in plaintext")
		}

		got, err := s.GetPlaneConnection(fx.Project.ID)
		mustOK(t, err, "GetPlaneConnection")
		if got.PlaneWorkspace != "ws-slug" || got.WatchedProjects != "p1,p2" {
			t.Fatalf("connection round-trip mismatch: %+v", got)
		}
	})
}
```

NOTE: `runInTx` currently sets config without a Plane key. Calling `s.SetConfig(...)` again inside the test overrides it. Confirm `SetConfig` is on the `Service` interface (it is -- used in `runInTx`).

- [ ] **Step 5: Run, expect PASS**

Run: `go test -run '^Test_SetGetPlaneConnection_encrypts$' .`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add service.go plane_sync_test.go
git commit -m "feat(plane): service connection management (set/get/test, encrypted)"
```

---

## Task 6: Service -- link management (SYNC-010)

**Files:**
- Modify: `service.go`, `plane_sync_test.go`

- [ ] **Step 1: Add interface methods**

In the `Service` interface:

```go
	LinkFeatureToPlane(featureID, planeProjectID, planeWorkItemID string) (*PlaneLink, error)
	UnlinkFeatureFromPlane(featureID string) error
	GetPlaneLinkByFeature(featureID string) (*PlaneLink, error)
	FindPlaneLinksByProject(projectID string) ([]*PlaneLink, error)
```

- [ ] **Step 2: Implement**

```go
func (s *service) GetPlaneLinkByFeature(featureID string) (*PlaneLink, error) {
	return s.r.GetPlaneLinkByFeature(s.Member.WorkspaceID, featureID)
}

func (s *service) FindPlaneLinksByProject(projectID string) ([]*PlaneLink, error) {
	return s.r.FindPlaneLinksByProject(s.Member.WorkspaceID, projectID)
}

func (s *service) LinkFeatureToPlane(featureID, planeProjectID, planeWorkItemID string) (*PlaneLink, error) {
	f, err := s.r.GetFeature(s.Member.WorkspaceID, featureID)
	if err != nil {
		return nil, errors.New("feature not found")
	}
	m, err := s.r.GetMilestone(s.Member.WorkspaceID, f.MilestoneID)
	if err != nil {
		return nil, errors.New("milestone not found")
	}
	existing, _ := s.r.GetPlaneLinkByFeature(s.Member.WorkspaceID, featureID)
	id := newUUID()
	if existing != nil {
		id = existing.ID
	}
	link := &PlaneLink{
		WorkspaceID: s.Member.WorkspaceID, ID: id, ProjectID: m.ProjectID,
		FeatureID: featureID, PlaneProjectID: planeProjectID, PlaneWorkItemID: planeWorkItemID,
		LastStatus: string(StatusPending),
	}
	s.r.StorePlaneLink(link)
	return link, nil
}

func (s *service) UnlinkFeatureFromPlane(featureID string) error {
	link, err := s.r.GetPlaneLinkByFeature(s.Member.WorkspaceID, featureID)
	if err != nil {
		return errors.New("link not found")
	}
	s.r.DeletePlaneLink(s.Member.WorkspaceID, link.ID)
	return nil
}
```

(`GetMilestone` exists on the repo -- it's used in `CreateFeatureCommentWithID`. `ProjectID` comes from the milestone, matching how comments derive project id.)

- [ ] **Step 3: Test**

Append to `plane_sync_test.go`:

```go
func Test_LinkUnlinkFeature(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member) {
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t)})
		fx := newProjectFixture(t, s)
		feat := fx.Features[0]

		link, err := s.LinkFeatureToPlane(feat.ID, "plane-proj", "WI-1")
		mustOK(t, err, "LinkFeatureToPlane")
		if link.PlaneWorkItemID != "WI-1" {
			t.Fatalf("bad link: %+v", link)
		}
		got, err := s.GetPlaneLinkByFeature(feat.ID)
		mustOK(t, err, "GetPlaneLinkByFeature")
		if got.ID != link.ID {
			t.Fatal("link id mismatch")
		}
		if err := s.UnlinkFeatureFromPlane(feat.ID); err != nil {
			t.Fatalf("unlink: %v", err)
		}
		if _, err := s.GetPlaneLinkByFeature(feat.ID); err == nil {
			t.Fatal("expected link gone after unlink")
		}
	})
}
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -run '^Test_LinkUnlinkFeature$' .`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add service.go plane_sync_test.go
git commit -m "feat(plane): service link/unlink feature to work item"
```

---

## Task 7: Service -- the sync engine (`SyncLink` + `SyncProject`)

This is the heart. Push local->Plane, pull Plane->local, dedupe by external id (SYNC-020/021/022/023/040).

**Files:**
- Modify: `service.go`, `plane_sync_test.go`

- [ ] **Step 1: Add interface methods + result type**

In `service.go` (near the Plane methods), add a result type ABOVE the interface or near other types:

```go
// SyncResult summarizes one sync run.
type SyncResult struct {
	Pushed  int              `json:"pushed"`
	Pulled  int              `json:"pulled"`
	PerLink []LinkSyncResult `json:"perLink"`
}

type LinkSyncResult struct {
	LinkID    string `json:"linkId"`
	FeatureID string `json:"featureId"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	Pushed    int    `json:"pushed"`
	Pulled    int    `json:"pulled"`
}
```

In the `Service` interface:

```go
	SyncProject(projectID string) (*SyncResult, error)
	SyncLink(link *PlaneLink) (pushed int, pulled int, err error)
```

- [ ] **Step 2: Implement `SyncLink`**

```go
func (s *service) SyncLink(link *PlaneLink) (int, int, error) {
	conn, err := s.r.GetPlaneConnectionByProject(s.Member.WorkspaceID, link.ProjectID)
	if err != nil {
		return 0, 0, errors.New("no plane connection for project")
	}
	client, err := s.planeClientForConnection(conn)
	if err != nil {
		return 0, 0, err
	}

	maps, err := s.r.FindPlaneCommentMapByLink(s.Member.WorkspaceID, link.ID)
	if err != nil {
		return 0, 0, err
	}
	// index by featmap comment id (push side) and plane comment id (pull side)
	mappedFeatmap := map[string]bool{}
	mappedPlane := map[string]bool{}
	for _, m := range maps {
		if m.FeatmapCommentID != nil && m.PlaneCommentID != nil {
			mappedFeatmap[*m.FeatmapCommentID] = true
		}
		if m.PlaneCommentID != nil {
			mappedPlane[*m.PlaneCommentID] = true
		}
	}

	pushed := 0
	// PUSH: featmap comments with no (featmap_comment_id -> plane_comment_id) map row.
	localComments := s.GetFeatureCommentsByFeature(link.FeatureID)
	for _, lc := range localComments {
		if mappedFeatmap[lc.ID] {
			continue // already pushed
		}
		// skip comments that ORIGINATED from plane (they have a map row with origin=plane)
		originatedFromPlane := false
		for _, m := range maps {
			if m.FeatmapCommentID != nil && *m.FeatmapCommentID == lc.ID && m.Origin == string(OriginPlane) {
				originatedFromPlane = true
				break
			}
		}
		if originatedFromPlane {
			continue
		}
		pc, perr := client.CreateComment(link.PlaneProjectID, link.PlaneWorkItemID, lc.Post)
		if perr != nil {
			// leave unmapped (stays queued); record and continue, never panic
			return pushed, 0, perr
		}
		pcID := pc.ID
		fcID := lc.ID
		s.r.StorePlaneCommentMap(&PlaneCommentMap{
			WorkspaceID: s.Member.WorkspaceID, ID: newUUID(), LinkID: link.ID,
			FeatmapCommentID: &fcID, PlaneCommentID: &pcID, Origin: string(OriginFeatmap),
			PlaneUpdatedAt: &pc.UpdatedAt, CreatedAt: time.Now().UTC(),
		})
		mappedPlane[pcID] = true
		pushed++
	}

	pulled := 0
	// PULL: all plane comments; skip mapped (echo/edit-safe) + watermark optimization.
	planeComments, perr := client.ListComments(link.PlaneProjectID, link.PlaneWorkItemID)
	if perr != nil {
		return pushed, pulled, perr
	}
	var maxSeen string
	for _, pc := range planeComments {
		if mappedPlane[pc.ID] {
			continue // already imported OR our own pushed comment -> echo-safe
		}
		// create local comment with plane attribution
		fc, cerr := s.CreateFeatureCommentWithID(newUUID(), link.FeatureID, pc.CommentHTML)
		if cerr != nil {
			return pushed, pulled, cerr
		}
		pcID := pc.ID
		fcID := fc.ID
		s.r.StorePlaneCommentMap(&PlaneCommentMap{
			WorkspaceID: s.Member.WorkspaceID, ID: newUUID(), LinkID: link.ID,
			FeatmapCommentID: &fcID, PlaneCommentID: &pcID, Origin: string(OriginPlane),
			PlaneUpdatedAt: &pc.UpdatedAt, CreatedAt: time.Now().UTC(),
		})
		pulled++
		if ts := pc.UpdatedAt.UTC().Format(time.RFC3339Nano); ts > maxSeen {
			maxSeen = ts
		}
	}
	// advance cursor only on full pull success
	if maxSeen > link.LastPulledCursor {
		link.LastPulledCursor = maxSeen
	}
	return pushed, pulled, nil
}
```

NOTE on attribution: `CreateFeatureCommentWithID` sets `CreatedByName = s.Acc.Name`. For pulled comments we want "Plane: <actor>". v1 simplification: accept the syncing account's name on pulled comments (attribution-by-name is a UI-deferred nicety; the map row records origin=plane authoritatively). The "Plane: <actor>" display is delivered when the UI badge lands (SYNC-011, deferred). Document this in the commit. (If you prefer exact attribution now, add a `CreateFeatureCommentAs(name, ...)` service variant -- out of scope for v1 per spec.)

- [ ] **Step 3: Implement `SyncProject`**

```go
func (s *service) SyncProject(projectID string) (*SyncResult, error) {
	links, err := s.r.FindPlaneLinksByProject(s.Member.WorkspaceID, projectID)
	if err != nil {
		return nil, err
	}
	res := &SyncResult{PerLink: []LinkSyncResult{}}
	for _, link := range links {
		pushed, pulled, serr := s.SyncLink(link)
		lr := LinkSyncResult{LinkID: link.ID, FeatureID: link.FeatureID, Pushed: pushed, Pulled: pulled}
		now := time.Now().UTC()
		link.LastSyncedAt = &now
		if serr != nil {
			link.LastStatus = string(StatusError)
			link.LastError = serr.Error()
			lr.Status = string(StatusError)
			lr.Error = serr.Error()
		} else {
			link.LastStatus = string(StatusOK)
			link.LastError = ""
			lr.Status = string(StatusOK)
		}
		s.r.StorePlaneLink(link) // persist status + advanced cursor
		res.Pushed += pushed
		res.Pulled += pulled
		res.PerLink = append(res.PerLink, lr)
	}
	return res, nil
}
```

This is the per-link isolation point: one link's error sets ITS status and the loop continues.

- [ ] **Step 4: Write the echo-prevention + cursor tests (the load-bearing ones)**

Append to `plane_sync_test.go`. Use a fake Plane that records created comments and serves them back -- this proves no echo across runs:

```go
import (
	"net/http"
	"net/http/httptest"
	"sync"
)

// fakePlane is an in-memory Plane double: comments created via POST are returned
// by subsequent GETs, so a sync run sees its own pushes (the echo risk).
type fakePlane struct {
	mu       sync.Mutex
	comments []PlaneComment
	seq      int
	srv      *httptest.Server
}

func newFakePlane() *fakePlane {
	f := &fakePlane{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			f.seq++
			id := "plane-" + itoa(f.seq)
			cm := PlaneComment{ID: id, CommentHTML: "<p>pushed</p>", UpdatedAt: timeNowUTC(), Actor: "remote"}
			f.comments = append(f.comments, cm)
			b, _ := jsonMarshal(cm)
			_, _ = w.Write(b)
			return
		}
		// GET list
		b, _ := jsonMarshal(planeCommentList{Results: f.comments, NextPageResults: false})
		_, _ = w.Write(b)
	}))
	return f
}

// seedRemote adds a comment that did NOT originate from featmap (a real Plane-side post).
func (f *fakePlane) seedRemote(html string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	f.comments = append(f.comments, PlaneComment{ID: "remote-" + itoa(f.seq), CommentHTML: html, UpdatedAt: timeNowUTC(), Actor: "remote"})
}
```

Add small helpers at the bottom of `plane_sync_test.go` to avoid import churn:

```go
func itoa(i int) string { return fmtSprintf("%d", i) }
```

(Simpler: just `import "strconv"` and use `strconv.Itoa`, `import "encoding/json"` for `json.Marshal`, `import "time"` for `time.Now().UTC()`, `import "fmt"`. Replace the `jsonMarshal`/`timeNowUTC`/`fmtSprintf` placeholders with the real stdlib calls. They are written as placeholders here only to flag the imports -- use `json.Marshal`, `time.Now().UTC()`, `strconv.Itoa` directly.)

The actual test:

```go
func Test_SyncLink_noEcho_and_pull(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member) {
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t)})
		fx := newProjectFixture(t, s)
		feat := fx.Features[1] // a feature with no seeded comment

		fp := newFakePlane()
		defer fp.srv.Close()

		// connection pointing at the fake; inject the fake's http client by
		// storing the fake URL as base_url (PlaneClient uses default client, which
		// reaches httptest's real URL).
		_, err := s.SetPlaneConnection(fx.Project.ID, fp.srv.URL, "ws", "key", "")
		mustOK(t, err, "SetPlaneConnection")
		link, err := s.LinkFeatureToPlane(feat.ID, "pp", "wi")
		mustOK(t, err, "link")

		// 1 local comment to push
		_, err = s.CreateFeatureCommentWithID(newUUID(), feat.ID, "local one")
		mustOK(t, err, "local comment")

		// first sync: push 1, pull sees its own push -> must NOT re-import (echo)
		pushed, pulled, err := s.SyncLink(link)
		mustOK(t, err, "sync 1")
		if pushed != 1 {
			t.Fatalf("expected 1 pushed, got %d", pushed)
		}
		if pulled != 0 {
			t.Fatalf("echo! pulled own pushed comment: %d", pulled)
		}

		// a genuine remote comment appears
		fp.seedRemote("<p>from plane</p>")
		pushed2, pulled2, err := s.SyncLink(link)
		mustOK(t, err, "sync 2")
		if pushed2 != 0 {
			t.Fatalf("nothing new to push, got %d", pushed2)
		}
		if pulled2 != 1 {
			t.Fatalf("expected to pull 1 remote, got %d", pulled2)
		}

		// idempotency: a third run changes nothing
		p3, q3, err := s.SyncLink(link)
		mustOK(t, err, "sync 3")
		if p3 != 0 || q3 != 0 {
			t.Fatalf("third run not idempotent: pushed=%d pulled=%d", p3, q3)
		}
	})
}
```

- [ ] **Step 5: Run, expect PASS**

Run: `go test -run '^Test_SyncLink_noEcho_and_pull$' .`
Expected: PASS. This proves: push works, no echo of own pushes, remote pull works, idempotent across runs (SYNC-022).

If `pulled != 0` on the first sync, the echo guard is broken -- the pushed comment's `plane_comment_id` must be in `mappedPlane` before the pull loop. Verify the push loop records the map row and sets `mappedPlane[pcID]=true` before pulling.

- [ ] **Step 6: Commit**

```bash
git add service.go plane_sync_test.go
git commit -m "feat(plane): SyncLink/SyncProject engine (push+pull, echo-safe, cursor)"
```

---

## Task 8: REST endpoints (SYNC-032 + connection/link config surface)

**Files:**
- Create: `plane-api.go`
- Modify: `main.go` (mount the route group)

- [ ] **Step 1: Create `plane-api.go`**

```go
package main

import (
	"net/http"

	"github.com/go-chi/chi"
	"github.com/go-chi/render"
)

// planeAPI mounts under the workspaceAPI group at /v1/projects/{projectID}/plane.
func planeAPI(r chi.Router) {
	r.Route("/projects/{projectID}/plane", func(r chi.Router) {
		r.Get("/connection", getPlaneConnection)
		r.Post("/connection", setPlaneConnection)
		r.Post("/connection/test", testPlaneConnection)
		r.Post("/link", linkFeatureToPlane)
		r.Post("/sync", syncPlaneProject)
	})
}

func getPlaneConnection(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "projectID")
	conn, err := GetEnv(r).Service.GetPlaneConnection(pid)
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, conn) // APIKeyCipher/Nonce are json:"-"
}

type setPlaneConnectionRequest struct {
	BaseURL         string `json:"baseUrl"`
	PlaneWorkspace  string `json:"planeWorkspace"`
	APIKey          string `json:"apiKey"`
	WatchedProjects string `json:"watchedProjects"`
}

func (p *setPlaneConnectionRequest) Bind(r *http.Request) error { return nil }

func setPlaneConnection(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "projectID")
	data := &setPlaneConnectionRequest{}
	if err := render.Bind(r, data); err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	conn, err := GetEnv(r).Service.SetPlaneConnection(pid, data.BaseURL, data.PlaneWorkspace, data.APIKey, data.WatchedProjects)
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, conn)
}

func testPlaneConnection(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "projectID")
	if err := GetEnv(r).Service.TestPlaneConnection(pid); err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, map[string]bool{"ok": true})
}

type linkFeatureRequest struct {
	FeatureID       string `json:"featureId"`
	PlaneProjectID  string `json:"planeProjectId"`
	PlaneWorkItemID string `json:"planeWorkItemId"`
}

func (p *linkFeatureRequest) Bind(r *http.Request) error { return nil }

func linkFeatureToPlane(w http.ResponseWriter, r *http.Request) {
	data := &linkFeatureRequest{}
	if err := render.Bind(r, data); err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	link, err := GetEnv(r).Service.LinkFeatureToPlane(data.FeatureID, data.PlaneProjectID, data.PlaneWorkItemID)
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, link)
}

func syncPlaneProject(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "projectID")
	if fid := r.URL.Query().Get("feature_id"); fid != "" {
		link, err := GetEnv(r).Service.GetPlaneLinkByFeature(fid)
		if err != nil {
			_ = render.Render(w, r, ErrInvalidRequest(err))
			return
		}
		pushed, pulled, serr := GetEnv(r).Service.SyncLink(link)
		if serr != nil {
			_ = render.Render(w, r, ErrInvalidRequest(serr))
			return
		}
		render.JSON(w, r, SyncResult{Pushed: pushed, Pulled: pulled, PerLink: []LinkSyncResult{{LinkID: link.ID, FeatureID: fid, Status: string(StatusOK), Pushed: pushed, Pulled: pulled}}})
		return
	}
	res, err := GetEnv(r).Service.SyncProject(pid)
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, res)
}
```

- [ ] **Step 2: Mount in `main.go`**

In `main.go`, after `r.Route("/v1/", workspaceAPI)`, the plane routes need the same account+workspace middleware. Simplest: mount inside `workspaceAPI`. In `workspace-api.go` `workspaceAPI(r)`, add near the other groups:

```go
	r.Group(func(r chi.Router) {
		r.Use(RequireSubscription())
		planeAPI(r)
	})
```

(If you prefer no subscription gate for sync, drop `RequireSubscription()`. Spec says `/mcp` skips subscription; for parity and since this is workflow-critical, omit `RequireSubscription()` -- use a plain `r.Group(func(r chi.Router){ planeAPI(r) })`. Pick the no-subscription form.)

- [ ] **Step 3: Build + smoke the route compiles**

Run: `go build .`
Expected: clean.

- [ ] **Step 4: REST handler test (httptest against the router is heavy; instead test the service path already covered). Add a thin test that the route is registered.**

Skip a full HTTP test (the service logic is covered in Task 7). Just confirm build + manual curl later in Task 12. Commit:

```bash
git add plane-api.go workspace-api.go
git commit -m "feat(plane): REST endpoints (connection, link, sync)"
```

---

## Task 9: MCP tools (SYNC-033 + connection/link via MCP)

**Files:**
- Create: `mcp_plane.go`
- Modify: `mcp.go` (registration)

- [ ] **Step 1: Create `mcp_plane.go`**

```go
package main

import (
	"context"
	"errors"
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
		pushed, pulled, serr := s.SyncLink(link)
		status := string(StatusOK)
		errStr := ""
		if serr != nil {
			status = string(StatusError)
			errStr = serr.Error()
		}
		return &SyncResult{Pushed: pushed, Pulled: pulled, PerLink: []LinkSyncResult{
			{LinkID: link.ID, FeatureID: a.FeatureID, Status: status, Error: errStr, Pushed: pushed, Pulled: pulled},
		}}, serr
	}
	return s.SyncProject(a.ProjectID)
}
```

- [ ] **Step 2: Register in `mcp.go` `buildMCPServer()`**

After the `query_board` registration, add:

```go
	add(srv, "set_plane_connection",
		"Configure a project's Plane connection (base URL + API key + workspace slug + watched project ids). The API key is stored encrypted; only a last-4 hint is ever returned. Re-call to update.",
		func(a planeConnectionArgs) string { return a.WorkspaceID }, mcpSetPlaneConnection)

	add(srv, "get_plane_connection",
		"Get a project's Plane connection (base URL, workspace slug, watched projects, key hint). Never returns the API key.",
		func(a planeProjectArgs) string { return a.WorkspaceID }, mcpGetPlaneConnection)

	add(srv, "test_plane_connection",
		"Test a project's stored Plane connection by calling Plane's /users/me. Returns ok or a specific error (401, unreachable).",
		func(a planeProjectArgs) string { return a.WorkspaceID }, mcpTestPlaneConnection)

	add(srv, "link_feature_to_plane",
		"Link a feature card to a Plane work item so their comments sync. One card links to at most one work item.",
		func(a planeLinkArgs) string { return a.WorkspaceID }, mcpLinkFeatureToPlane)

	add(srv, "unlink_feature_from_plane",
		"Remove the Plane link from a feature card (stops comment sync for it).",
		func(a planeUnlinkArgs) string { return a.WorkspaceID }, mcpUnlinkFeatureFromPlane)

	add(srv, "plane_sync",
		"Sync comments between Featmap and Plane for a project (or one card via feature_id). Pushes new local comments to Plane and pulls new Plane comments in, echo-safe. Returns per-link {pushed, pulled, status}.",
		func(a planeSyncArgs) string { return a.WorkspaceID }, mcpPlaneSync)
```

- [ ] **Step 3: Run the static guards (DB-free)**

Run: `SKIP_DB_TESTS=1 go test -run '^TestMCP' .`
Expected: PASS. `TestMCPRegistrationCompleteness` now counts the 6 new handlers. `TestMCPOutputSchemasAreClientSafe` passes (all return typed structs or `okResult`, no `any` field). If registration fails, a handler isn't wired.

- [ ] **Step 4: Build + commit**

Run: `go build .`

```bash
git add mcp_plane.go mcp.go
git commit -m "feat(plane): MCP tools (connection, link, plane_sync) -- 56 tools"
```

---

## Task 10: Background poller (SYNC-031)

The poller runs OUTSIDE request middleware, so it builds its own service+repo+tx per cycle.

**Files:**
- Create: `plane_poller.go`
- Modify: `main.go`

- [ ] **Step 1: Create `plane_poller.go`**

```go
package main

import (
	"log"
	"time"

	"github.com/jmoiron/sqlx"
)

// startPlanePoller launches a background goroutine that periodically syncs every
// project that has a Plane connection. Runs outside the HTTP request lifecycle,
// so it builds its own Service + repo + tx per cycle (mirrors how middleware
// would, but self-managed). interval "" or "0" disables it.
func startPlanePoller(db *sqlx.DB, config Configuration) {
	interval := config.PlanePollInterval
	if interval == "" || interval == "0" {
		log.Println("plane poller: disabled")
		return
	}
	d, err := time.ParseDuration(interval)
	if err != nil || d <= 0 {
		log.Printf("plane poller: invalid planePollInterval %q, disabled", interval)
		return
	}
	log.Printf("plane poller: every %s", d)
	go func() {
		ticker := time.NewTicker(d)
		defer ticker.Stop()
		for range ticker.C {
			runPlanePollCycle(db, config)
		}
	}()
}

func runPlanePollCycle(db *sqlx.DB, config Configuration) {
	tx, err := db.Beginx()
	if err != nil {
		log.Printf("plane poller: begin tx: %v", err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	repo := NewFeatmapRepository(db)
	repo.SetTx(tx)

	conns, err := repo.FindAllPlaneConnections()
	if err != nil {
		log.Printf("plane poller: list connections: %v", err)
		return
	}
	for _, conn := range conns {
		svc := NewFeatmapService()
		svc.SetConfig(config)
		svc.SetRepoObject(repo)
		// load the workspace member context the connection belongs to.
		// The poller acts as the connection's workspace; load any member of it.
		m, err := repo.GetMemberByWorkspaceFirst(conn.WorkspaceID)
		if err != nil {
			log.Printf("plane poller: no member for workspace %s: %v", conn.WorkspaceID, err)
			continue
		}
		svc.SetMemberObject(m)
		acc, err := repo.GetAccount(m.AccountID)
		if err == nil {
			svc.SetAccountObject(acc)
		}
		if _, err := svc.SyncProject(conn.ProjectID); err != nil {
			log.Printf("plane poller: sync project %s: %v", conn.ProjectID, err)
			// continue other connections
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("plane poller: commit: %v", err)
		return
	}
	committed = true
}
```

NOTE: this needs a repo helper `GetMemberByWorkspaceFirst(workspaceID) (*Member, error)` returning any one member of the workspace (the poller has no specific user). Add it:

In `repo.go` interface + impl:

```go
	GetMemberByWorkspaceFirst(workspaceID string) (*Member, error)
```
```go
func (a *repo) GetMemberByWorkspaceFirst(workspaceID string) (*Member, error) {
	x := &Member{}
	if err := a.tx.Get(x, "SELECT * FROM members WHERE workspace_id=$1 ORDER BY created_at LIMIT 1", workspaceID); err != nil {
		return nil, errors.Wrap(err, "not found")
	}
	return x, nil
}
```

(Confirm `Member` has `AccountID` and `created_at` columns -- check the members table/struct; adjust the column if named differently. `SetRepoObject`, `SetMemberObject`, `SetAccountObject`, `NewFeatmapService`, `NewFeatmapRepository` all exist -- used in `runInTx`.)

- [ ] **Step 2: Start it in `main.go`**

In `main.go`, after `m.Up()` (migrations applied) and after `db` is connected, add:

```go
	startPlanePoller(db, config)
```

- [ ] **Step 3: Build**

Run: `go build .`
Expected: clean. (No unit test for the goroutine timing; `runPlanePollCycle` correctness rests on `SyncProject` which is tested. A light test can call `runPlanePollCycle` against a seeded DB but is optional -- the cycle is thin glue.)

- [ ] **Step 4: Commit**

```bash
git add plane_poller.go main.go repo.go
git commit -m "feat(plane): background poll cycle (SYNC-031, self-managed tx)"
```

---

## Task 11: CLI subcommand (SYNC-034)

**Files:**
- Create: `plane_cli.go`
- Modify: `main.go`

- [ ] **Step 1: Create `plane_cli.go`**

```go
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
)

// runPlaneCLI handles `featmap plane sync ...`. It is a thin HTTP client over the
// REST endpoint (SYNC-032), so it shares one code path with every other trigger.
func runPlaneCLI(args []string) {
	if len(args) < 1 || args[0] != "sync" {
		fmt.Fprintln(os.Stderr, "usage: featmap plane sync --url <base> --key <api-key> --workspace <ws-id> --project <id> [--feature <id>] [--json]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("plane sync", flag.ExitOnError)
	urlF := fs.String("url", "http://localhost:5000", "featmap server base URL")
	keyF := fs.String("key", os.Getenv("FEATMAP_API_KEY"), "featmap API key (or FEATMAP_API_KEY)")
	wsF := fs.String("workspace", "", "workspace id (Workspace header)")
	projF := fs.String("project", "", "featmap project id")
	featF := fs.String("feature", "", "optional feature id to scope the sync")
	jsonF := fs.Bool("json", false, "emit raw JSON")
	_ = fs.Parse(args[1:])

	if *projF == "" || *keyF == "" || *wsF == "" {
		fmt.Fprintln(os.Stderr, "error: --project, --key, and --workspace are required")
		os.Exit(2)
	}

	endpoint := fmt.Sprintf("%s/v1/projects/%s/plane/sync", *urlF, *projF)
	if *featF != "" {
		endpoint += "?feature_id=" + *featF
	}
	req, _ := http.NewRequest("POST", endpoint, bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer "+*keyF)
	req.Header.Set("Workspace", *wsF)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "server returned %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}
	if *jsonF {
		fmt.Println(string(body))
		return
	}
	var res SyncResult
	if err := json.Unmarshal(body, &res); err != nil {
		fmt.Println(string(body))
		return
	}
	fmt.Printf("synced: pushed=%d pulled=%d across %d link(s)\n", res.Pushed, res.Pulled, len(res.PerLink))
	for _, l := range res.PerLink {
		fmt.Printf("  link %s: %s pushed=%d pulled=%d %s\n", l.FeatureID, l.Status, l.Pushed, l.Pulled, l.Error)
	}
}
```

- [ ] **Step 2: Branch at the top of `main()`**

In `main.go`, make `func main()` start with:

```go
func main() {
	if len(os.Args) > 1 && os.Args[1] == "plane" {
		runPlaneCLI(os.Args[2:])
		return
	}
	r := chi.NewRouter()
	// ... existing body ...
```

(`os` is already imported in main.go.)

- [ ] **Step 3: Build**

Run: `go build .`
Expected: clean. Quick check: `./featmap plane` (with no args) prints usage and exits 2 -- but the binary needs conf.json to NOT be required for the CLI path; since the CLI branch returns before `readConfiguration()`, it won't touch conf.json. Good.

- [ ] **Step 4: Commit**

```bash
git add plane_cli.go main.go
git commit -m "feat(plane): CLI subcommand 'featmap plane sync' (SYNC-034)"
```

---

## Task 12: Full verification, docs, rebuild, live smoke

**Files:**
- Modify: `readme.md`, `CLAUDE.md`, `CHANGELOG.md`

- [ ] **Step 1: Full build + vet + guards + suite**

Run:
```bash
go build . && go vet . && ./scripts/deadcode-check.sh
SKIP_DB_TESTS=1 go test -run '^TestMCP' .
go test .
```
Expected: build/vet clean; `deadcode-check: OK`; registration + schema-safe guards PASS; full suite PASS. If deadcode flags a new unreachable func, wire it or add to the baseline only if a deliberate false positive.

- [ ] **Step 2: Update tool count + docs**

Confirm count: `grep -c 'add(srv,' mcp.go` (was 50, now 56). Update `readme.md` "50 tools" -> the new count, add a `Plane` row to the tool table:

```
| Plane sync | `set_plane_connection`, `get_plane_connection`, `test_plane_connection`, `link_feature_to_plane`, `unlink_feature_from_plane`, `plane_sync` |
```

Add a `## Plane comment sync` section to `readme.md` (connection setup, link, sync triggers, the `planeEncryptionKey`/`planePollInterval` conf fields, that it is comments-only v1). Update `CLAUDE.md`: bump tool count, add `plane.go`/`plane-api.go`/`mcp_plane.go`/`plane_poller.go`/`plane_cli.go` to the backend file list with one-line roles, and a short "Plane sync" architecture note (engine = `SyncLink`, encrypted creds, poller is the first background job, migration 24).

- [ ] **Step 3: CHANGELOG entry**

Under `## [Unreleased]` -> `### Added`:

```markdown
- Plane comment sync (v1, backend/agentic -- no UI yet): link a Featmap card to a
  Plane work item and mirror comments both directions, echo-safe (dedupe by
  external id), driveable from a background poller, REST (`POST
  /v1/projects/{id}/plane/sync`), MCP (`plane_sync` + connection/link tools), and
  CLI (`featmap plane sync`). Per-project connection with the Plane API key
  encrypted at rest (AES-256-GCM, dedicated `planeEncryptionKey`); only a last-4
  hint is ever returned. New `plane*.go`, migration `24_plane_sync.up.sql`
  (Postgres ENUM types `plane_comment_origin`/`plane_sync_status`). Comments-only:
  no card push, no field sync, edits not synced (see icebox ICE-033/034).
```

- [ ] **Step 4: Commit docs**

```bash
git add readme.md CLAUDE.md CHANGELOG.md
git commit -m "docs: document Plane comment sync (56 MCP tools)"
```

- [ ] **Step 5: Rebuild container + reconnect**

Run: `docker compose up -d --build`
Then the USER runs `/mcp` to reconnect featmap and pick up the 6 new tools.

- [ ] **Step 6: Live smoke (after reconnect, needs a real Plane project + key)**

Via MCP: `set_plane_connection` (real base URL + key + workspace slug), `test_plane_connection` (expect ok), `link_feature_to_plane` (a roadmap card -> a Plane work item), post a Featmap comment, `plane_sync` -> verify it appears in Plane; add a Plane comment, `plane_sync` again -> verify it appears on the card; run `plane_sync` a third time -> zero pushed/pulled (idempotent, no echo). Confirm via psql that `plane_connections.api_key_cipher` is not plaintext.

---

## Self-review notes (author)

- **Spec coverage:** SYNC-001 (set/test connection T5/T8/T9) | SYNC-002 watched_projects (stored T1/T5, used by poller scope -- future filtering noted) | SYNC-003 crypto (T2) | SYNC-010 link (T6/T8/T9) | SYNC-011 badge -> UI deferred (link+status data exists) | SYNC-020 push (T7) | SYNC-021 pull (T7) | SYNC-022 echo (T7 + the load-bearing test) | SYNC-023 schema+cursor (T1/T7) | SYNC-030 manual = per-feature sync via REST/MCP (T8/T9) | SYNC-031 poller (T10) | SYNC-032 REST (T8) | SYNC-033 MCP (T9) | SYNC-034 CLI (T11) | SYNC-040 status fields (T1 columns, written in T7 SyncProject).
- **Known v1 simplifications (documented, not gaps):** pulled-comment attribution uses the syncing account name not "Plane: <actor>" (exact attribution lands with the deferred UI badge); SYNC-002 watched_projects is stored but project-level poll filtering is not yet enforced (poller syncs all of a project's existing links -- linking is already scoped by the user); markdown<->html pass-through (ICE-034).
- **Placeholder scan:** the fake-Plane test helpers note `jsonMarshal`/`timeNowUTC`/`fmtSprintf` are stand-ins -- the implementer uses `json.Marshal`/`time.Now().UTC()`/`strconv.Itoa` directly (flagged in Task 7 Step 4). No other placeholders.
- **Type consistency:** `PlaneClient`, `PlaneComment`, `PlaneConnection`/`PlaneLink`/`PlaneCommentMap`, `SyncResult`/`LinkSyncResult`, `CommentOrigin`/`SyncStatus`, `encryptPlaneKey`/`decryptPlaneKey`, `SyncLink`/`SyncProject` consistent across tasks. Repo method names match between interface and impl.
- **Verification-before-completion:** Tasks 7 and 12 carry the echo/idempotency proof and the live smoke; the registration + schema-safe guards (the bugs that bit us before) run in T9/T12.
```
