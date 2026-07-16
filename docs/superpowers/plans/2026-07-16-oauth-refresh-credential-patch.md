# OAuth Refresh Credential Patch Plan

**Goal:** Prevent an in-flight OAuth token refresh from overwriting account settings, including `model_mapping`, that an administrator saves concurrently.

**Architecture:** Treat the credentials read before the upstream refresh as a baseline. Compute the top-level fields that the refresher actually changed or removed, then apply only that delta atomically to the current PostgreSQL `credentials` JSONB value. Keep the existing full-replacement persistence path for callers that intentionally replace credentials.

**Scope:** OAuth refresh persistence, the account repository JSONB update, scheduler snapshot refresh, and focused service/repository regression tests. No frontend behavior or API contract changes.

## Tasks

1. Add a failing OAuth refresh test that simulates an administrator changing `model_mapping` while the upstream token request is in flight.
2. Add credential-delta calculation and route OAuth refresh persistence through an optional patch-capable repository interface.
3. Implement an atomic PostgreSQL top-level credential patch that supports both setting and deleting fields.
4. Add repository coverage for the generated JSONB update and verify scheduler cache synchronization remains intact.
5. Run focused OAuth refresh and account repository tests, then broader service and frontend account tests.

## Correctness Rules

- Refresh-owned changes such as `access_token`, `refresh_token`, `expires_at`, and `_token_version` are persisted.
- Unchanged fields inherited from the refresh baseline are not written, so a concurrent `model_mapping` edit survives.
- Fields intentionally removed by a refresher are removed atomically.
- Spark shadow credential invariants remain unchanged.
- Repositories without patch support retain the existing full-update fallback for tests and alternate implementations.

