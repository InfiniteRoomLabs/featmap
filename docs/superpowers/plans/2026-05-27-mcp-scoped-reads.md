# MCP Scoped Reads Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give MCP clients scoped board reads -- a `query_board` tool (gojq filter/projection over the board) and a typed `get_feature` drill tool -- so an agent never has to swallow the ~433K full-board dump or drop to raw SQL.

**Architecture:** Two NEW package-level MCP tool handlers in a new `mcp_reads.go`, registered through the existing `add()` helper in `mcp.go`. `query_board` builds the full `boardResult` server-side (reusing the same service getters as `get_board`), marshals it to generic JSON, and runs a compiled gojq program over it, returning matches in a `{results: [...]}` envelope (`Out = queryBoardResult{ Results any }`, permissive schema). `get_feature` returns one feature (typed, schema-validated) with optional comments. `get_board` is untouched.

**Tech Stack:** Go 1.25, `github.com/modelcontextprotocol/go-sdk` v1.6.0, `github.com/itchyny/gojq` v0.12.17 (new, pinned), sqlx, testcontainers-go (Postgres) for tests.

---

## Context the engineer needs (read before starting)

- **All Go is flat `package main`** in the repo root. New code goes in a new top-level `*.go` file. No subpackages.
- **MCP tool pattern** (`mcp.go`): a handler is a package-level func
  `func mcpXxx(ctx context.Context, s Service, a XxxArgs) (Out, error)`. It is
  registered in `buildMCPServer()` with
  `add(srv, "tool_name", "description", func(a XxxArgs) string { return a.WorkspaceID }, mcpXxx)`.
  The `add()` helper wraps the handler in `withService`, which pulls the
  per-request `Service` from context, enforces auth, and calls
  `resolveWorkspace(s, workspaceID)` so `s.Member.WorkspaceID` etc. are loaded
  before the handler runs.
- **Reads are read-only** -- the always-commit `Transaction()` tx-poisoning
  gotcha does NOT apply. No SAVEPOINT machinery. Just return errors (never panic).
- **`boardResult`** (mcp.go:272) is the full board struct:
  `Project, Milestones, Workflows, SubWorkflows, Features, FeatureComments, Personas, WorkflowPersonas`.
  `mcpGetBoard` (mcp.go:301) builds it from `s.GetProject(id)`,
  `s.GetMilestonesByProject(id)`, `s.GetWorkflowsByProject(id)`,
  `s.GetSubWorkflowsByProject(id)`, `s.GetFeaturesByProject(id)`,
  `s.GetFeatureCommentsByProject(id)`, `s.GetPersonasByProject(id)`,
  `s.GetWorkflowPersonasByProject(id)`.
- **`repo.GetFeature(workspaceID, featureID) (*Feature, error)`** already exists
  (repo.go:526). **`FindFeatureCommentsByFeature`** (repo.go:580) returns only a
  SINGLE comment via `.Get` -- do NOT use it for the list; add a new multi-row
  method.
- **Service is an interface** (`service.go`). Adding a service method means: add
  the method decl to the `Service` interface block AND the `*service` impl.
  Workspace scoping uses `s.Member.WorkspaceID` and account name `s.Acc.Name`.
- **Test harness** (`mcp_testmain_test.go`, `mcp_helpers_test.go`): tests call
  `runInTx(t, func(t, ctx, s, acc, ws, member) { ... })`, build fixtures with
  `newProjectFixture(t, s)` (1 project, 2 milestones, 1 workflow, 3 subworkflows,
  6 features, 1 persona, 1 comment on `features[0]`), and call `mcpXxx(ctx, s, args)`
  directly (NOT through the SDK transport). `SKIP_DB_TESTS=1` skips the DB harness.
- **Registration guard**: `mcp_registration_test.go` `TestMCPRegistrationCompleteness`
  asserts every `mcp[A-Z]*` handler with signature `(context.Context, Service, T) (U, error)`
  is registered via `add()`. Both new handlers MUST be registered or this fails.
- **Run tests**: `go test -run '^Test...' .` (use `.`, never `./...` -- the
  gitignored `data/` dir breaks the package glob). Build: `go build .`.

## File structure

- **Create** `mcp_reads.go` -- arg structs (`getFeatureArgs`, `queryBoardArgs`),
  result structs (`featureResult`, `queryBoardResult`), handlers (`mcpGetFeature`,
  `mcpQueryBoard`), the gojq runner helper (`runBoardFilter`), and the
  `queryBoardFilterDoc` description constant.
