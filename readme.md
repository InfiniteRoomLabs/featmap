# Featmap

Featmap is a user story mapping tool for product people to build, plan and communicate product backlogs.

![Featmap screenshot](screenshot.png)

- [Introduction](#introduction)
  - [Purpose](#purpose)
  - [Features](#features)
  - [Intended audience](#intended-audience)
  - [Motivation](#motivation)
- [Getting started](#getting-started) 
- [Self hosting](#self-hosting) 
  - [Requirements](#requirements) 
  - [Download](#download) 
  - [Configuration](#configuration) 
  - [Run](#run) 
  - [Upgrade](#upgrade) 
  - [Building from source and running with docker-compose](#Building-from-source-and-running-with-docker-compose)
- [License](#license) 

## Introduction
 Featmap is an open source user story mapping tool. It is built using React, Typescript and Go. 
 ### Purpose
Featmap was built for product people to take advantage of a technique called *user story mapping*. User story mapping, or just story mapping, is an effective tool to create, plan and communicate your product backlog. Story mapping was created by Jeff Patton and its primary utility is providing us with an overview of the entire product and how user goals are broken down into a series of tasks. Finally, it helps us to define valuable product slices (releases) and prioritize between them.
### Features
* Personas
* Markdown editing
* Discuss user stories
* Share your user story maps with external stakeholders
* User story annotations
* User story estimates with roll-ups
* **Account-scoped API keys** for scripts and automation (Authorization: Bearer ...)
* **Built-in MCP server** at `/mcp` -- drive any board from a local LLM agent over Model Context Protocol (Streamable HTTP transport)

### Intended audience
Featmap is great for product managers, product owners or just about anyone who is building products. Featmap can also be used as a light weight work item management system for development teams.

### Motivation
There are many user story mapping tools, however none are really focused on easy-of-use and simplicity. Featmap was built to fill that gap. We hope you will find it as useful as we found building it.
## Getting started
You have two choices when it comes to using Featmap.
1. Use our hosted service at https://www.featmap.com. This is the most simple way of using Featmap. Note that we also offer a free trial.
2. Host it yourself by running it on you own server, without cost. Please refer to the [instructions](#self-hosting) for self-hosting.
## Self hosting
Featmap can be run on your own server.
### Requirements
Featmap runs on top of [PostgreSQL](https://www.postgresql.org/), so make sure you have it running on your system. At this step, make sure to setup the credentials and database that Featmap will use.
### Download
[Download](https://github.com/amborle/featmap/releases) the Featmap binary for your respective platform and save it somewhere on your system. If needed, make it executable on your system.
### Configuration
In the directory where you placed the binary, create a file called ```conf.json```.

Here's a sample  ```conf.json``` you can use:

```json
{
  "appSiteURL": "https://localhost:5000",
  "dbConnectionString": "postgresql://postgres:postgres@postgres:5432/postgres?sslmode=disable",
  "jwtSecret": "ChangeMeForProduction",
  "port": "5000",
  "emailFrom": "",
  "smtpServer": "",
  "smtpPort": "587",
  "smtpUser": "",
  "smtpPass": "",
  "environment": "development"
}
```
Setting | Description
--- | --- 
`appSiteURL` | The url to where you will be hosting the app.
`dbConnectionString` | The connection string to the PostgreSQL database that Featmap should connect to.
`jwtSecret` | This setting is used to secure the cookies produced by Featmap. Generate a random string and keep it safe! 
`port` | The port that Featmap should run on.
`emailFrom` | The email adress that should be used as sender when sending invitation and password reset mails.
`smtpServer` | SMTP server for sending emails.
`smtpPort` | **Optional** Will default to port 587 if not specified. 
`smtpUser` | SMTP server username.
`smtpPass` | SMTP server password.
`environment` |  **Optional** If set to `development`, Featmap assumes your are **not** running on **https** and the the backend will not serve secure cookies. Remove this setting if you have set it up to run https.
### Run
Execute the binary.

```bash
./featmap-1.0.0-linux-amd64
Serving on port 5000
```
Open a browser to http://localhost:5000 and you are ready to go!
### Upgrading
Just download the latest release and swap out the executable. Remember to backup your database and the old executable.

## Building from source and running with docker-compose

Clone the repository

```bash
git clone https://github.com/amborle/featmap.git
```

Navigate to the repository.

```bash
cd featmap
```

Let's copy the configuration files

```bash
cp config/.env .
cp config/conf.json .
```

Now let's build it.

```bash
docker-compose build
```

Startup the services, the app should now be available on the port you defined in you configuration files (default 5000).
```bash
docker-compose up -d
```

### Upgrading
Remember to backup your database (/data), just in case.

Pull down the latest source
```bash
git pull
```
Now let's rebuild it.
```bash
docker-compose build --no-cache
```
And finally run it.
```bash
docker-compose up -d
```

## API keys

Featmap supports per-account API keys for scripting and automation. Open the **Account settings** page (Account menu -> Settings), scroll to **API keys**, name a key, and click **Create key**. The full token is shown exactly once -- copy it immediately.

Keys are SHA-256 hashed at rest; only an 8-character prefix is stored alongside for display. Each request authenticates by sending the plaintext token in an HTTP header:

```
Authorization: Bearer <your-api-key>
```

Keys carry the privileges of the account that created them; pass the workspace UUID in the `Workspace:` header (same convention as the cookie-based UI) when calling workspace-scoped endpoints. Revoke a key any time from the same settings page.

## MCP server

Featmap exposes the bulk of its workspace API as a **Model Context Protocol** server at `/mcp`, intended for local LLM-driven automation. The endpoint speaks the Streamable HTTP transport (single POST endpoint, JSON-RPC 2.0 payloads, optional SSE upgrade for server-initiated messages -- unused in this version).

### Configuring a client

Most MCP clients (Claude Desktop, Claude Code, Cursor, etc.) accept a JSON config along these lines:

```json
{
  "mcpServers": {
    "featmap": {
      "type": "http",
      "url": "http://localhost:5000/mcp",
      "headers": {
        "Authorization": "Bearer <your-api-key>"
      }
    }
  }
}
```

### Available tools

47 tools are registered. Workspace context is passed as a tool argument (`workspace_id`), not via the `Workspace` header, so a single key can drive any workspace the owning account belongs to.

| Group | Tools |
|---|---|
| Discovery | `list_workspaces`, `list_projects`, `get_board` |
| Project | `create_project` |
| Milestone | `create_milestone`, `update_milestone`, `move_milestone`, `set_milestone_color`, `set_milestone_status` |
| Workflow | `create_workflow`, `update_workflow`, `move_workflow`, `set_workflow_color`, `set_workflow_status` |
| Subworkflow | `create_subworkflow`, `update_subworkflow`, `move_subworkflow`, `set_subworkflow_color`, `set_subworkflow_status` |
| Feature | `create_feature`, `update_feature`, `rename_feature`, `update_feature_description`, `move_feature`, `delete_feature`, `set_feature_color`, `set_feature_status` |
| Comments | `add_comment` |
| Personas | `create_persona`, `update_persona`, `delete_persona`, `attach_persona_to_workflow`, `detach_persona_from_workflow` |
| Bulk: create | `bulk_create_features`, `bulk_create_milestones`, `bulk_create_workflows`, `bulk_create_subworkflows`, `bulk_create_personas` |
| Bulk: update | `bulk_update_features`, `bulk_update_milestones`, `bulk_update_workflows`, `bulk_update_subworkflows` |
| Bulk: other | `bulk_add_comment`, `bulk_attach_personas`, `bulk_detach_personas`, `bulk_reorder_features`, `bulk_delete_features`, `bulk_delete_personas` |

Status tools accept `OPEN` or `CLOSED`. Color tools accept any of: `WHITE`, `GREY`, `RED`, `ORANGE`, `YELLOW`, `GREEN`, `TEAL`, `BLUE`, `INDIGO`, `PURPLE`, `PINK`. Avatars are `avatar00` through `avatar08`.

`update_feature` is a unified **partial** update: pass `feature_id` plus any subset of `title`, `description`, `color`, `status`, `to_milestone_id`, `to_subworkflow_id`, `index` -- omitted fields are left unchanged. It supersedes `rename_feature` / `update_feature_description` / `set_feature_color` / `set_feature_status` / `move_feature` (which remain for compatibility). `update_milestone`, `update_workflow`, `update_subworkflow`, and `update_persona` are partial in the same way -- only the fields you pass change.

The `bulk_*` tools take an `items` array (max 100) and return `{ "results": [ { "index", "ok", "id", "error" } ] }`, one entry per input item in order. Items are best-effort and isolated by per-item savepoints: a failing item reports its error in its own slot without aborting the others or the surrounding transaction.

`bulk_reorder_features` is the exception -- it is all-or-nothing and must list **every** feature in the target `(milestone, subworkflow)` cell exactly once (a count mismatch, non-member, or duplicate rejects the whole call with no writes); the server assigns the lexorank chain so the cell ends in exactly the given order.

### Recipe: bootstrap a board from zero

```
list_workspaces                                            -> grab workspace_id
create_project       workspace_id=..., title=...           -> project_id
create_milestone     workspace_id=..., project_id=..., title="M1: MVP"
create_workflow      workspace_id=..., project_id=..., title="User Journey"
create_subworkflow   workspace_id=..., workflow_id=..., title="Sign Up"
create_feature       workspace_id=..., subworkflow_id=..., milestone_id=..., title="Email signup"
set_feature_color    workspace_id=..., feature_id=..., color="RED"
add_comment          workspace_id=..., feature_id=..., body="**bold** markdown"
get_board            workspace_id=..., project_id=...      -> full snapshot
```

The server returns rich JSON for every mutation, so an LLM agent can chain operations without round-tripping through `get_board`.

### Security notes

- Bind to `127.0.0.1` for local-only access. The MCP endpoint sits behind the same `RequireAccount` middleware as the rest of the API, but exposing it publicly without TLS + rate limiting is not recommended.
- API keys cannot be recovered after creation. Revoke compromised keys immediately.
- DNS rebinding protection is enabled by default in the upstream MCP Go SDK -- the `Host` header must match a loopback name for `/mcp` requests.

## License
Featmap is licensed under Business Source License 1.1. See [license](https://github.com/amborle/featmap/blob/master/LICENSE)
