# Plane Comment Sync -- Live Smoke Test

> Run this in a fresh session AFTER secrets management is reworked and you have a real
> Plane API key. It validates the shipped SYNC v1 feature against a real Plane instance
> (the test suite already covers logic + SSRF + echo-prevention against fakes; this is
> the end-to-end "does it actually talk to Plane" check). ~15 minutes.

**Status:** not yet run. Feature merged to `master` (commit `3bcee60`). 56 MCP tools live.

## What you need first

- A real Plane API key (`X-API-Key`) with access to a Plane workspace + at least one
  project containing at least one work item.
- The Plane workspace **slug** (the `workspace-slug` in Plane URLs) and the **base URL**
  (`https://api.plane.so` for cloud; your self-host URL otherwise).
- The featmap repo at `~/projects/infinite-room-labs/third-party/featmap`.

## Step 1 -- configure the encryption key + rebuild

The Plane API key is encrypted at rest with `planeEncryptionKey`; without it,
connections cannot be created.

```bash
cd ~/projects/infinite-room-labs/third-party/featmap
# generate a 32-byte base64 key
head -c 32 /dev/urandom | base64
```

Add to the runtime `conf.json` at the repo root (gitignored scratch; create from
`config/conf.json` if absent):

```json
{
  "planeEncryptionKey": "<the base64 key from above>",
  "planePollInterval": "0",
  "planeAllowPrivateHosts": false
}
```

Notes:
- `planePollInterval: "0"` disables the background poller for the smoke test (you trigger
  syncs manually). Set e.g. `"5m"` later to enable it.
- Leave `planeAllowPrivateHosts: false` for cloud Plane. Set `true` ONLY if your Plane is
  self-hosted on a private/loopback/internal address (the SSRF guard blocks those
  otherwise -- by design).

Rebuild the container so it picks up the conf change, then reconnect the MCP client:

```bash
docker compose up -d --build
```

Then in the Claude session run `/mcp` to reconnect the `featmap` server (the 6 Plane tools
must appear: `set_plane_connection`, `get_plane_connection`, `test_plane_connection`,
`link_feature_to_plane`, `unlink_feature_from_plane`, `plane_sync`).

## Step 2 -- pick a Featmap card to link

Workspace id: `6c031636-7ac2-4882-9983-26468f242f63`. Use any project; the roadmap project
`5f0687bb-403d-4c1a-88bb-7e2cb6b864ad` is fine. Grab a card id to link:

```
query_board(
  workspace_id="6c031636-7ac2-4882-9983-26468f242f63",
  project_id="5f0687bb-403d-4c1a-88bb-7e2cb6b864ad",
  filter='.features[] | select(.title|startswith("SYNC-")) | {id,title}'
)
```

Pick one feature `id` -> call it `<FEATURE_ID>`. Pick its target Plane project id
(`<PLANE_PROJECT_ID>`) and a work item id in that project (`<WORK_ITEM_ID>`) from Plane.

> Prefer a throwaway/test work item -- this WILL post comments to it.

## Step 3 -- the smoke sequence (via MCP tools)

Run these in order; the **Expected** line is the pass criterion.

1. **Set connection**
   ```
   set_plane_connection(workspace_id="6c03...", project_id="5f06...",
     base_url="https://api.plane.so", plane_workspace="<SLUG>",
     api_key="<REAL_KEY>", watched_projects="<PLANE_PROJECT_ID>")
   ```
   Expected: returns the connection with `apiKeyHint` = last 4 of the key; NO full key in
   the response.

2. **Test connection**
   ```
   test_plane_connection(workspace_id="6c03...", project_id="5f06...")
   ```
   Expected: `{"ok": true}`. (A bad key -> a specific 401 error, not a 500.)

3. **Link the card**
   ```
   link_feature_to_plane(workspace_id="6c03...", feature_id="<FEATURE_ID>",
     plane_project_id="<PLANE_PROJECT_ID>", plane_work_item_id="<WORK_ITEM_ID>")
   ```
   Expected: returns a link with `lastStatus: "pending"`.

