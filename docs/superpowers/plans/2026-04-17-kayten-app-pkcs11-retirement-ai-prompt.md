# kayten-app PKCS#11 retirement + capability-token + silent-wipe detection — AI prompt

- **Date:** 2026-04-17
- **Repo scope:** `/Users/tahir/Repos/kayten-app`
- **Authoritative repo spec:** [`2026-04-17-kayten-app-pkcs11-retirement-spec.md`](2026-04-17-kayten-app-pkcs11-retirement-spec.md)
- **Parent spec:** [`2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`](2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md)
- **Upstream dependencies (merge order):**
  1. `2026-04-17-kayten-proto-hsm-keys-constants-spec.md` — publishes `CryptoKeyId` (with new bands), `IssueSelfRevokeCapability`, `RevokeDeviceByCapability` RPCs, `revoked_at`
  2. `2026-04-17-kayten-server-device-wipe-integration-spec.md` — implements the two capability RPCs
  3. `2026-04-17-uhsm-hsm-mobile-profile-v2-spec.md` — provides the firmware-side semantic command surface the app talks to
  4. `2026-04-17-uhsm-host-kmobile-manager-spec.md` — provides the host-side dispatcher

---

```text
You are Claude (Opus or Sonnet) operating inside the `kayten-app` repo
at `/Users/tahir/Repos/kayten-app`. This is a Flutter/Dart app with a
Kotlin native plugin for HSM access via FT4222H USB-SPI bridge.

Your task: remove legacy PKCS#11 command usage from app setup/messaging
paths; migrate to semantic command model with `crypto_key_id` semantics
(using proto-generated constants); renumber internal slot constants to
the new bands (parent §9b — no wire change, values still fit in 1 byte);
implement capability-token self-revoke lifecycle (parent §6a.3 + §6a.3.1);
implement silent-wipe detection on cold start (parent §6a.3 step 0);
enforce `LOGIN_USER_V1 (0x45)` via `0x55` inner-dispatch in hardware
production.

This is the single biggest per-repo workstream in the fan-out. Expect
~2 weeks of careful, well-tested work.

======================================================================
MANDATORY READING (in order)
======================================================================

1. `docs/superpowers/plans/2026-04-17-kayten-app-pkcs11-retirement-spec.md`
   — authoritative scope. Read every section. Pay attention to:
     §0      — baseline touchpoints
     §2.5    — hardware-prod PIN rules (0x45 NEW + 0x46 unchanged + 0x44 plain)
     §2.6    — slot constant renumbering into new bands (parent §9b)
     §3      — provisioning UX + throttle (0xF5) + silent-wipe step 0
     §3.1    — capability-token lifecycle (NEW)
     §3.1.1  — native outbox storage specifics (Android / iOS / Dart)
     §4      — schema-v2 + forced re-enrollment
     §5      — new status codes (incl. 0xF5)
     §6      — validation tests

2. `docs/superpowers/plans/2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`
   — parent spec. Focus on:
     §3.1 + §3.1.1 — why 0x45 goes via 0x55 but 0x44 doesn't
     §6   — state machine
     §6a  — lockout + wipe procedure
     §6a.3 — app-side response (step 0, capability flow, ordering)
     §6a.3.1 — capability lifecycle contract
     §9b — new slot bands (pay attention to which app slots migrate where)
     §9c — capability RPC proto contract
     §10 — security considerations #14 (plaintext 0x44) + #15 (capability leak model)

3. Existing kayten-app layout:
   - `android/app/src/main/kotlin/com/kayten/uhsm/models/HsmCommand.kt`
     — contains `HsmCommand.Legacy` sealed subtree to DELETE.
   - `android/app/src/main/kotlin/com/kayten/uhsm/hsm/Pkcs11ObjectResolver.kt`,
     `Pkcs11ObjectRegistry.kt` — DELETE.
   - `android/app/src/main/kotlin/com/kayten/uhsm/hsm/SecureChannel.kt`
     (lines 562-620 = identity-key-signed transcript — do NOT weaken)
   - `android/app/src/main/kotlin/com/kayten/uhsm/hsm/CryptoOperations.kt`
     — contains `executeViaSecureEnvelope` (0x55 wrapper you route 0x45 through).
   - `lib/core/constants/crypto_constants.dart` — contains `kSlot*`
     constants: `kSlotSecureChannelStart..End` 9..12,
     `kSlotMessagingStart..End` 13..30 (maxSlots=9),
     `kSlotVoiceEpochInner/Outer` 42..43. ALL MIGRATE.
   - `lib/services/conversation_slot_manager.dart` — LRU over 13..30 today.
   - `lib/data/remote/api/device_api_client.dart` — `revokeDevice` at line 69.
   - `lib/data/local/secure/secure_token_store.dart` — KEYCHAIN/KEYSTORE
     backed; the capability_jwt is NOT stored here.
   - `lib/data/remote/api/grpc_client.dart` lines 88-103 — `authenticated`
     vs `unauthenticated` CallOptions; `RevokeDeviceByCapability` uses
     unauthenticated.
   - `lib/data/remote/api/ws_client.dart` — existing in-memory offline
     queue (NOT the durable outbox — that's new per this spec).
   - Proto submodule `api/proto` — bump AFTER the kayten-proto PR merges.

======================================================================
STEP-BY-STEP TASK LIST
======================================================================

Task 1 — Branch + proto bump + regen
  Create `feat/2026-04-17-pkcs11-retirement` off `develop`. Bump
  `api/proto` submodule to the merged-kayten-proto SHA. Regenerate Dart
  stubs; commit the submodule bump + generated diffs in an early commit
  on the branch so subsequent commits see the new symbols.

Task 2 — Delete `HsmCommand.Legacy`
  In `android/app/src/main/kotlin/com/kayten/uhsm/models/HsmCommand.kt`,
  delete the entire `HsmCommand.Legacy` sealed subclass tree. Update all
  call sites that construct `Legacy.*` variants — either delete the path
  (if PKCS#11-only) or migrate to `HsmCommand.Composite.*` / semantic
  equivalents.

Task 3 — Delete `Pkcs11ObjectResolver` + `Pkcs11ObjectRegistry`
  Delete these files. Remove imports from `HsmPlugin.kt`, `HsmService.kt`,
  `SecureChannel.kt`. Delete `CKA_*`, `CKO_*`, `CKK_*`, `CK_SESSION_HANDLE`,
  `CK_OBJECT_HANDLE` constants from `HsmConstants.kt`.

Task 4 — Renumber slot constants (parent §9b)
  Edit `lib/core/constants/crypto_constants.dart`:
    // OLD → NEW
    kSlotSecureChannelStart = 9  → 110   // inside new band 110..117
    kSlotSecureChannelEnd   = 12 → 113
    kSlotMessagingStart     = 13 → 130   // inside new band 130..191
    kSlotMessagingEnd       = 30 → 159   // 15 convos × 2 slots; can expand to 191 later
    kSlotVoiceEpochInner    = 42 → 118   // inside new band 118..129
    kSlotVoiceEpochOuter    = 43 → 119
  Update `ConversationSlotManager.allocateSlot`/`destroySlot` range
  checks accordingly. Import proto-generated `CryptoKeyId` constants for
  the band boundaries; ensure runtime assertions use
  `CRYPTO_KEY_ID_CONVERSATION_FIRST/LAST` etc.
  Mirror the numeric updates in any Kotlin constants that reference the
  same slots.
  Wire format on `0x40`/`0x41`/`0x42` stays 1-byte — values fit 0..255.

Task 5 — Semantic command call-site migration
  Rewrite these call sites to use semantic commands + crypto_key_id:
  - Identity pub read: prefer `0x61 READ_PUBLIC_KEY_V1 { crypto_key_id = 23 }`
    over legacy. Retained `0x47` for fresh-boot bring-up only.
  - SPK generation: `0x60 GENERATE_MESSAGING_KEY_V1 { kind = SPK }` —
    returns `(priv_id, pub_id, pub, spk_signature)` atomic.
  - Ephemeral ECDH: `0x60 GENERATE_MESSAGING_KEY_V1 { kind = EPHEMERAL_ECDH }`
    — returns firmware-assigned id in the 192..239 band.
  - SPK rotation: `0x63 ROTATE_SPK_V1`.
  - OPK generation: `0x5C GENERATE_OPK_V1` (existing OPK/DH4 workstream).
  - Random bytes: `0x64 GENERATE_RANDOM_V1` (bounded ≤ 512B).
  - Delete OPK / ephemeral: `0x62 DELETE_MESSAGING_KEY_V1`.

  Update `CryptoOperations.kt`, `KeyManagement.kt`, `SecureChannel.kt`,
  `VoiceModeCrypto.kt`, `MessageKeyWrapper.kt` to route through these.

Task 6 — Hardware-prod `LOGIN_USER_V1` via `0x55` inner dispatch
  In `android/.../HsmPlugin.kt` (or equivalent for 0x45 construction):
  when capability bit `SPI_CAP_MOBILE_PROFILE_V1` is set AND build is
  hardware-prod, route `0x45 LOGIN_USER_V1` construction through
  `CryptoOperations.executeViaSecureEnvelope` (the same helper that
  wraps 0x46). Dev/staging keep bare-outer fast path (same `#if` /
  flavor guard already used for 0x46).

  `0x44 PROVISION_DEVICE_V1` remains plaintext-outer — do NOT wrap it.
  Add a comment at the 0x44 construction site citing parent §10 #14 and
  §3.1.1 so future reviewers don't "fix" it.

