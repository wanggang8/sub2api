# Observer Data Export Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add independently authenticated, repeatable observer data exports to embedded sub2api, preserve upload availability and idempotency, then publish observer 0.3.2 in sub2api 0.1.181.

**Architecture:** `backend/internal/observercontrol` snapshots current immutable uploads and atomic heartbeat files with hard links under a short data lock, builds an immutable `tar.gz` in `exports/`, then removes only source paths that still refer to the snapshotted inode. Export receipts preserve observation upload idempotency after source archives are cleaned. The raw export token remains in observer-local ignored evidence; sub2api reads only its SHA-256 from `OBSERVER_EXPORT_TOKEN_SHA256`.

**Tech Stack:** Go standard library HTTP, `archive/tar`, `compress/gzip`, SHA-256, filesystem atomic rename/hard links, existing `testify/require`, GitHub Actions/GoReleaser.

## Global Constraints

- Do not embed or commit the raw export token or its configured digest.
- Do not grant export access to the shared observer agent token.
- Existing heartbeat, upload, release lookup, and artifact download contracts must remain compatible.
- Export creation must not delete data until the completed package is persisted and verified.
- New uploads and heartbeats after the snapshot must remain for the next export.
- Generated files and directories use `0600` and `0700` permissions respectively.
- Preserve all unrelated user changes in the observer workspace.

---

### Task 1: Export authentication and storage initialization

**Files:**
- Modify: `backend/internal/observercontrol/server.go`
- Modify: `backend/internal/observercontrol/embedded.go`
- Test: `backend/internal/observercontrol/server_test.go`

**Interfaces:**
- Consumes: `Config.DataDir`, `bearerToken`, `decodeSHA256`, `NewEmbedded`.
- Produces: `Config.ExportTokenSHA256 string`, optional export-token state on `Server`, and initialized `exports/` plus `export-receipts/` directories.

- [ ] **Step 1: Write failing tests** for configured export-token authentication, agent-token rejection, missing/invalid export-token configuration, and restrictive new directory permissions.
- [ ] **Step 2: Run `go test ./internal/observercontrol -run 'TestExportAuth|TestNewExportDirectories' -count=1` from `backend/`; expect failure before implementation.**
- [ ] **Step 3: Add `ExportTokenSHA256` to `Config`, decode non-empty valid digests into a separate hash, leave exports disabled for missing/invalid values, initialize `exports` and `export-receipts`, and read `OBSERVER_EXPORT_TOKEN_SHA256` in `NewEmbedded`.**
- [ ] **Step 4: Add constant-time `authenticateExport`; return `503 export_not_configured` when disabled and `401 unauthorized` for the wrong token.**
- [ ] **Step 5: Run the focused tests; expect PASS.**

### Task 2: Snapshot, package, persist, and repeat-download exports

**Files:**
- Create: `backend/internal/observercontrol/export.go`
- Modify: `backend/internal/observercontrol/server.go`
- Test: `backend/internal/observercontrol/export_test.go`

**Interfaces:**
- Consumes: `Server.dataDir`, `Server.now`, `writeAtomic`, `writeError`, and export authentication from Task 1.
- Produces: `POST /api/v1/observer/exports`, `GET /api/v1/observer/exports/{export_id}`, `exportManifest`, and immutable `exports/<export_id>.tar.gz`.

- [ ] **Step 1: Write a failing end-to-end handler test** that seeds one heartbeat and one observation, POSTs with the export token, parses the returned tar.gz, and asserts fixed members `manifest.json`, `observations/<upload_id>.tar.gz`, and `agents/<installation_id>.json` with matching manifest size/SHA-256.
- [ ] **Step 2: Write failing tests** for no data (`409 no_exportable_data`), concurrent creation (`409 export_in_progress`), invalid export ID (`404`), and repeat GET returning byte-identical content and checksum headers.
- [ ] **Step 3: Run `go test ./internal/observercontrol -run 'TestExport' -count=1`; expect failures because routes and implementation do not exist.**
- [ ] **Step 4: Implement a short locked snapshot:** validate regular non-symlink source files, hard-link them into `.export-<id>/observations` and `.export-<id>/agents`, then release the data lock before hashing/compression.
- [ ] **Step 5: Implement deterministic member metadata and tar.gz creation:** sort member names, calculate size/SHA-256, write `manifest.json`, use `0600` tar modes, close/sync the temporary file, verify its digest, and atomically rename it to `exports/<id>.tar.gz`.
- [ ] **Step 6: Register the POST and GET routes and stream retained packages with `Content-Disposition`, `Content-Length`, `X-Observer-Export-ID`, and `X-Checksum-SHA256`.**
- [ ] **Step 7: Run the focused export tests; expect PASS.**