4. **Post a Featmap comment**, then sync push:
   ```
   add_comment(workspace_id="6c03...", feature_id="<FEATURE_ID>", body="smoke test from featmap")
   plane_sync(workspace_id="6c03...", project_id="5f06...", feature_id="<FEATURE_ID>")
   ```
   Expected: `plane_sync` returns `{pushed: 1, pulled: 0, ...}`. Open the work item in Plane
   -> the "smoke test from featmap" comment is there.

5. **Add a comment ON the Plane work item** (in the Plane UI), then sync pull.
   To also confirm the stored-XSS guard, make the Plane comment contain HTML/markup,
   e.g. `<b>bold</b> and <script>alert(1)</script>`:
   ```
   plane_sync(workspace_id="6c03...", project_id="5f06...", feature_id="<FEATURE_ID>")
   ```
   Expected: `{pushed: 0, pulled: 1, ...}`. Read the card back:
   ```
   get_feature(workspace_id="6c03...", feature_id="<FEATURE_ID>", include_comments=true)
   ```
   -> the Plane comment appears as a featmap comment, but **as sanitized plain text**:
   pulled `comment_html` is run through `bluemonday.StrictPolicy()` before storage, so
   ALL tags are stripped (no `<script>`, no `<b>`, no event handlers survive) -- the
   `post` is the visible text only (e.g. `bold and alert(1)`). This is intentional:
   `post` is a markdown field, the content is untrusted external HTML, and full
   md<->html fidelity is deferred (icebox ICE-034). Confirm the stored `post` contains
   NO `<script` / no tags.

6. **Idempotency / no-echo** -- run sync a third time with no new comments:
   ```
   plane_sync(workspace_id="6c03...", project_id="5f06...", feature_id="<FEATURE_ID>")
   ```
   Expected: `{pushed: 0, pulled: 0, ...}`. No duplicate comments on either side.

7. **Encryption-at-rest check** (the key must never be plaintext in the DB):
   ```bash
   docker compose exec -T postgres psql -U postgres -d postgres -At \
     -c "select api_key_hint, encode(api_key_cipher,'hex') from plane_connections;"
   ```
   Expected: `api_key_hint` is only the last 4 chars; the cipher column is opaque hex, NOT
   the real key.

8. **SSRF guard (negative check)** -- confirm a private host is rejected:
   ```
   set_plane_connection(workspace_id="6c03...", project_id="5f06...",
     base_url="http://169.254.169.254", plane_workspace="x", api_key="x", watched_projects="")
   ```
   Expected: ERROR ("private/loopback/link-local address ... set planeAllowPrivateHosts").
   This must NOT overwrite the working connection (re-run step 2 to confirm it still works).

## Step 4 -- cleanup

```
unlink_feature_from_plane(workspace_id="6c03...", feature_id="<FEATURE_ID>")
```
Delete the smoke comments from the Plane work item + the Featmap card if you used a real
card. (Step 8 already left the good connection intact; delete it via DB if you want a clean
slate: `delete from plane_connections where project_id='5f06...';`.)

## If something fails

- Tools missing after `/mcp`: the container didn't rebuild or reconnect didn't happen.
  `docker compose ps` (featmap up?), re-run `/mcp`.
- `set_plane_connection` errors "planeEncryptionKey not configured": the conf field is
  empty or the container wasn't rebuilt after editing conf.json.
- `test_plane_connection` 401: wrong key/slug. Unreachable: wrong base URL.
- A push/pull count is off: read `get_feature(..., include_comments=true)` and the
  `plane_comment_map` table (`select origin, plane_comment_id, featmap_comment_id from
  plane_comment_map;`) to see what got mapped.

Reference: spec `docs/superpowers/specs/2026-05-28-plane-comment-sync-design.md`,
impl plan `docs/superpowers/plans/2026-05-28-plane-comment-sync.md`.
