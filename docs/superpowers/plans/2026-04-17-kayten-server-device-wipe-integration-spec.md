# kayten-server — device wipe integration spec

- Date: 2026-04-17
- Scope repo: `/Users/tahir/Repos/kayten-server`
- Parent: `2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`

## 0) Schema baseline (authoritative)

This repo stores keys in **`identity_keys`** (one row per `device_id`) and a unified **`prekeys`** table (`migrations/005_keys.up.sql`): `prekey_id`, `is_one_time`, `used`, `used_at`, etc. There are **no** separate `signed_prekey` / `one_time_prekeys` tables and **no** `is_active` column on `prekeys`. Any spec language elsewhere that mentions those names is **aspirational** — implementations must map to this schema.

## 1) Objective

Ensure the device revocation path fully reflects HSM wipe semantics and prevents stale key material from being served after wipe. **NEW in this iteration** (parent §9c / §6a.3 / §6a.3.1): add the two capability-token RPCs that let a locally-wiped device complete its own revocation without live JWT auth.

**Client coordination (parent §6a.3):** mobile apps **MUST** implement a durable pre-wipe `RevokeDevice` outbox (`kayten_wipe_revocation_outbox` / `WipeRevocationOutbox.plist`) so wipes are not lost offline. Apps store a server-issued `capability_jwt` in that outbox (fetched at enrollment + every successful login) and use it via `RevokeDeviceByCapability` post-wipe. `RevokeDevice` and `RevokeDeviceByCapability` MUST be **idempotent** (same `device_id`/`jti` replayed) so duplicate outbox drains are harmless.

## 2) Required backend behavior

### 2.1 Existing `DeviceService.RevokeDevice` extensions

Extend `DeviceService.RevokeDevice` (existing `DEVICE_REVOKED` + `KEY_CHANGE_ALERT` fan-out) to:

1. Mark the device revoked (`devices.revoked_at` — already present in typical flows).
2. **`prekeys`:** set `used = TRUE` (and `used_at = NOW()` where the codebase already sets it) for **every** row for that `device_id` — covers both long-lived (SPK-like) and one-time rows in one operation.
3. Clear the device's inbound undelivered message queue (recipient = wiped device), matching master §6a.4.
4. **`identity_keys` / API:** partners must be able to tell "revoked" from "missing": implement `GetIdentityKeyResponse.revoked_at` (§9a of parent) by adding `identity_keys.revoked_at` **or** populating the proto field from `devices.revoked_at` until a column migration lands — pick one and document it in the PR.
5. Keep existing notification fan-out (`DEVICE_REVOKED`, `KEY_CHANGE_ALERT`).
6. **Supersede any active `revocation_capability_jti` rows** for the device — `UPDATE revocation_capability_jti SET superseded_at = NOW() WHERE device_id = $1 AND used_at IS NULL AND superseded_at IS NULL`. Not strictly required (the device is already revoked), but keeps the capability table clean.

### 2.2 New `DeviceService.IssueSelfRevokeCapability` (auth-gated)

Parent §9c. Request is empty (`device_id` comes from auth context). Flow:

1. Parse Bearer JWT → resolve `device_id` from the access token.
2. Generate `jti = uuid_v4()`.
3. Compose compact JWT (HS256) with payload:
   ```json
   { "iss": "kayten-server", "sub": "<device_id>",
     "aud": "self-revoke", "jti": "<uuid>",
     "iat": <now_unix>, "exp": <now_unix + 30*86400> }
   ```
   Signed with `revocation_signing_key` (loaded from existing server config secret store; rotatable).
4. Insert `revocation_capability_jti(jti, device_id, issued_at=NOW(), expires_at=NOW()+30d, used_at=NULL, superseded_at=NULL)`.
5. Supersede prior active rows for the same device: `UPDATE revocation_capability_jti SET superseded_at = NOW() WHERE device_id = $1 AND used_at IS NULL AND superseded_at IS NULL AND jti != $2`.
6. Respond `{capability_jwt, expires_at}`.

### 2.3 New `DeviceService.RevokeDeviceByCapability` (UNAUTHENTICATED)

