# MCP Scoped Reads -- Design

Date: 2026-05-27
Branch: `feat/mcp-scoped-reads`
Status: approved (pre-implementation)

## Problem

The MCP read surface has no scoped read. The only read tools are
`list_workspaces`, `list_projects`, and `get_board`. `get_board` returns the
whole project -- all 7 arrays including every feature's full `description`
markdown and every `FeatureComment`. On the roadmap board this is ~433K
characters, which overflows an agent's token budget.

Consequence: to read even a 15-card slice (e.g. all `SYNC-*` cards) an agent
must either swallow the 433K dump or drop out of the MCP surface entirely and
hit Postgres directly. Both defeat the agentic-first thesis of the fork: an
agent cannot do real scoped work through MCP alone.

Root cause: `get_board` is all-or-nothing and full-fidelity. The balloon is
specifically `Features.description` and the `FeatureComments` array; the
backbone (workflows / subworkflows / milestones / personas) is small.

## Goal

Let an agent (or any MCP client) read exactly the slice and fields it needs,
without a 433K dump and without leaving MCP. Keep a bulletproof,
fully-typed path for the high-frequency "read one card" operation.

Non-goals (explicitly out of scope -- next epic, not this one):
- Server-side pagination.
- A dedicated `list_features` / search tool.
- A general response-filter wired onto every read tool (this design filters
  `get_board` only -- the one bloated read).
- Any UI, migration, or schema change.

## Approach (chosen: jq filter + typed drill)

Two changes to the MCP read surface:

### 1. `get_board` gains an optional `filter` param (gojq)

- **No `filter`** -> behaves exactly as today: returns the full typed
  `boardResult`, output-schema-validated. Existing callers are unaffected
  (back-compat = the default path is unchanged).
- **`filter` present** -> the server builds the full `boardResult` in memory
  (cheap for a local Postgres + Go binary), runs the gojq expression over its
  JSON, and returns the transformed JSON as the tool result.

Why jq over typed filter params or k8s-style label selectors:
- Typed filter params only cover scopes we anticipate; they cannot project
  away heavy fields. jq does scoping **and** projection in one param, so the
  agent can drop `description` / `comments` / `annotations` at will -- the
  field-bloat question disappears because heavy fields are included only when
  asked for.
- k8s label selectors (`k8s.io/apimachinery/pkg/labels`) select over
  `map[string]string` labels. Features have no labels, only fixed struct
  fields, so a label selector would mean inventing a grammar over fixed fields
  -- a worse, custom jq. Rejected.
- The primary consumer is an LLM, which is fluent in jq. The flexible query
  surface is a feature, not a footgun, for this consumer.

Library: `github.com/itchyny/gojq` -- pure Go, no cgo, widely used. Pinned in
`go.mod` per the repo's pin-everything ethos.

### 2. New `get_feature` tool (typed, reliable drill)

- `get_feature(workspace_id, feature_id, include_comments=false)`.
- Returns one feature, full `description`, output-schema-validated. With
  `include_comments=true`, also returns that feature's comments.
- This is the zero-jq path: the common "read one card" op must never depend on
  the agent getting a jq expression right.
- Backed by the existing `repo.GetFeature(workspaceID, featureID)`. Comments
  via a new `repo.GetFeatureCommentsByFeature(workspaceID, featureID)` (one SQL
  query) rather than filtering the project-wide comment fetch.

### 3. Shape awareness for the agent (how it learns to write filters)

The agent learns the board shape from two sources, belt-and-suspenders:

- **Auto output schema (free):** `mcpsdk.AddTool` derives an output schema from
  the Go return type whenever `Out != any` (verified: go-sdk v1.6.0
  `server.go:307`). `get_board`'s unfiltered `boardResult` and `get_feature`'s
  return both advertise their full shape -- field names and types -- to clients
  that read output schemas.
- **Embedded shape reference + canonical jq recipes in the `filter` param's
  *description* (the real lever):** the param description is *input* schema,
  always present in the tool list the model sees. It carries a compact board
  shape (the key arrays + the fields on a feature stub) and 2-3 worked jq
  examples (stubs for a prefix, one card's body, a card's comments). The agent
  reads the shape and a worked example exactly where it writes the filter, so
  it never has to call unfiltered `get_board` just to discover field names.

## Data flow

