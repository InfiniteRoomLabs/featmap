# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Featmap -- open-source user story mapping tool. Go backend (chi router, PostgreSQL via sqlx) serving a React/TypeScript SPA. Single binary: Go embeds the built React app, SQL migrations, and email templates via `go-bindata`. Licensed Business Source License 1.1 (NOT OSI open source -- read LICENSE before reuse).

This is a vendored third-party repo under `infinite-room-labs/third-party/`. Upstream: https://github.com/amborle/featmap. Treat changes as fork-local unless explicitly upstreaming.

## Build / Run

Single-binary build embeds React assets and migrations. Order matters: webapp -> bindata -> go build.

```bash
# Full release build (all archs)
./build/complete_build.sh           # runs build_webapp.sh, generate.sh, build_all_arch.sh

# Individual steps
./build/build_webapp.sh             # pnpm install --frozen-lockfile + pnpm run build in webapp/
./build/generate.sh                 # go-bindata for migrations/, tmpl/, webapp/build/
VERSION=dev ./build/build_all_arch.sh  # cross-compile darwin/windows/linux to bin/

# Local dev (docker) -- full stack: postgres + featmap single binary
cp config/.env .                    # FEATMAP_DB/_USER/_PASSWORD/_HTTP_PORT live here
cp config/conf.json .               # mounted into the container at /opt/featmap/conf.json
docker-compose build
docker-compose up -d                # postgres + featmap on FEATMAP_HTTP_PORT (default 5000)
#   -> UI/REST at http://localhost:5000 , MCP at http://localhost:5000/mcp
#   postgres data persists in ./data ; `docker-compose down` to stop, rm ./data to reset DB

# Frontend dev (hot reload, talks to Go backend on :5000 via CORS allowlist)
cd webapp && pnpm start             # serves on :3000

# Frontend tests
cd webapp && pnpm test
```

Prerequisites for **host** source builds: Go 1.25+ (matches `go.mod`), Node + pnpm, `go-bindata` (`go install github.com/kevinburke/go-bindata/v4/...@v4.0.2` -- original `jteeuwen/go-bindata` is archived), PostgreSQL. The **docker** path needs none of these -- the multi-stage `Dockerfile` runs the whole webapp+bindata+go build in pinned builder images.

**Module policy note**: Migrated from `npm` to `pnpm`. Webapp builds need a few workarounds documented in `webapp/.npmrc`:
- `trust-policy-exclude[]=semver` -- multiple `semver` versions in CRA 4's transitive tree pre-date npm provenance; the global pnpm `trust-policy=no-downgrade` would refuse the install.
- `strict-peer-dependencies=false` -- CRA 4 pins `@babel/core@7.12.3` while several `@babel/*` plugins want `>=7.13`. Upstream's own lockfile shipped under loose-peer npm.
- `pnpm.onlyBuiltDependencies = [core-js, ejs, es5-ext]` in `package.json` -- the global pnpm setting blocks postinstall scripts by default.

**Node compatibility**: CRA 4 + webpack 4 use legacy md4 hashes that OpenSSL 3 dropped. Build scripts set `NODE_OPTIONS=--openssl-legacy-provider`. Required for Node 17+. If you switch to a newer Node and CRA 4 build breaks with `ERR_OSSL_EVP_UNSUPPORTED`, that's why.