Task 7 — Provisioning UX + throttle + PIN retry countdown
  In `lib/features/auth/`:
  - New screen: `ProvisioningScreen` — init-pin (8 digits) + user-pin
    (4..16 digits) form with strength feedback.
  - Post-wipe variant: same screen + red banner keyed off
    `runtime.last_wipe_reason` (see parent §6a.3 step 3 for banner copy
    per reason: `PIN_LOCKOUT`, `USER_REQUEST`, `PIN_LOCKOUT_RECOVERY`).
  - PIN retry countdown: on `0x45` `AUTH_FAILED`, show
    `"N attempts remaining"` from response body.
  - Throttle UX: on `0x44` `INIT_PIN_THROTTLED (0xF5)`, show
    "Too many failed init-PIN attempts. Try again in N seconds."
    (Derive N client-side from a mirrored throttle table matching parent
     §5.1.1 until firmware response includes the delay hint.)

Task 8 — Silent-wipe detection on cold start (parent §6a.3 step 0)
  In `lib/core/boot/boot_coordinator.dart` (or where app startup runs
  HSM probing), add a new step that runs BEFORE any other HSM crypto
  operation:
    runtime := hsmService.getRuntimeStatusV1()
    if runtime.provisioningState == PROVISIONING_REQUIRED
       && localState.registeredDeviceId != null:
       // silent wipe detected — either DEVICE_WIPED response was lost
       // or power-loss wipe finished while app was off.
       wipeReason := runtime.lastWipeReason
       await _performSilentWipeRecovery(
         reason: "silent_wipe_detected",
         lastWipeReason: wipeReason);
       navigateToProvisioning(wipeReason: wipeReason);
       return

  `_performSilentWipeRecovery` calls the same wipe-handler entrypoint
  as the DEVICE_WIPED-response path (Task 10), just with a different
  reason string for server-side metrics.