```
Agent                          get_board handler                gojq
  | filter=".features[]           |                              |
  |   | select(.title            |                              |
  |   | startswith(\"SYNC-\"))    |                              |
  |   | {id,title,status}"        |                              |
  |------------------------------>|                              |
  |                               | build full boardResult       |
  |                               | (typed, server-side)         |
  |                               | marshal to JSON              |
  |                               | compile filter -------------->| parse/validate
  |                               |                              |  (error -> clean
  |                               |                              |   parse error)
  |                               | run filter over board JSON-->| transform
  |                               |<-----------------------------| []results
  |  transformed JSON (raw)       |                              |
  |<------------------------------|                              |
```

`get_feature` is a straight typed call: resolve workspace -> `repo.GetFeature`
(+ optional comments) -> typed return.

## Components / boundaries

- New file `mcp_reads.go` (flat `package main`, per repo convention): holds
  `mcpGetFeature`, the gojq filtering helper, the `filter`-aware variant of the
  `get_board` handler, and the embedded shape-reference constant. Keeps the
  read additions in one focused file rather than growing `mcp.go`.
- `mcp.go`: register `get_feature`; update `get_board`'s args struct + handler
  wiring to accept `filter`. Both registrations go through the existing
  `add()` helper so `TestMCPRegistrationCompleteness` covers them.
- `repo.go`: add `GetFeatureCommentsByFeature`.
- `service.go`: add the service-layer passthrough for the new repo method +
  the get_feature path (workspace-scoped, same pattern as existing getters).

## Return-shape constraint (accepted trade-off)

When `filter` is passed, the output is whatever jq produces -- an arbitrary
shape -- so it cannot ride back through the typed `boardResult` `Out` (the SDK
validates `Out` against the declared output schema and would reject a
non-matching shape). The filtered result is therefore returned as raw JSON
text content, and output-schema validation is lost **only** on the filtered
path. This is inherent to any caller-controlled projection (jq / GraphQL /
SQL SELECT) and is acceptable.

What we still guarantee:
- The board fed *into* jq is the type-safe `boardResult` built server-side
  (source of truth validated).
- The filter string is compiled by gojq before running; a malformed filter
  returns a clean parse error -- never a crash or a partial dump.
- `get_feature` keeps full output-schema validation (the reliable path).

## Error handling

- Empty/whitespace `filter` -> treated as absent (full board).
- Filter fails to compile -> return a clear error naming the parse failure;
  no board data leaked.
- Filter compiles but errors at runtime (e.g. type error mid-expression) ->
  surface the gojq runtime error via the returned `error`; never panic.
  (Reads are read-only, so the always-commit Transaction tx-poisoning class
  does not apply -- no SAVEPOINT machinery needed.)
- `get_feature` with unknown `feature_id` -> "feature not found" error, not a
  500/nil-deref.
- Both tools enforce workspace membership via `withService` +
  `resolveWorkspace`, same as every other tool.

## Testing

- `get_feature`: returns the right card; `include_comments` toggles the
  comments array; unknown id errors cleanly; wrong-workspace id is denied.
- `get_board` no-filter: unchanged full board (regression guard).
- `get_board` with filter: a prefix-select-project filter returns only the
  expected stubs; a single-id select returns one card; a malformed filter
  returns a parse error and no data; an empty filter == no filter.
- `TestMCPRegistrationCompleteness` (existing) must stay green -- both new
  handlers registered, no orphans, no duplicate tool names.
- Tests run against real Postgres (testcontainers), per repo convention;
  static registration test runs DB-free under `SKIP_DB_TESTS=1`.

## Docs / housekeeping

- Tool count 47 -> 49 in `readme.md` and `CLAUDE.md`; add the two tools to the
  MCP tool tables; document the `filter` param with the same shape reference +
  recipes embedded in its description.
- `CHANGELOG.md` `[Unreleased]` entry.
- `deadcode-check.sh` must stay green (new code is reachable from `buildMCPServer`).

## Why this unblocks SYNC

Every scoped read this design enables is one we were forced to run as raw SQL
while planning SYNC (list the SYNC-* cards, read one card's full AC, read a
card's comments). With these tools an agent does that planning -- and the
ongoing SYNC sync state inspection -- entirely through MCP. SYNC scope/
sequencing (the backend-first-vs-UI question) reopens once these ship.
