# kayten-app — PKCS#11 retirement spec

- Date: 2026-04-17
- Scope repo: `/Users/tahir/Repos/kayten-app`
- Parent: `2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`

## 0) Baseline touchpoints (non-exhaustive)

Implementers should start from the current PKCS#11-shaped call graph, including:

- `android/app/src/main/kotlin/com/kayten/uhsm/models/HsmCommand.kt` (`HsmCommand.Legacy` subtree to delete)
- `android/app/src/main/kotlin/com/kayten/uhsm/hsm/CryptoOperations.kt`, `SecureChannel.kt`, `HsmService.kt` (as wired today)
- `android/app/src/main/kotlin/com/kayten/uhsm/hsm/Pkcs11ObjectResolver.kt`, `Pkcs11ObjectRegistry.kt` (to delete)
- `lib/core/constants/crypto_constants.dart` — `kSlot*` constants migrating to new bands
- `lib/services/conversation_slot_manager.dart` — LRU range moving 13..30 → 130..191 (or subset)
- `lib/features/hsm_workbench/`, `lib/features/auth/providers/auth_provider.dart` (secure channel / setup)
- `lib/data/remote/api/device_api_client.dart:69` — existing `RevokeDevice` call site; new RPCs plug in here
- `lib/data/local/secure/secure_token_store.dart` — **NOT** the storage site for the capability token (it lives outside Keychain/Keystore)

**Tests to migrate** when `Legacy` disappears: `android/app/src/test/kotlin/com/kayten/uhsm/hsm/SecureChannelTest.kt`, `android/app/src/test/kotlin/com/kayten/uhsm/transport/HsmCommandTest.kt`, `HsmCommandProvisionTest.kt`, and Dart tests under `test/` that assert legacy command bytes.

## 1) Objective

Remove legacy PKCS#11 command usage from app setup/messaging paths and migrate to semantic command model with `crypto_key_id` semantics. Close the post-wipe authentication gap via the new capability-token flow.

## 2) Mandatory code migration

1. Delete `HsmCommand.Legacy` usage and call-sites.
2. Remove `Pkcs11ObjectResolver` and object-handle translation paths.
3. Replace logical slot constants (`kSlot*`) in messaging/HSM flows with `crypto_key_id` model.
4. Route key operations through semantic commands:
   - read public key via `0x61` / retained identity path `0x47`
   - key generation/rotation via `0x60`/`0x63` (respect parent §5.5 **current + previous** SPK semantics when caching pubs)
   - random via `0x64`