Parent §9c. **No Bearer required** — validated entirely via JWT signature + single-use `jti`. Flow:

1. Verify HS256 signature against `revocation_signing_key` (and any rotated predecessor still within grace window). Fail → `UNAUTHENTICATED`.
2. Verify `aud == "self-revoke"`. Fail → `UNAUTHENTICATED`.
3. Verify `exp > now`. Fail → `UNAUTHENTICATED`.
4. Verify `jti` is a UUID-v4-shape. Fail → `INVALID_ARGUMENT`.
5. Rate-limit check: ≤ 3 requests per `sub` / 60 s window. Fail → `RESOURCE_EXHAUSTED`.
6. Atomic `UPDATE revocation_capability_jti SET used_at = NOW() WHERE jti = $1 AND used_at IS NULL AND expires_at > NOW() RETURNING device_id`.
   - 1 row returned: proceed.
   - 0 rows returned: `jti` already used OR not issued by us OR expired → `FAILED_PRECONDITION` (idempotent — app treats as success).
7. On 1-row success, run the **same internal RevokeDevice flow** as §2.1 (steps 1-5). `reason` field is passed through from the RPC request.
8. Respond empty.

### 2.4 Token rotation on login

The auth service's `LOGIN_USER` handler (or the equivalent successful-auth code path) MUST, after issuing the new session tokens, also invoke the `IssueSelfRevokeCapability` internal flow for the logging-in device (or expose a convenience wrapper). Prior active jtis for that device are superseded automatically by step 2.2.5. Clients treat the returned capability as replacing whatever they had stored in the outbox.

### 2.5 Prekey + bundle paths — revoked-device refusal

`GetPrekey` / bundle paths must refuse revoked devices (`NOT_FOUND` or equivalent); verify against current `prekeys` + `devices.revoked_at` logic. Unchanged from prior spec iteration.

## 3) API / proto behavior

- Additive field on `GetIdentityKeyResponse`: `google.protobuf.Timestamp revoked_at` (unset = active).
- **New RPCs on `DeviceService`** (parent §9c, proto spec §3.5.1):
  - `IssueSelfRevokeCapability(IssueSelfRevokeCapabilityRequest) returns (IssueSelfRevokeCapabilityResponse)`
  - `RevokeDeviceByCapability(RevokeDeviceByCapabilityRequest) returns (RevokeDeviceByCapabilityResponse)`
- Clients treat set `revoked_at` as revoked identity and require re-verification per product rules.

## 4) DB and performance

- Prefer a single `UPDATE prekeys SET used = TRUE, used_at = NOW() WHERE device_id = $1 AND NOT used` (or equivalent) — measure on large bundles; add/extend indexes only if profiling shows need (partial indexes on `(device_id)` / `NOT used` may already exist).

### 4.1 New migration — `014_revocation_capability_jti`

Create `kayten-server/migrations/014_revocation_capability_jti.up.sql`:

```sql
CREATE TABLE revocation_capability_jti (
    jti            UUID PRIMARY KEY,
    device_id      TEXT        NOT NULL,
    issued_at      TIMESTAMPTZ NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL,
    used_at        TIMESTAMPTZ NULL,
    superseded_at  TIMESTAMPTZ NULL
);

-- At most one active capability per device. Used + superseded rows stay
-- for audit history.
CREATE UNIQUE INDEX uq_revocation_capability_active_per_device
  ON revocation_capability_jti (device_id)
  WHERE used_at IS NULL AND superseded_at IS NULL;

-- Fast jti lookup for RevokeDeviceByCapability validation.
CREATE INDEX idx_revocation_capability_unused
  ON revocation_capability_jti (jti)
  WHERE used_at IS NULL;

-- Periodic cleanup candidate: rows where expires_at < NOW() - 90d can be
-- pruned; keep this simple (no built-in TTL).
```

Matching `014_revocation_capability_jti.down.sql`:

```sql
DROP INDEX IF EXISTS idx_revocation_capability_unused;
DROP INDEX IF EXISTS uq_revocation_capability_active_per_device;
DROP TABLE IF EXISTS revocation_capability_jti;
```