Task 9 — Capability-token lifecycle (parent §6a.3.1)
  Create `lib/data/local/wipe_revocation_outbox.dart`:
    abstract class WipeRevocationOutbox {
      Future<void> writeCapabilityToken({required String jwt, required DateTime expiresAt});
      Future<({String jwt, DateTime expiresAt})?> readCapabilityToken();
      Future<void> clearCapabilityToken();
      Future<void> enqueueEntry({required String deviceId, required String reason, required bool silent});
      Future<List<OutboxEntry>> listEntries();
      Future<void> removeEntry(OutboxEntry entry);
    }
  Native backends:
    - Android: EncryptedSharedPreferences `kayten_wipe_revocation_outbox`
      in app-private `no_backup` storage. Master key alias:
      `MasterKey.DEFAULT_MASTER_KEY_ALIAS` (AndroidX-managed, NOT our
      `com.kayten.*` namespace — survives our local wipe).
    - iOS: plist file at
      `Application Support/Kayten/WipeRevocationOutbox.plist` with
      `NSFileProtectionCompleteUnlessOpen` and
      `isExcludedFromBackupKey = true`.
    - Dart software/demo mode: in-memory stub (capability drain has no
      real server to hit in demo).

  Wire to enrollment + login:
    - After `enrollmentManager.completeEnrollment()` success → call
      `deviceApiClient.issueSelfRevokeCapability()` (authed gRPC) →
      `outbox.writeCapabilityToken(...)`.
    - In `AuthNotifier.login` on success → same call.

