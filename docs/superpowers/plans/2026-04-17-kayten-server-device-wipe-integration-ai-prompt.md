# kayten-server device wipe + self-revoke capability — AI prompt

- **Date:** 2026-04-17
- **Repo scope:** `/Users/tahir/Repos/kayten-server`
- **Authoritative repo spec:** [`2026-04-17-kayten-server-device-wipe-integration-spec.md`](2026-04-17-kayten-server-device-wipe-integration-spec.md)
- **Parent spec:** [`2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`](2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md)
- **Upstream dependency:** `2026-04-17-kayten-proto-hsm-keys-constants-spec.md` MUST be merged first — it publishes `GetIdentityKeyResponse.revoked_at` and the two new capability RPCs (`IssueSelfRevokeCapability`, `RevokeDeviceByCapability`).

---

```text
You are Claude (Opus or Sonnet) operating inside the `kayten-server`
repo at `/Users/tahir/Repos/kayten-server`. This is Go 1.22 + Postgres,
using gRPC (server + Connect-go fallback) + WebSocket envelope streams.

Your task: implement the server-side wipe-aware device revocation
behavior. Make stale prekeys unservable after wipe. Expose `revoked_at`
on `GetIdentityKeyResponse`. **CRITICAL NEW WORK:** implement the two
capability-token RPCs (`IssueSelfRevokeCapability` + `RevokeDeviceByCapability`)
that let a locally-wiped mobile device complete its own revocation without
live JWT auth — closes the post-wipe auth gap identified in master spec
§6a.3 review.

======================================================================
MANDATORY READING (in order)
======================================================================

1. `docs/superpowers/plans/2026-04-17-kayten-server-device-wipe-integration-spec.md`
   — authoritative scope. Read every section. Key:
     §0   — schema baseline (NO `signed_prekey` / `is_active` — use unified `prekeys`)
     §2.1 — RevokeDevice extensions
     §2.2 — IssueSelfRevokeCapability handler (NEW)
     §2.3 — RevokeDeviceByCapability handler (NEW, UNAUTHENTICATED)
     §2.4 — token rotation on login
     §4.1 — migration 014_revocation_capability_jti
     §4.2 — revocation_signing_key config
     §4.3 — rate-limit state
     §5   — metrics (incl. kayten_silent_wipe_detected_total)
     §6   — verification tests
     §7   — security considerations

2. `docs/superpowers/plans/2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`
   — parent spec. Focus on:
     §6a.3    — client outbox flow (what server must tolerate)
     §6a.3.1  — capability lifecycle (issue / rotate / consume / supersede)
     §6a.4    — RevokeDevice fan-out semantics
     §7 row "kayten-server" — full per-repo delta
     §9c      — capability RPC wire contract (copy into your handler)
     §10 #15  — security attacker model for capability leak

3. Existing server layout:
   - `internal/device/service.go:330-394` — existing `RevokeDevice` flow
     with `DEVICE_REVOKED` + `KEY_CHANGE_ALERT` fan-out. Preserve this.
   - `migrations/005_keys.up.sql` — unified `prekeys` table (is_one_time,
     used, used_at). NO `signed_prekey`. NO `is_active`.
   - `internal/auth/` — existing JWT signing infrastructure. Your new
     `revocation_signing_key` loads from the same secret-store class.
   - `api/proto/` — git submodule pinned to kayten-proto develop. Bump
     AFTER the kayten-proto PR merges (see Task 1).

======================================================================
STEP-BY-STEP TASK LIST
======================================================================

Task 1 — Branch + proto submodule bump
  Create `feat/2026-04-17-device-wipe-capability` off `develop`.
  Bump `api/proto` submodule to the SHA of the merged kayten-proto PR
  (feat/2026-04-17-hsm-keys-v1-constants). Regenerate Go stubs:
    make proto    # or whichever target the repo uses
  Commit the submodule bump + generated file diffs.

Task 2 — Migration `014_revocation_capability_jti`
  Create `migrations/014_revocation_capability_jti.up.sql`:

    CREATE TABLE revocation_capability_jti (
        jti            UUID PRIMARY KEY,
        device_id      TEXT        NOT NULL,
        issued_at      TIMESTAMPTZ NOT NULL,
        expires_at     TIMESTAMPTZ NOT NULL,
        used_at        TIMESTAMPTZ NULL,
        superseded_at  TIMESTAMPTZ NULL
    );

    CREATE UNIQUE INDEX uq_revocation_capability_active_per_device
      ON revocation_capability_jti (device_id)
      WHERE used_at IS NULL AND superseded_at IS NULL;

    CREATE INDEX idx_revocation_capability_unused
      ON revocation_capability_jti (jti)
      WHERE used_at IS NULL;

  Create `migrations/014_revocation_capability_jti.down.sql` with
  DROP INDEX + DROP TABLE.

Task 3 — Config: `revocation_signing_key`
  Load an HS256 secret from the existing server config secret-store
  using the same mechanism as the session JWT signing key. Support
  rotation:
    - KAYTEN_REVOCATION_SIGNING_KEY_CURRENT  (required, base64 32 bytes)
    - KAYTEN_REVOCATION_SIGNING_KEY_PREVIOUS (optional, grace window)

  Expose as `cfg.RevocationSigningKey` / `cfg.RevocationSigningKeyPrevious`.

Task 4 — Extend `DeviceService.RevokeDevice`
  In `internal/device/service.go`, augment the existing RevokeDevice
  flow (keeping DEVICE_REVOKED + KEY_CHANGE_ALERT fan-out intact):

    tx.Exec(`UPDATE prekeys SET used = TRUE, used_at = NOW()
             WHERE device_id = $1 AND NOT used`, deviceID)

    tx.Exec(`DELETE FROM inbound_message_queue     -- or repo's name
             WHERE recipient_device_id = $1`, deviceID)

    tx.Exec(`UPDATE revocation_capability_jti
             SET superseded_at = NOW()
             WHERE device_id = $1
               AND used_at IS NULL AND superseded_at IS NULL`, deviceID)

  Populate `GetIdentityKeyResponse.revoked_at` either by:
    (a) adding `identity_keys.revoked_at TIMESTAMPTZ` column (separate
        small migration) and setting on revoke, OR
    (b) JOIN-ing `devices.revoked_at` in the `GetIdentityKey` query
        until a column migration lands.
  Pick one; document choice in the PR body.

Task 5 — Handler: `IssueSelfRevokeCapability(ctx, req)` — AUTH-GATED
  In `internal/device/service.go` (or new `internal/device/capability.go`):

    1. Parse Bearer JWT via existing middleware; extract device_id.
    2. jti := uuid.NewV4()
    3. now := time.Now()
    4. claims := jwt.MapClaims{
         "iss": "kayten-server",
         "sub": deviceID,
         "aud": "self-revoke",
         "jti": jti.String(),
         "iat": now.Unix(),
         "exp": now.Add(30*24*time.Hour).Unix(),
       }
       token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).
         SignedString(cfg.RevocationSigningKey)
    5. BEGIN tx.
       tx.Exec(`UPDATE revocation_capability_jti
                SET superseded_at = NOW()
                WHERE device_id = $1 AND used_at IS NULL
                  AND superseded_at IS NULL AND jti != $2`, deviceID, jti)
       tx.Exec(`INSERT INTO revocation_capability_jti
                  (jti, device_id, issued_at, expires_at)
                VALUES ($1, $2, $3, $4)`,
                jti, deviceID, now, now.Add(30*24*time.Hour))
       COMMIT.
    6. Return { capability_jwt: token, expires_at: timestamppb.New(exp) }.
    7. Metric: kayten_revocation_capability_issued_total++.

Task 6 — Handler: `RevokeDeviceByCapability(ctx, req)` — UNAUTHENTICATED
  Register this RPC with NO auth middleware (i.e., NOT behind the
  Bearer-JWT interceptor). Flow:

    1. Rate-limit preliminary check: extract `sub` claim without sig-
       verify just to key the rate-limit bucket. ≤ 3 req / sub / 60 s.
       Over limit: return codes.ResourceExhausted.
    2. Parse + verify HS256 with cfg.RevocationSigningKey. On sig fail,
       retry with cfg.RevocationSigningKeyPrevious (if set). Fail both
       → codes.Unauthenticated.
    3. Verify claims.aud == "self-revoke"  → else codes.Unauthenticated.
    4. Verify claims.exp > now.Unix()     → else codes.Unauthenticated.
    5. Parse jti → UUID; if malformed → codes.InvalidArgument.
    6. BEGIN tx.
       row := tx.QueryRow(`
         UPDATE revocation_capability_jti
         SET used_at = NOW()
         WHERE jti = $1 AND used_at IS NULL AND expires_at > NOW()
         RETURNING device_id`, jti)
       Scan device_id:
         0 rows: COMMIT, return codes.FailedPrecondition (jti already used
                 OR not issued by us OR expired). Metric:
                 kayten_revocation_capability_consumed_total{result="already_used"}++.
         1 row: proceed to step 7.
    7. Call the same internal RevokeDevice flow as Task 4 (share the
       helper; do NOT duplicate fan-out code). Pass req.reason through.
       COMMIT.
    8. If req.reason == "silent_wipe_detected":
         Metric: kayten_silent_wipe_detected_total++.
    9. Metric: kayten_revocation_capability_consumed_total{result="ok"}++.
   10. Return empty response.

Task 7 — Token rotation on login
  In the auth service's `LOGIN_USER` success path (find it in
  `internal/auth/` — grep for where access/refresh tokens are issued),
  after creating the session tokens, also call the internal
  `IssueSelfRevokeCapability` helper for the logging-in device. Return
  the capability JWT to the client (either piggy-backed in a new field
  on the login response, or as a follow-up RPC call the client makes —
  decide based on what minimizes client round-trips; document in PR).

Task 8 — Metrics
  Register Prometheus collectors:
    kayten_revocation_capability_issued_total (counter)
    kayten_revocation_capability_consumed_total{result=<ok|already_used|expired|signature_invalid|rate_limited>} (counter)
    kayten_silent_wipe_detected_total (counter)
    kayten_device_wiped_total{reason=<pin_lockout_wipe|user_requested|silent_wipe_detected>} (counter, optional)

Task 9 — Tests
  In `internal/device/capability_test.go` (new file) + existing
  `internal/device/service_test.go`:
  - Revoke happy path: device registered → RevokeDevice → prekeys used,
    capability rows superseded, revoked_at set, KEY_CHANGE_ALERT emitted.
  - IssueSelfRevokeCapability: bearer-auth required; returns JWT parseable
    with configured signing key; jti row inserted; prior active row
    superseded.
  - RevokeDeviceByCapability happy: valid token → device revoked +
    prekeys used + queue cleared.
  - Idempotent replay: same token twice → 2nd returns FAILED_PRECONDITION;
    device state unchanged after 2nd call.
  - Signature rejection: flip a byte in payload → UNAUTHENTICATED.
  - Expired token: fast-forward clock → UNAUTHENTICATED.
  - Wrong audience: claims.aud = "access" → UNAUTHENTICATED.
  - Rate-limit: 4 calls in 60s → 4th is RESOURCE_EXHAUSTED.
  - Supersede: issue capability A → LOGIN issues B → calling with A now
    returns FAILED_PRECONDITION (row has superseded_at != NULL).
  - Silent wipe metric: call with reason="silent_wipe_detected" → counter
    bumped.
  - Signing key rotation: sign with PREVIOUS → validated during grace →
    after grace expired → UNAUTHENTICATED.

======================================================================
SELF-VERIFICATION BEFORE PR
======================================================================

  # Build.
  go build ./...
  # => exit 0

  # Lint.
  golangci-lint run
  # => zero issues

  # Migration applies cleanly on a fresh DB.
  make db-reset && make migrate-up
  # => migrations/014 present; table exists:
  psql -c "\d revocation_capability_jti"   # => columns match §4.1

  # Tests.
  go test ./internal/device/... ./internal/auth/...
  # => all pass

  # RPC registered at the right auth level.
  grep -A3 "RevokeDeviceByCapability" internal/grpc/server.go
  # => shows NO interceptor chain that injects Bearer auth (unauthenticated)

  # Status code export: server still maps 0xF5 from WebSocket envelopes
  # correctly if the app surfaces HSM throttle events (spec §2.1.7).
  # (No server action, just a grep sanity.)
  grep "INIT_PIN_THROTTLED" internal/  # ok whether 0 or 1+ hits

======================================================================
GUARDRAILS
======================================================================

- `RevokeDeviceByCapability` MUST NOT be behind the Bearer-auth
  interceptor. Register it on a separate unauth-allowed gRPC method list.
- Rate-limit MUST key off JWT `sub` (decoded without sig-verify for
  keying, sig-verified before consume). Per-IP rate-limit layered on top.
- `revocation_capability_jti.jti` UUID MUST be the primary key so
  duplicate-issuance is physically impossible.
- Log all `RevokeDeviceByCapability` consumptions with decoded `sub` for
  audit (security consideration §7 — detect key compromise).
- MUST NOT invent tables named `signed_prekey`, `one_time_prekeys`,
  or `013_pin_lockout_wipe`. Spec §0 is explicit.
- Tolerate idempotent duplicate `RevokeDevice` (live-JWT + capability
  outbox drain may both succeed; 2nd should be a no-op).

======================================================================
WHEN DONE, RETURN
======================================================================

- Branch name + head SHA.
- Submodule bump SHA for `api/proto`.
- Migration output (up + down apply + down reverts cleanly).
- `go test` summary.
- Metrics list actually registered.
- PR description noting:
  * Which option chosen for `GetIdentityKeyResponse.revoked_at` — column
    on identity_keys or JOIN from devices.
  * How login-path integration delivers the capability JWT to the client.
  * Grace-window duration for signing-key rotation.
- Risk list.
```