5. **Hardware production PIN-command rules (parent §3.1 / §3.1.1 / §4):**
   - **`LOGIN_USER_V1 (0x45)` MUST be sent only as inner command of `SECURE_EXECUTE_V1 (0x55)`** once `SPI_CAP_MOBILE_PROFILE_V1` is on. Dev/staging MAY keep bare-outer for bench.
   - `CHANGE_USER_PIN_V1 (0x46)` — same rule (unchanged).
   - `PROVISION_DEVICE_V1 (0x44)` — remains plaintext outer (see parent §10 #14 / §3.1.1).
6. **Renumber slot constants to new bands (parent §9b).** Edit `lib/core/constants/crypto_constants.dart`:
   - `kSlotSecureChannelStart..End` 9..12 → 110..113 (inside 110..117 band)
   - `kSlotMessagingStart..End` 13..30 → 130..159 (inside 130..191 band; 15 conversations × 2 slots; expand later if needed)
   - `kSlotVoiceEpochInner/Outer` 42..43 → 118..119 (inside 118..129 band)
   - Adjust `ConversationSlotManager.allocateSlot()` / `destroySlot()` range checks accordingly.
   - Wire format for `0x40`/`0x41`/`0x42` unchanged — 1-byte `convKeyInner`/`convKeyOuter` fields still fit the new numeric values.

## 3) Provisioning and wipe UX

Implement app UX/flow for:
- provisioning required (`init_pin` + `user_pin`)
- PIN retries visible to user
- **Init-pin throttle (parent §5.1.1, status `0xF5`):** when `PROVISION_DEVICE_V1` returns `INIT_PIN_THROTTLED`, show "Too many failed attempts — try again in N seconds" UX; disable the submit button for that duration (parse delay hint from future firmware response or mirror the table client-side).
- device wiped state handling (from lockout or user wipe)
- **Silent-wipe detection on cold start (parent §6a.3 step 0, normative):** on every app launch, before any HSM crypto operation beyond `0x43`/`0x49`/`0x66`, probe `GET_RUNTIME_STATUS_V1`. If `provisioning_state == PROVISIONING_REQUIRED` AND `local_state.registered_device_id != null`, treat as silent wipe → run step 1 of §6a.3 with `reason = "silent_wipe_detected"`, `last_wipe_reason = runtime.last_wipe_reason`.
- post-wipe recovery per parent **§6a.3 ordering:** persist **`kayten_wipe_revocation_outbox`** (Android) / **`Application Support/Kayten/WipeRevocationOutbox.plist`** (iOS) **before** erasing Keystore/Keychain + wrapped-key DB; drain with `WorkManager` / `BGAppRefreshTask` via `RevokeDeviceByCapability` (unauthenticated, uses stored `capability_jwt`); then local wipe; then UX to re-provision. **Do NOT delete the outbox file during local wipe.**
- **User-initiated wipe (settings):** `0x68 REQUEST_WIPE_CHALLENGE_V1` → show confirmation UI → `0x65` inside `0x55` (hardware prod) with `user_pin` + `wipe_nonce`; handle `0xEF WIPE_NONCE_INVALID` by re-issuing `0x68`

## 3.1) Capability-token lifecycle (parent §6a.3.1 — NEW)

Implement in `lib/data/remote/api/device_api_client.dart` + a new `lib/data/local/wipe_revocation_outbox.dart`:

1. **At enrollment success** — call `DeviceService.IssueSelfRevokeCapability()` (authed); persist returned `{capability_jwt, expires_at}` to the native outbox companion file via platform channel. **Never** write this JWT to `flutter_secure_storage` (it must survive the local wipe).
2. **On every successful `LOGIN_USER_V1`** — repeat the call; overwrite the stored JWT with the fresh one.
3. **On wipe trigger (`DEVICE_WIPED` response OR silent-wipe detection):**
   a. Write outbox entry `{device_id, capability_jwt (copy from store), reason, silent_flag, enqueued_at}` into the durable outbox file.
   b. Attempt `DeviceService.RevokeDevice(device_id, reason)` over the live JWT with exponential backoff up to **60 seconds** (not 5 minutes). On success, clear the outbox entry.
   c. Proceed to local wipe unconditionally after step 3b (whether or not step 3b succeeded).
4. **Post-wipe drain (WorkManager / BGAppRefreshTask):** iterate outbox entries → for each, call `DeviceService.RevokeDeviceByCapability(capability_jwt, reason)` **unauthenticated** (no Bearer header). Response handling:
   - `OK` → remove entry from outbox.
   - `FAILED_PRECONDITION` (jti already used) → remove entry (idempotent success).
   - `UNAUTHENTICATED` → remove entry (token invalid / expired — nothing we can do; log and give up).
   - `RESOURCE_EXHAUSTED` → retry with backoff (likely rate-limited due to rapid retries).
   - Transport error → retry with exponential backoff.

### 3.1.1 Native outbox storage

- **Android:** `EncryptedSharedPreferences` with file name `kayten_wipe_revocation_outbox` in app-private `no_backup` storage. Key derivation: uses AndroidX `MasterKey.DEFAULT_MASTER_KEY_ALIAS` (NOT wiped by our local-wipe flow because it's managed by the system Keystore aliased under AndroidX's root, not by our `com.kayten.*` Keystore namespace). Layout: JSON blob under key `outbox` + JSON blob under key `capability_token`.
- **iOS:** plist file at `Application Support/Kayten/WipeRevocationOutbox.plist` with `NSFileProtectionCompleteUnlessOpen`. Excluded from iCloud backup via `URLResourceKey.isExcludedFromBackupKey = true`. Keys: `outbox`, `capabilityToken`.
- **Dart surface:** new `WipeRevocationOutbox` class in `lib/data/local/wipe_revocation_outbox.dart` — abstract methods `writeCapabilityToken`, `readCapabilityToken`, `enqueueEntry`, `listEntries`, `removeEntry`. Backed by platform channel calls on Android/iOS; backed by in-memory stub in `HSM_MODE=software` / demo.

## 4) Data model migration

Move wrapped-key metadata to schema-v2 with `crypto_key_id` references.
On first migration, enforce re-enrollment to eliminate stale PKCS#11 handle assumptions. The schema-v2 migration is the natural place to re-seat slot constants into the new bands (§2 step 6) — existing rows carry the old 13..30 / 42..43 numbering which is incompatible; re-enrollment generates fresh keys at the new band-compliant ids.

## 5) Error handling

Handle new statuses:
- command unavailable (`0xE0`)
- key-id not readable (`0xE1`) / rotate-only (`0xE2`) / stale (`0xE3`)
- session not ready (`0xED`)
- already provisioned (`0xE4`)
- init-pin invalid (`0xE5`)
- device wiped (`0xEE`)
- wipe nonce invalid (`0xEF`)
- **init-pin throttled (`0xF5`)** — NEW (parent §5.1.1). Surface as `HsmStatusException.initPinThrottled`; UI shows "Too many failed attempts — try again in N seconds" (derive N from fail-count table if firmware doesn't carry it in response).

## 6) Validation

- Setup flow should not emit legacy command `0x33`.
- HSM setup success on current firmware using semantic path.
- Lockout wipe sequence works end-to-end with server revocation trigger (both live-JWT happy path AND post-wipe capability-token drain path).
- **NEW — silent-wipe detection:** power off during a PIN-lockout wipe, reboot app, assert the cold-start probe detects `PROVISIONING_REQUIRED + last_wipe_reason=PIN_LOCKOUT` and runs the revocation-outbox flow.
- **NEW — capability token persistence:** enroll → confirm `capability_jwt` in outbox storage; trigger local wipe → confirm JWT survives; run drain → confirm `RevokeDeviceByCapability` succeeds with `OK` or `FAILED_PRECONDITION`.
- **NEW — `LOGIN_USER_V1` via `0x55`:** in hardware-prod build, bare `0x45` is rejected by the host/HSM; the app routes through `CryptoOperations.executeViaSecureEnvelope` equivalent for `0x45`.
- **NEW — slot band renumbering:** messaging encrypt/decrypt uses `convKeyInner` in the 130..191 range; voice ephemeral allocated at 118..119; secure-channel session keys at 110..113. `flutter test` + Kotlin unit tests pass.

## 7) Out of scope

- iOS MFi HSM hardware path (separate workstream, tracked in CLAUDE.md).
- Two-phase provisioning (parent §13 follow-up — closes master §10 #14).
- Rust/Swift translator reference impls (proto-spec follow-up when kayten-app-v2 regenerates bindings).