Task 10 — Wipe handler + outbox drain (parent §6a.3 steps 1-3)
  Refactor existing wipe-response path to:
    1. Read current capability_jwt from outbox.
    2. Append outbox entry `{device_id, capability_jwt, reason,
       silent_flag, enqueued_at}` — write to disk via platform channel.
    3. Attempt `DeviceService.RevokeDevice(device_id, reason)` with live
       JWT, exponential backoff up to 60 seconds. On success, remove
       the outbox entry.
    4. Proceed to local wipe unconditionally:
       - Delete all `com.kayten.*` Keystore (Android) / Keychain (iOS) entries.
       - Drop the wrapped-key database (schema-v2 per Task 11).
       - Clear conversation / voice / secure-channel session caches.
       - DO NOT delete the outbox file.
    5. Navigate to provisioning with wipe_reason banner.

  Add `lib/services/wipe_revocation_drainer.dart`:
    - Scheduled via WorkManager on Android / BGAppRefreshTask on iOS.
    - On each run, iterate `outbox.listEntries()`; for each, call
      `deviceApiClient.revokeDeviceByCapability(jwt, reason)` with
      unauthenticated CallOptions.
    - Status mapping:
        OK                    → remove entry
        FAILED_PRECONDITION   → remove entry (idempotent)
        UNAUTHENTICATED       → remove entry (token invalid/expired)
        RESOURCE_EXHAUSTED    → retry with backoff
        transport error       → retry with backoff

Task 11 — Schema-v2 forced re-enrollment
  Create a new Drift schema version 2 that replaces wrapped-key columns
  with `crypto_key_id: Int`-keyed references. On first launch after
  upgrade, detect schema-v1, wipe the database, enforce full re-enrollment.
  No mixed v1/v2 live rows — that's a non-goal per parent §10.4.

Task 12 — New status-code error handling
  Extend `HsmStatusException` / `HsmError` enum with:
    - `commandUnavailable` (0xE0)
    - `keyIdNotReadable` (0xE1)
    - `keyIdRotateOnly` (0xE2)
    - `keyIdStale` (0xE3)
    - `sessionNotReady` (0xED)
    - `deviceWiped` (0xEE)
    - `alreadyProvisioned` (0xE4)
    - `initPinInvalid` (0xE5)
    - `wipeNonceInvalid` (0xEF)
    - `initPinThrottled` (0xF5)  // NEW
  Localize error strings in `lib/l10n/app_{de,en,tr}.arb`.

