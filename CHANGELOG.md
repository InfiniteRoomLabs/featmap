# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This is an Infinite Room Labs fork of [amborle/featmap](https://github.com/amborle/featmap).
Entries below cover fork-local changes; upstream history is in the git log.

## [Unreleased]

### Added
- Account-scoped API keys for scripts and automation (`Authorization: Bearer ...`),
  managed from the Account settings page. Keys are UUID v4, SHA-256 hashed at rest
  with an 8-char display prefix, and shown in plaintext exactly once at creation
  (`migrations/23_api_keys.up.sql`, `account-api.go`, `model.go`/`repo.go`/`service.go`).
- Built-in MCP server at `/mcp` exposing 50 workspace tools to local LLM agents over
  the Model Context Protocol (Streamable HTTP, Stateless transport) -- workspaces,
  projects, milestones, workflows, subworkflows, features, comments, and personas
  (`mcp.go`, mounted in `main.go` behind `RequireAccount`).
- Unified partial-update MCP tools `update_feature`, `update_milestone`, `update_workflow`,
  `update_subworkflow`, plus a partial-safe rewrite of `update_persona`: each accepts any
  subset of an entity's fields and leaves omitted fields unchanged (pointer/presence
  semantics, fixing a zero-value clobber bug). They supersede the single-field
  `rename_feature` / `set_*_color` / `set_*_status` / `move_feature` tools, which remain
  for compatibility (`mcp_bulk.go`, `mcp_bulk_structural.go`).
- Bulk MCP tools that mutate many entities per call (max 100) and return a per-item
  `{index, ok, id, error}` envelope in input order: `bulk_create_features` / `_milestones` /
  `_workflows` / `_subworkflows` / `_personas`, `bulk_update_features` / `_milestones` /
  `_workflows` / `_subworkflows`, `bulk_add_comment`, `bulk_attach_personas` /
  `bulk_detach_personas`, `bulk_reorder_features`, and `bulk_delete_features` / `_personas`.
  Each item is isolated by a Postgres SAVEPOINT so one item's failure neither poisons the
  shared (always-commit) transaction nor aborts its siblings; `bulk_reorder_features` is
  all-or-nothing and pre-computes the full lexorank chain before any write
  (`mcp_bulk.go`, `mcp_bulk_structural.go`, `service.go` `ReorderFeatures`,
  `repo.go` savepoint helpers).
- `ApiKeysSection` panel on the account page with one-shot plaintext reveal,
  copy-to-clipboard, and revoke (`webapp/src/components/ApiKeysSection.tsx`).
- `.dockerignore` keeping secrets (`conf.json`, `.env`, `.mcp.json`) and regenerated
  artifacts (`node_modules`, `bindata.go`, `webapp/build`) out of the build context.
- GitHub Actions CI (`.github/workflows/ci.yml`, SHA-pinned actions, least-privilege
  `contents: read`): a DB-free `lint` job (build, vet, MCP registration guard, deadcode)
  and a `test` job (full suite via testcontainers Postgres). Plus `mcp_registration_test.go`
  -- a stdlib `go/ast` test asserting every `mcp*` tool handler is wired via `add()` with no
  orphans or duplicate tool names (the check that would have caught the unregistered
  `bulk_add_comment`) -- and `scripts/deadcode-check.sh`, which gates *new* unreachable code
  against `.github/deadcode-baseline.txt` (pinned `deadcode@v0.45.0`).

- Scoped MCP read tools so agents can read a board slice without the full-board
  dump: `get_feature` (one card by id, typed/schema-validated, optional
  `include_comments`) and `query_board` (a jq/gojq filter+projection over the
  board, returning matched values as `{results:[...]}`). `get_board` is
  unchanged. gojq runs server-side with the request context forwarded for
  cancellation; malformed filters return a clean parse error and never panic
  (`mcp_reads.go`, `repo.go` `FindFeatureCommentsByFeatureID`, `service.go`
  `GetFeature`/`GetFeatureCommentsByFeature`, new dep `github.com/itchyny/gojq`).
  `query_board` returns its `{results:...}` via an `any` handler type so the SDK
  emits no output schema -- a typed `any` field would emit a boolean subschema
  (`"results": true`) that strict MCP clients reject, aborting the entire
  tools/list. `TestMCPOutputSchemasAreClientSafe` connects an in-memory client to
  the real server and guards against any tool reintroducing that class.

### Changed
- Rewrote `Dockerfile` as a digest-pinned multi-stage build (pnpm + `--frozen-lockfile`,
  go-bindata v4, static binary on distroless-nonroot), replacing the stale single-stage
  one that used npm, the archived `jteeuwen/go-bindata`, and a `go get -u` install pattern
  that no longer works on modern Go. The whole webapp + bindata + go build now runs in
  pinned builder images with no host toolchain required.
- `docker-compose.yml`: build image renamed to `featmap-fork:local` with `pull_policy: never`
  (so compose can never silently fall back to the upstream Hub image), postgres bumped to
  `16-alpine` digest-pinned, and the `conf.json` mount made read-only.
- `webapp/.npmrc`: pin `auto-install-peers=false` to match the lockfile so
  `pnpm install --frozen-lockfile` works without the global pnpm hardening config
  (fixes `ERR_PNPM_LOCKFILE_CONFIG_MISMATCH` in containers/CI).
- `mware.go` `User()` now authenticates `Authorization: Bearer <key>` first and falls
  back to the JWT cookie, giving REST and MCP a single auth surface.
- Bumped `go.mod` from Go 1.15 to 1.23 and added `modelcontextprotocol/go-sdk` v1.6.0.
- Migrated the webapp from npm to pnpm; build scripts use pnpm with
  `NODE_OPTIONS=--openssl-legacy-provider` for the CRA 4 / webpack 4 md4 hash
  requirement on modern Node.
- Documented the MCP server, API keys, agent integration, and local docker workflow
  in `CLAUDE.md` and `readme.md`.

### Security
- `.gitignore` excludes `/.mcp.json` (live API key bearer tokens) so client configs
  are never committed.