- **Create** `mcp_reads_test.go` -- tests for both handlers.
- **Modify** `repo.go` -- add `FindFeatureCommentsByFeatureID` to the `Repository`
  interface and the `*repo` impl.
- **Modify** `service.go` -- add `GetFeature` and `GetFeatureCommentsByFeature`
  to the `Service` interface and the `*service` impl.
- **Modify** `mcp.go` -- register `get_feature` and `query_board` in `buildMCPServer()`.
- **Modify** `go.mod` / `go.sum` -- add `github.com/itchyny/gojq v0.12.17`.
- **Modify** `readme.md`, `CLAUDE.md`, `CHANGELOG.md` -- docs (47 -> 49 tools).

---

## Task 1: Add the gojq dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the pinned dependency**

Run:
```bash
go get github.com/itchyny/gojq@v0.12.17
```
Expected: `go.mod` gains `require github.com/itchyny/gojq v0.12.17` (and its
transitive `github.com/itchyny/timefmt-go`). `go.sum` updated.

- [ ] **Step 2: Verify the module resolves and the tree still builds**

Run:
```bash
go build .
```
Expected: builds clean (no usage yet -- this just proves the dep resolves under
the repo's `-mod=readonly`/GOSUMDB hardening).

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "build: add itchyny/gojq v0.12.17 for MCP board filtering"
```

---

## Task 2: Repo method -- list a feature's comments

**Files:**
- Modify: `repo.go` (interface block near line 89; impl near line 580)
- Test: covered via the service test in Task 3 (repo has no standalone test file;
  the existing suite exercises the repo through the service, per convention)

- [ ] **Step 1: Add the method to the `Repository` interface**

In `repo.go`, in the interface block, directly below the existing
`FindFeatureCommentsByProject(workspaceID string, projectID string) ([]*FeatureComment, error)`
line (currently line 89), add:

```go
	FindFeatureCommentsByFeatureID(workspaceID string, featureID string) ([]*FeatureComment, error)
```

- [ ] **Step 2: Add the impl**

In `repo.go`, directly below the existing `FindFeatureCommentsByFeature` impl
(the single-row one, ends near line 586), add:

```go
// FindFeatureCommentsByFeatureID returns ALL comments on one feature, oldest
// first. (Distinct from FindFeatureCommentsByFeature, which .Get's a single row.)
func (a *repo) FindFeatureCommentsByFeatureID(workspaceID string, featureID string) ([]*FeatureComment, error) {
	x := []*FeatureComment{}
	err := a.tx.Select(&x, "SELECT * FROM feature_comments WHERE workspace_id = $1 AND feature_id = $2 ORDER BY created_at", workspaceID, featureID)
	if err != nil {
		return nil, errors.Wrap(err, "not found")
	}
	return x, nil
}
```

- [ ] **Step 3: Verify it compiles**

Run:
```bash
go build .
```
Expected: builds clean (interface and impl now match; if you added the interface
decl but not the impl, the build fails with `*repo does not implement Repository`).

- [ ] **Step 4: Commit**

```bash
git add repo.go
git commit -m "feat(repo): add FindFeatureCommentsByFeatureID (multi-row)"
```

---

## Task 3: `get_feature` tool (typed drill, optional comments)

**Files:**
- Create: `mcp_reads.go`
- Create: `mcp_reads_test.go`
- Modify: `service.go` (interface block; impl near the other feature getters ~line 1888)
- Modify: `mcp.go` (`buildMCPServer()`)

- [ ] **Step 1: Add the two service methods (interface + impl)**

In `service.go`, in the `Service` interface block, near
`GetFeaturesByProject(id string) []*Feature` (line 136) add:

```go
	GetFeature(id string) (*Feature, error)
	GetFeatureCommentsByFeature(featureID string) []*FeatureComment
```

In `service.go`, directly after the `GetFeaturesByProject` impl (ends ~line 1894),
add:

```go
func (s *service) GetFeature(id string) (*Feature, error) {
	return s.r.GetFeature(s.Member.WorkspaceID, id)
}

func (s *service) GetFeatureCommentsByFeature(featureID string) []*FeatureComment {
	cc, err := s.r.FindFeatureCommentsByFeatureID(s.Member.WorkspaceID, featureID)
	if err != nil {
		log.Println(err)
	}
	return cc
}
```

- [ ] **Step 2: Create `mcp_reads.go` with the get_feature pieces**

Create `mcp_reads.go`:

```go
package main

// MCP scoped-read tools: get_feature (typed single-card drill) and query_board
// (gojq filter/projection over the full board). These keep an agent from having
// to fetch the entire board (hundreds of KB) just to read a slice.
//
// Reads are read-only, so the always-commit Transaction() tx-poisoning gotcha
// does not apply -- handlers simply return errors, never panic.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/itchyny/gojq"
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
```

- [ ] **Step 3: Register `get_feature` in `mcp.go`**

In `mcp.go` `buildMCPServer()`, after the `get_board` registration (line 530-532),
add:

```go
	add(srv, "get_feature",
		"Read ONE feature (card) by id: full title, description, color, status, placement. Set include_comments=true to also get its comments. Use this to drill into a single card cheaply instead of fetching the whole board.",
		func(a getFeatureArgs) string { return a.WorkspaceID }, mcpGetFeature)
```

- [ ] **Step 4: Write the failing test for get_feature**

Create `mcp_reads_test.go`:

```go
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
```

- [ ] **Step 5: Run the test -- expect PASS**

Run:
```bash
go test -run '^Test_mcpGetFeature$' .
```
Expected: PASS. (Build must succeed first; if `*service` doesn't satisfy
`Service`, you missed an impl in Step 1.)

- [ ] **Step 6: Confirm registration guard still green**

Run:
```bash
SKIP_DB_TESTS=1 go test -run '^TestMCP' .
```
Expected: PASS -- `mcpGetFeature` is registered (Step 3). If it reports an orphan,
the registration line is missing or misspelled.

- [ ] **Step 7: Commit**

```bash
git add mcp_reads.go mcp_reads_test.go service.go mcp.go
git commit -m "feat(mcp): add get_feature tool (typed single-card read)"
```

---

## Task 4: `query_board` tool (gojq filter/projection)

**Files:**
- Modify: `mcp_reads.go` (add query_board pieces)
- Modify: `mcp_reads_test.go` (add query_board tests)
- Modify: `mcp.go` (`buildMCPServer()`)

- [ ] **Step 1: Add query_board structs, the filter doc, and the handler to `mcp_reads.go`**

Append to `mcp_reads.go` (the `gojq`, `encoding/json`, `fmt`, `strings` imports
are already added in Task 3):

```go
// --- query_board ---------------------------------------------------------

// queryBoardFilterDoc is embedded in the filter param description so the model
// sees the board shape AND worked examples right where it writes the filter.
const queryBoardFilterDoc = `A jq (gojq) program run over the FULL board JSON. ` +
	`Returns matched values wrapped as {"results":[...]}. ` +
	`Board shape: {project, milestones:[{id,title,color,status}], ` +
	`workflows:[{id,title}], subWorkflows:[{id,title,workflowId}], ` +
	`features:[{id,title,description,color,status,milestoneId,subWorkflowId,rank,estimate}], ` +
	`featureComments:[{id,featureId,post}], personas:[...], workflowPersonas:[...]}. ` +
	`Examples -- ` +
	`stubs for a prefix: ".features[] | select(.title|startswith(\"SYNC-\")) | {id,title,status,color}" ; ` +
	`one card body: ".features[] | select(.id==\"<uuid>\")" ; ` +
	`a card's comments: ".featureComments[] | select(.featureId==\"<uuid>\") | .post". ` +
	`Note: regex uses Go RE2 (no lookaround/backreferences).`

type queryBoardArgs struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"the workspace UUID"`
	ProjectID   string `json:"project_id" jsonschema:"the project UUID whose board to query"`
	Filter      string `json:"filter" jsonschema:"REQUIRED jq program; see description"`
}

// queryBoardResult wraps arbitrary jq output. Results is `any` so the SDK derives
// a permissive schema -- the {results:...} envelope validates, the inner content
// is unconstrained (caller-controlled projection).
type queryBoardResult struct {
	Results any `json:"results"`
}

func mcpQueryBoard(ctx context.Context, s Service, a queryBoardArgs) (*queryBoardResult, error) {
	if strings.TrimSpace(a.Filter) == "" {
		return nil, errors.New("filter is required (use get_board for the full untyped board)")
	}
	project := s.GetProject(a.ProjectID)
	if project == nil {
		return nil, errors.New("project not found")
	}
	board := boardResult{
		Project:          project,
		Milestones:       s.GetMilestonesByProject(a.ProjectID),
		Workflows:        s.GetWorkflowsByProject(a.ProjectID),
		SubWorkflows:     s.GetSubWorkflowsByProject(a.ProjectID),
		Features:         s.GetFeaturesByProject(a.ProjectID),
		FeatureComments:  s.GetFeatureCommentsByProject(a.ProjectID),
		Personas:         s.GetPersonasByProject(a.ProjectID),
		WorkflowPersonas: s.GetWorkflowPersonasByProject(a.ProjectID),
	}
	out, err := runBoardFilter(board, a.Filter)
	if err != nil {
		return nil, err
	}
	return &queryBoardResult{Results: out}, nil
}

// runBoardFilter marshals the board to generic JSON, compiles the jq program,
// runs it, and collects all emitted values. Parse/compile failures and runtime
// errors are returned as errors (never panics).
func runBoardFilter(board boardResult, filter string) ([]any, error) {
	query, err := gojq.Parse(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter: %w", err)
	}
	code, err := gojq.Compile(query)
	if err != nil {
		return nil, fmt.Errorf("invalid filter: %w", err)
	}

	// gojq operates on generic interface{} data, not Go structs.
	raw, err := json.Marshal(board)
	if err != nil {
		return nil, fmt.Errorf("marshaling board: %w", err)
	}
	var input any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("decoding board: %w", err)
	}

	results := []any{}
	iter := code.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if e, ok := v.(error); ok {
			if he, halt := e.(*gojq.HaltError); halt && he.Value() == nil {
				break
			}
			return nil, fmt.Errorf("filter error: %w", e)
		}
		results = append(results, v)
	}
	return results, nil
}
```

- [ ] **Step 2: Register `query_board` in `mcp.go`**

In `mcp.go` `buildMCPServer()`, directly after the `get_feature` registration
added in Task 3, add:

```go
	add(srv, "query_board",
		"Run a jq filter/projection over a project's full board and get back only the matched values ({results:[...]}). Use this to read a slice (e.g. all SYNC-* cards) or specific fields without fetching the entire board. See the filter param for board shape + examples.",
		func(a queryBoardArgs) string { return a.WorkspaceID }, mcpQueryBoard)
```

Note: the rich shape/recipe doc lives in `queryBoardFilterDoc` on the `Filter`
field's jsonschema tag is NOT possible (struct tags take only string literals),
so it is surfaced two ways: (a) the short hint in the `filter` jsonschema tag
above, and (b) `queryBoardFilterDoc` is appended to the tool description -- update
the registration to use it:

```go
	add(srv, "query_board",
		"Run a jq filter/projection over a project's full board and get back only the matched values ({results:[...]}). Use this to read a slice (e.g. all SYNC-* cards) or specific fields without fetching the entire board. "+queryBoardFilterDoc,
		func(a queryBoardArgs) string { return a.WorkspaceID }, mcpQueryBoard)
```

(Use this second form; delete the first if you typed it.)

- [ ] **Step 3: Write failing tests for query_board**

Append to `mcp_reads_test.go`:

```go
func Test_mcpQueryBoard(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member) {
		fx := newProjectFixture(t, s)

		// Projection: select features in SW1's column, return id+title stubs.
		// Fixture feature titles are "F-<milestone>-<subworkflow>", e.g. "F-M1-SW1".
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
```

- [ ] **Step 4: Run the tests -- expect PASS**

Run:
```bash
go test -run '^Test_mcpQueryBoard$' .
```
Expected: PASS. If the projection count is wrong, double-check the fixture title
format (`"F-"+m.Title+"-"+sw.Title`, e.g. `F-M1-SW1`) in `mcp_helpers_test.go`.

- [ ] **Step 5: Confirm registration guard + full read suite green**

Run:
```bash
SKIP_DB_TESTS=1 go test -run '^TestMCP' .
go test -run '^Test_mcp(GetFeature|QueryBoard)$' .
```
Expected: both PASS. `TestMCPRegistrationCompleteness` now counts both new
handlers as registered.

- [ ] **Step 6: Commit**

```bash
git add mcp_reads.go mcp_reads_test.go mcp.go
git commit -m "feat(mcp): add query_board tool (gojq board filter/projection)"
```

---

## Task 5: Full verification + docs + changelog

**Files:**
- Modify: `readme.md`, `CLAUDE.md`, `CHANGELOG.md`

- [ ] **Step 1: Full build + vet + deadcode + suite**

Run:
```bash
go build . && go vet . && ./scripts/deadcode-check.sh
SKIP_DB_TESTS=1 go test -run '^TestMCP' .
go test .
```
Expected: build clean; vet clean; `deadcode-check: OK` (the two new handlers are
reachable from `buildMCPServer`, so NOT reported); registration guard PASS; full
suite PASS.

- [ ] **Step 2: Update `readme.md` tool table + count**

In `readme.md`: change "48 tools are registered" (line ~177) to "49 tools are
registered" -- wait, verify the current count first:
```bash
grep -c 'add(srv,' mcp.go
```
Set the readme/CLAUDE counts to that number. Add the two tools to the Discovery
row of the tool table:

```
| Discovery | `list_workspaces`, `list_projects`, `get_board`, `get_feature`, `query_board` |
```

And add a short paragraph after the bulk-tools description documenting
`query_board` (jq filter, `{results:[...]}` envelope, board shape) and
`get_feature` (typed single-card read, `include_comments`).

- [ ] **Step 3: Update `CLAUDE.md` MCP section**

In `CLAUDE.md`, update the tool count (search for the current count, e.g. "47
tools" / "48 tools") to the new number, and add a line to the `mcp.go` /
architecture description noting `mcp_reads.go` holds the scoped-read tools
(`get_feature`, `query_board`) and that `query_board` filters via gojq.

- [ ] **Step 4: Add CHANGELOG entry**

In `CHANGELOG.md` under `## [Unreleased]` -> `### Added`, append:

```markdown
- Scoped MCP read tools so agents can read a board slice without the full-board
  dump: `get_feature` (one card by id, typed/schema-validated, optional
  `include_comments`) and `query_board` (a jq/gojq filter+projection over the
  board, returning matched values as `{results:[...]}`). `get_board` is
  unchanged. gojq runs server-side; malformed filters return a clean parse error
  (`mcp_reads.go`, `repo.go` `FindFeatureCommentsByFeatureID`, `service.go`
  `GetFeature`/`GetFeatureCommentsByFeature`).
```

- [ ] **Step 5: Commit**

```bash
git add readme.md CLAUDE.md CHANGELOG.md
git commit -m "docs: document get_feature + query_board MCP tools (49 tools)"
```

- [ ] **Step 6: Rebuild the running container so the new tools go live**

The MCP server in the local docker stack must be rebuilt to expose the new tools.

Run:
```bash
docker compose up -d --build
```
Expected: `featmap` image rebuilds and the container restarts healthy. After this,
the USER runs `/mcp` in their client to reconnect the featmap server and pick up
`get_feature` + `query_board` (no full Claude restart needed).

- [ ] **Step 7: Live smoke test (after reconnect)**

Once reconnected, verify against the real roadmap board (project
`5f0687bb-403d-4c1a-88bb-7e2cb6b864ad`, workspace `6c031636-7ac2-4882-9983-26468f242f63`):

```
query_board(workspace_id=..., project_id=..., filter='.features[] | select(.title|startswith("SYNC-")) | {id,title,status,color}')
```
Expected: ~15 SYNC-* stubs under `results`, a few KB -- not 433K. Then
`get_feature` on one of those ids with `include_comments=true`. This replaces the
SQL queries used while planning.

---

## Self-review notes (filled by author)

- **Spec coverage:** query_board (gojq, `{results}` envelope) = Tasks 1+4;
  get_feature (typed, include_comments) = Tasks 2+3; shape doc/recipes =
  `queryBoardFilterDoc` (Task 4); auto output schema = inherent to typed
  `featureResult` + permissive `queryBoardResult`; registration coverage = Steps
  in Tasks 3/4; error handling (empty/parse/runtime/not-found) = Task 4 handler +
  tests; docs/count = Task 5. No spec section left unmapped.
- **No placeholders:** every code/test block is complete and runnable.
- **Type consistency:** `getFeatureArgs`/`featureResult`/`mcpGetFeature`,
  `queryBoardArgs`/`queryBoardResult`/`mcpQueryBoard`/`runBoardFilter`,
  `FindFeatureCommentsByFeatureID`, `GetFeature`/`GetFeatureCommentsByFeature`
  used consistently across tasks. Board getters match `mcpGetBoard`'s set exactly.