======================================================================
SELF-VERIFICATION BEFORE PR
======================================================================

  # Proto bump committed.
  git log --oneline | head -1
  # => mentions submodule bump in an early commit

  # Legacy symbols gone.
  rg "HsmCommand\.Legacy" android/ lib/
  # => 0 hits
  rg "Pkcs11ObjectResolver|Pkcs11ObjectRegistry" android/ lib/
  # => 0 hits
  rg "CKA_|CKO_|CKK_|CK_SESSION_HANDLE|CK_OBJECT_HANDLE" android/
  # => 0 hits
  rg "kSlotIdentityEd25519|kSlotSignedPrekey|kSlotOpkStart" lib/
  # => 0 hits

  # Slot constants renumbered — Dart side.
  rg "kSlotSecureChannelStart\s*=\s*110" lib/core/constants/crypto_constants.dart
  rg "kSlotMessagingStart\s*=\s*130" lib/core/constants/crypto_constants.dart
  rg "kSlotVoiceEpochInner\s*=\s*118" lib/core/constants/crypto_constants.dart
  # => each 1 hit

  # Slot constants renumbered — Kotlin mirror (Task 4 step 7: "Mirror the
  # numeric updates in any Kotlin constants that reference the same slots").
  # Guards against the Dart-only-renumber bug where wire still sends old values
  # via HsmPlugin.kt / HsmConstants.kt paths (identity read, voice setup).
  rg "\b(9|10|11|12|13|14|15|16|17|18|19|20|21|24|25|26|27|28|29|30|42|43)\b" \
     android/app/src/main/kotlin/com/kayten/uhsm/ \
     --glob '*.kt' \
     --glob '!**/*Test*.kt' \
     | rg -i 'slot|kSlot|SLOT|conversation|messaging|voice|secure.?channel'
  # => 0 hits (any match = stale numeric slot ref that must migrate to 110..239)

  # No legacy slot symbols anywhere.
  rg "kSlotSecureChannel|kSlotMessaging|kSlotVoiceEpoch" \
     android/app/src/main/kotlin/com/kayten/uhsm/ \
     --glob '*.kt' \
     --glob '!**/*Test*.kt'
  # => 0 hits (all references should be by proto-generated CryptoKeyId.*)

  # Analyzer clean.
  flutter analyze
  # => 0 errors (info-level trailing commas OK)

  # Dart format.
  dart format --set-exit-if-changed .
  # => exit 0

  # Tests.
  flutter test
  # => all pass (~1500+ tests; note ~5 pre-existing failures unrelated to this work)
  cd android && ./gradlew :app:testDebugUnitTest
  # => pass

  # 0x45 via 0x55 on hw-prod build.
  grep -A5 "LoginUser\|LOGIN_USER_V1" android/app/src/main/kotlin/com/kayten/uhsm/hsm/HsmPlugin.kt
  # => shows executeViaSecureEnvelope routing guarded by capability bit

  # Capability outbox file created, not in Keychain.
  # Manual inspection of lib/data/local/wipe_revocation_outbox.dart — should NOT
  # import flutter_secure_storage or SecureTokenStore.

  # WorkManager scheduled.
  rg "WipeRevocationDrainer|wipe_revocation_drain" android/app/src/main/
  # => at least 1 hit (WorkManager registration)

  # Silent-wipe probe wired in.
  rg "silent_wipe_detected" lib/core/boot/
  # => at least 1 hit

======================================================================
GUARDRAILS
======================================================================

- `capability_jwt` MUST NOT be written to `flutter_secure_storage`,
  Keychain, or the wrapped-key DB. It lives in native outbox storage ONLY.
- `0x44 PROVISION_DEVICE_V1` MUST remain plaintext-outer in all profiles.
  Attempting to 0x55-wrap it on a fresh HSM will fail (handshake needs
  identity key that 0x44 is the cmd that generates).
- `0x45 LOGIN_USER_V1` 0x55-wrap gated on `SPI_CAP_MOBILE_PROFILE_V1`.
  Without the capability bit, stay on bare-outer for backward compat.
- Local wipe MUST NOT delete the outbox file.
- Outbox-first: write outbox BEFORE attempting the live-JWT RevokeDevice
  call, not after. Otherwise a process-kill during the RPC loses the
  revocation intent.
- WorkManager drain uses UNAUTHENTICATED CallOptions. Do NOT accidentally
  call the authenticated gRPC client (it would fail with no JWT).
- Weeks 1-3 gate: do NOT require `SPI_CAP_MOBILE_PROFILE_V1` to be
  advertised by the host until Week 3 merge — until then the app can
  still talk to old hosts.

======================================================================
WHEN DONE, RETURN
======================================================================

- Branch name + head SHA.
- `flutter analyze` + `dart format` output (both clean).
- Test summary: Flutter test count, Kotlin test count.
- Diff stats per major area (android/, lib/, proto bump).
- Confirmation the `rg` gates (parent §11) pass against the kayten-app tree.
- List of unsupported scenarios intentionally left as fail-closed (e.g.
  capability drain after token expiry — documents the UX that emerges).
- Risk list.
```