**`@types/react`**: must be explicitly pinned in `devDependencies` (was implicit under npm's flat hoisting; pnpm requires it).

## Architecture

### Backend (Go, flat `package main`)

All Go files live in the repo root as a single package. Layering is by file, not by directory:

- `main.go` -- chi router wiring, config load, migration runner, JWT auth init, static asset serving
- `mware.go` -- middleware stack (`ContextSkeleton`, `Transaction`, `Auth`, `User`) + `RequireAccount`/`RequireMember`/`RequireAdmin`/`RequireOwner`/`RequireSubscription` guards
- `*-api.go` -- HTTP handlers grouped by domain (`users-api`, `account-api`, `workspace-api`, `subscription-api`, `link-api`). `account-api.go` also carries the API-key CRUD (`GET/POST/DELETE /v1/account/apikeys`)
- `mcp.go` -- Streamable HTTP MCP server (48 tools) + helpers. Tool handlers are package-level `mcpFoo` funcs wired via `buildMCPServer()`; `withService` centralises bearer auth + `resolveWorkspace`. See the MCP section below
- `mcp_bulk.go` -- bulk + unified-partial feature tools (`update_feature`, `bulk_create_features`, `bulk_update_features`) layered on the single-entity tools. Bulk loops run via `runBulkTx`, which wraps each item in a Postgres SAVEPOINT so a per-item failure rolls back its own slot instead of poisoning the shared always-commit transaction (see the Transaction gotcha below)
- `mcp_bulk_structural.go` -- partial-update tools for structural entities (`update_milestone`/`update_workflow`/`update_subworkflow`, and the pointer-field rewrite of `update_persona`) plus the rest of the bulk surface (`bulk_add_comment`, `bulk_create_*`, `bulk_attach_personas`/`bulk_detach_personas`, `bulk_update_*`, `bulk_reorder_features`, `bulk_delete_*`). `bulk_reorder_features` is all-or-nothing via the `ReorderFeatures` service method, which pre-computes the full lexorank chain before any write and requires the complete cell
- `service.go` -- business logic, `Service` interface (huge -- ~50KB). All handler logic calls into the service via `GetEnv(r).Service`
- `repo.go` -- `Repository` interface + sqlx impl, all SQL lives here
- `model.go` -- domain structs (Workspace, Account, Member, Project, Milestone, Workflow, Subworkflow, Feature, Persona, Invite, Subscription, FeatureComment, Annotation)
- `stripe.go` -- Stripe webhook + subscription lifecycle
- `email.go` -- SMTP send, templates rendered from `tmpl/*.tmpl` (embedded via bindata)
- `shared.go`, `response.go` -- helpers
- `lexorank/` -- fractional-ordering library used to position cards without renumbering siblings

### Request lifecycle

Every request gets a fresh `Service` and a DB transaction via middleware:

1. `ContextSkeleton` constructs a new `FeatmapService` per request, stashes it in `r.Context()` under `contextKey`.
2. `Transaction` opens a `sqlx.Tx`, attaches a `FeatmapRepository` bound to that tx, and runs the handler inside `txnDo` (commit/rollback wrapper).
3. `Auth` + `User` parse the JWT cookie, load the `Account`, and -- if the `Workspace` header is present -- load the `Member` and `Workspace` onto the service.
4. Route-level middleware (`RequireAccount`, `RequireMember`, `RequireAdmin`, `RequireOwner`, `RequireSubscription`) gates by role/plan.
5. Handlers call `GetEnv(r).Service.SomeMethod(...)`. Service uses its bound `Repository`.

The service holds per-request state (account, member, workspace, subscription) -- it's intentionally not a singleton. Don't cache `Service` across requests.

### Route layout

- `/v1/users`, `/v1/link`, `/v1/subscription` -- public-ish (no account required)
- `/v1/account` -- authenticated account scope
- `/v1/*` (everything else) -- `workspaceAPI` group, requires account + workspace member context (from `Workspace` HTTP header)
- `/mcp` (+ `/mcp/*`) -- MCP Streamable HTTP endpoint, gated by `RequireAccount()` only; workspace is resolved per-tool-call from the `workspace_id` argument, not the `Workspace` header
- `/static/*` -- embedded React build assets
- `/*` -- SPA fallback, serves `index.html` from bindata

### Database

PostgreSQL only. Migrations in `migrations/*.up.sql`, applied on startup by `golang-migrate` reading from `go-bindata`-embedded source. **No `.down.sql` files exist** -- migrations are forward-only. Adding migration N: create `N_name.up.sql`, then re-run `./build/generate.sh` to regenerate `migrations/bindata.go` before building.

### Frontend (`webapp/`)

CRA 4 + TypeScript 4.3 + React 17 + Redux + connected-react-router + Formik + Yup + react-beautiful-dnd.

- `src/api/index.ts` -- fetch wrappers, sends `Workspace` header for workspace-scoped calls
- `src/store/{features,milestones,projects,subworkflows,workflows,personas,workflowpersonas,featurecomments,application}/` -- Redux slices, one folder per domain
- `src/pages/*.tsx` -- top-level routed pages
- `src/components/` -- shared UI (also includes the story map board itself)
- `src/core/lexorank.ts` -- TS port of the Go lexorank package; both ends must stay compatible
- `src/avatars/` -- generated SVG avatar assets

### Single-binary embedding

Three bindata bundles get compiled in:
- `migrations/bindata.go` -- package `migrations`
- `tmpl/bindata.go` -- package `tmpl`
- `webapp/bindata.go` -- package `webapp` (built React app under `webapp/build/...`)

These are gitignored (or should be) and regenerated by `./build/generate.sh`. If `main.go` won't compile complaining about `migrations.Asset`/`webapp.Asset`/`tmpl`, you forgot to run generate.

## Configuration

`conf.json` in the working directory at runtime. Required fields: `appSiteURL`, `dbConnectionString`, `jwtSecret`, `port`. Stripe/SMTP optional but features depending on them silently no-op. `environment: "development"` disables secure-cookie flag (use only over plain HTTP). Sample lives at `config/conf.json`.

CORS allowlist is `appSiteURL` + `http://localhost:3000` (hardcoded for CRA dev server).

## MCP Server & API Keys

Fork-local addition (commit `d71173a`). Featmap exposes most of its workspace API as a **Model Context Protocol** server so local LLM agents can drive boards. End-user docs (full 48-tool table, color/status enums, bootstrap recipe) live in `readme.md` -- this section is the dev/architecture view.

### How auth works

One auth surface for REST and MCP. `mware.go User()` tries `Authorization: Bearer <key>` **first**, falls back to the JWT cookie:

- API keys are UUID v4, **SHA-256 hashed at rest** (table `api_keys`, migration `23_api_keys.up.sql`); only an 8-char prefix is stored for display. Lookup is O(1) by hash. Plaintext is returned exactly once at creation and is unrecoverable.
- Keys carry the **privileges of the owning account**. The MCP server takes `workspace_id` as a tool argument (not the `Workspace` header), so a single key drives any workspace that account belongs to. `resolveWorkspace()` in `mcp.go` loads member/workspace/subscription onto the per-request service before each tool call.
- `/mcp` is gated by `RequireAccount()` only (no `RequireMember`/`RequireSubscription`); workspace-level gating happens inside `resolveWorkspace`.

### Transport

Streamable HTTP, **Stateless** mode (no session retention), JSON-RPC 2.0 over a single POST to `/mcp`. SDK: `github.com/modelcontextprotocol/go-sdk` v1.6.0 (the dep that forced `go.mod` from 1.15 to 1.23). DNS-rebinding protection is on by default in the SDK -- the `Host` header must be a loopback name for `/mcp` requests.

### Integrating into an agent

The server speaks standard MCP HTTP transport, so any client (Claude Code/Desktop, Cursor, custom agent) connects with an HTTP server entry. For **this repo**, write `.mcp.json` at the repo root -- it is **gitignored** (`/.mcp.json`) precisely because it holds a live bearer token. Never commit it.

```json
{
  "mcpServers": {
    "featmap": {
      "type": "http",
      "url": "http://localhost:5000/mcp",
      "headers": { "Authorization": "Bearer <your-api-key>" }
    }
  }
}
```

Mint the key in the UI: **Account menu -> Settings -> API keys -> Create key**, copy the one-shot plaintext. With the local docker stack up (above), the agent loop is: agent -> `localhost:5000/mcp` -> bearer auth -> tool call with `workspace_id` -> service/repo -> postgres.

### Testing

Tool handlers are exposed as package-level `mcpFoo` funcs so the suite (`mcp_test.go`, `mcp_helpers_test.go`, `mcp_testmain_test.go`) calls them directly without going through the SDK transport. `go test` against a real postgres -- there is no mock repo.

### MCP gotcha (read before writing tool handlers)

`mware.go Transaction()` **always commits** regardless of handler outcome (the `next.ServeHTTP` call is wrapped in `return nil`). A tool handler that panics mid-mutation leaves partial state committed. Tool handlers MUST surface failures via the returned `error` value, never panic mid-write.

## Gotchas

- **`package main` everywhere**: no internal subpackages. New backend code goes in a new top-level `*.go` file or an existing one.
- **`go.mod` runs Go 1.25 but pins old `+incompatible` deps** (`jwt-go` superseded by `golang-jwt/jwt`, `satori/go.uuid` archived, `stripe-go v70` is years behind). Old deps still compile under modern Go; don't casually `go get -u` -- upgrades need real testing.
- **Migrations are append-only**: never edit a committed `*.up.sql`. Add a new numbered file.
- **`go-bindata` is required** at build time. Without regenerating after changing migrations/templates/webapp build, the binary ships stale assets.
- **Webapp ESLint config is just `react-app`** -- no custom rules. CRA defaults apply.
- **JWT secret rotation invalidates every session**. Cookie-based, no refresh token flow.
- **Stripe code paths run only if `stripeKey` is set**. Self-hosters typically run without; `RequireSubscription` guards features behind a workspace subscription record either way.

## License

Business Source License 1.1 (see `LICENSE`). This is **not** OSI open source -- production use by competitors is restricted until the change date. Treat any redistribution or hosted-service decision as requiring legal review.