### Task 3: Safe cleanup, upload receipts, and concurrency recovery

**Files:**
- Modify: `backend/internal/observercontrol/export.go`
- Modify: `backend/internal/observercontrol/upload.go`
- Modify: `backend/internal/observercontrol/server.go`
- Test: `backend/internal/observercontrol/export_test.go`
- Test: `backend/internal/observercontrol/server_test.go`

**Interfaces:**
- Consumes: snapshot hard links and export manifest from Task 2.
- Produces: `export-receipts/<upload_id>.json`, inode-safe source cleanup, and startup/next-export staging recovery.

- [ ] **Step 1: Write failing tests** proving an observation included in a successful export is removed, its duplicate upload still returns the original ID without recreating a source archive, and a heartbeat replaced after snapshot remains active for the next export.
- [ ] **Step 2: Write failing recovery tests:** incomplete staging without a completed export is discarded without deleting sources; completed export plus staging resumes receipt creation and deletes only unchanged source inodes.
- [ ] **Step 3: Run focused tests and confirm the cleanup/idempotency assertions fail.**
- [ ] **Step 4: Guard heartbeat writes and upload persistence with `Server.dataMu`; make upload check a validated receipt before writing its archive.**
- [ ] **Step 5: After package persistence, create observation receipts atomically and delete only active paths for which `os.SameFile(active, snapshot)` is true; apply the same inode check to heartbeat cleanup without receipts.**
- [ ] **Step 6: Add recovery that inspects private `.export-*` staging directories: if the matching completed package exists, finish idempotent cleanup; otherwise remove only staging hard links.**
- [ ] **Step 7: Run all observercontrol tests with the race detector; expect PASS.**

### Task 4: Operator secret and documentation

**Files:**
- Modify: `deploy/.env.example`
- Modify: `deploy/docker-compose.yml`
- Modify: `README_CN.md`
- Modify: `/Users/vick/Desktop/project/fofa-observer-agent/README.md`
- Create locally (ignored): `/Users/vick/Desktop/project/fofa-observer-agent/local-evidence/embedded-release-secrets/observer-export.token`
- Create locally (ignored): `/Users/vick/Desktop/project/fofa-observer-agent/local-evidence/embedded-release-secrets/observer-export-token.sha256`

**Interfaces:**
- Consumes: endpoint/header/env names from Tasks 1–3.
- Produces: deployment instructions and a local `0600` raw token plus digest for server configuration.

- [ ] **Step 1: Generate a cryptographically random 32-byte token without printing it; store it with `0600` mode and write `sha256:<hex>` to the ignored digest file.**
- [ ] **Step 2: Add the optional environment variable to `.env.example` without any real secret.**
- [ ] **Step 3: Document token generation/storage, POST export, repeat GET, response checksum validation, retention, cleanup boundary, and uninterrupted subsequent uploads.**
- [ ] **Step 4: Scan tracked diffs for token-like values and verify both local secret files are ignored and `0600`.**

### Task 5: Observer 0.3.2 and sub2api 0.1.181 release verification

**Files:**
- Modify: `backend/cmd/server/VERSION`
- Replace: `backend/internal/observercontrol/assets/fobrain-net-observer-linux-amd64`
- Modify: `backend/internal/observercontrol/assets/release-manifest.json`
- Modify: `backend/internal/observercontrol/server_test.go`

**Interfaces:**
- Consumes: existing offline observer signing key and agent-token trust chain.
- Produces: signed observer `0.3.2`, embedded sub2api `0.1.181`, commit, annotated tag, pushed branch/tag, and verified GitHub Release.

- [ ] **Step 1: Run observer `go test ./...`, `go vet ./...`, and `make verify-deps`; expect PASS.**
- [ ] **Step 2: Build Linux amd64 observer with `VERSION=0.3.2`, the existing agent token/public key, and the current verified workspace commit marker; regenerate and verify the Ed25519-signed manifest without exposing secrets.**
- [ ] **Step 3: Embed the new binary/manifest, update the embedded-version assertion to `0.3.2`, and set sub2api VERSION to `0.1.181`.**
- [ ] **Step 4: Run `gofmt` on changed Go files, `go test -race ./internal/observercontrol`, full `go test ./...`, `go vet ./...`, and a Linux amd64 release build; expect PASS.**
- [ ] **Step 5: Review the final diff for scope, filesystem safety, authentication separation, test coverage, and accidental secrets.**
- [ ] **Step 6: Commit with a focused Conventional Commit message, create annotated tag `v0.1.181`, push the current branch and tag to `myfork`, and wait for the release workflow.**
- [ ] **Step 7: Verify the published Release metadata, download Linux amd64 plus `checksums.txt`, and confirm SHA-256 equality before reporting completion.**
