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
- Built-in MCP server at `/mcp` exposing 29 workspace tools to local LLM agents over
  the Model Context Protocol (Streamable HTTP, Stateless transport) -- workspaces,
  projects, milestones, workflows, subworkflows, features, comments, and personas
  (`mcp.go`, mounted in `main.go` behind `RequireAccount`).
- `ApiKeysSection` panel on the account page with one-shot plaintext reveal,
  copy-to-clipboard, and revoke (`webapp/src/components/ApiKeysSection.tsx`).

### Changed
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