### 4.2 New config — `revocation_signing_key`

Load from existing server config secret store (same mechanism as the session JWT signing key). Support rotation: `revocation_signing_key_current` + `revocation_signing_key_previous` (one-week grace window). HS256 (symmetric) is sufficient — both sign and verify happen on the server.

Config keys (example, match repo convention):
- `KAYTEN_REVOCATION_SIGNING_KEY_CURRENT` — 32 random bytes, base64-encoded.
- `KAYTEN_REVOCATION_SIGNING_KEY_PREVIOUS` — optional; only present during a rotation window.

### 4.3 Rate-limit state

Use the existing rate-limit middleware (redis-backed counter keyed by JWT `sub`, 60-second sliding window, limit 3). If no existing middleware, add a small in-memory bucket map — acceptable since `RevokeDeviceByCapability` is low-volume (≈ 1 call per device-wipe event across the fleet).

## 5) Metrics and observability

- Existing: `kayten_device_wiped_total{reason}` (optional, parent §7).
- NEW: `kayten_revocation_capability_issued_total` — counter per `IssueSelfRevokeCapability` success.
- NEW: `kayten_revocation_capability_consumed_total{result}` — `result` ∈ {`ok`, `already_used`, `expired`, `signature_invalid`, `rate_limited`}.
- NEW: `kayten_silent_wipe_detected_total` — set on `RevokeDeviceByCapability` call with `reason = "silent_wipe_detected"` (parent §6a.3 step 0), so ops can alert on clusters of silent wipes.

## 6) Verification

- Integration / repository tests: after revoke, `GetPrekey` (or bundle path) does not return usable unused prekeys for that device.
- Tests: `GetIdentityKey` returns populated `revoked_at` when device is revoked.
- Tests: undelivered queue cleanup for wiped device (if applicable table exists in this repo).
- **NEW — capability token happy path:** enrollment → `IssueSelfRevokeCapability` → store JWT → simulate wipe → call `RevokeDeviceByCapability(jwt, reason)` unauth → assert device revoked + prekeys used + KEY_CHANGE_ALERT fan-out.
- **NEW — idempotent replay:** call `RevokeDeviceByCapability` twice with same JWT → first OK, second `FAILED_PRECONDITION`.
- **NEW — signature rejection:** tamper one byte of `capability_jwt` → `UNAUTHENTICATED`.
- **NEW — expired token:** travel-time to `exp + 1s` → `UNAUTHENTICATED`.
- **NEW — rate-limit:** 4 calls in 60 s for the same `sub` → 4th gets `RESOURCE_EXHAUSTED`.
- **NEW — supersede on login:** issue capability A → LOGIN (which issues B) → assert A's row has `superseded_at != NULL`; calling `RevokeDeviceByCapability(A)` returns `FAILED_PRECONDITION`.
- **NEW — silent wipe detection flow (parent §6a.3 step 0):** client sends `RevokeDeviceByCapability` with `reason = "silent_wipe_detected"` → server revokes normally + bumps `kayten_silent_wipe_detected_total` metric.
- **NEW — revocation_signing_key rotation:** sign JWT with previous key, verify with current-or-previous → accepted during grace window; after window expires → `UNAUTHENTICATED`.

## 7) Security considerations

- `capability_jwt` leak → attacker can revoke the legitimate device once. DoS, not key compromise. Revocation itself triggers `KEY_CHANGE_ALERT` fan-out, forcing partner re-verification; the attacker gains nothing beyond denial-of-service. Single-use `jti` + 30d expiry + rotation-on-login bounds blast radius. Rate-limit prevents batch DoS across multiple stolen tokens.
- `revocation_signing_key` compromise → attacker can forge unlimited `capability_jwt`s for any `device_id` they know, revoking arbitrary devices. Mitigation: (1) store in same secret-store class as session JWT signing key; (2) rotate regularly (mechanism built in); (3) audit-log all `RevokeDeviceByCapability` consumptions with the decoded `sub` for post-hoc detection.
- Unauthenticated RPC endpoint surface increase: one new endpoint, IP rate-limited + per-`sub` rate-limited. Acceptable given the bounded blast radius.
