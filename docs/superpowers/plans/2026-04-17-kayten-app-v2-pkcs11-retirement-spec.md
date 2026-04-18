# kayten-app-v2 — PKCS#11 retirement spec (full rewrite, no backwards compat)

- Date: 2026-04-17
- Scope repo: `/Users/tahir/Repos/kayten-app-v2`
- Parent: `2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`

## 0) Scope explicitly differs from kayten-app (v1)

**kayten-app-v2 has NO backwards-compatibility requirement with its own prior state and NO backwards-compatibility requirement with the v1 app's schema / wire / key-id layout.** Per product direction:

- **Full Rust-core refactor is authorized.** `rust-core/kayten-keymanager` (and any other Rust module touching the SPI surface) may be restructured, renamed, or rewritten outright.
- **Zero PKCS#11 residue.** No `pkcs11`, no `C_*` names, no `session_handle`, no `object_handle`, no `CKA_*` / `CKO_*` / `CKK_*` — delete on sight. Not `#if`-d out, not renamed, not deprecated — gone.
- **No schema migration path.** Any existing app-v2 wrapped-key DB, key store, Keychain/Keystore entries from prior builds are dropped on first launch post-upgrade; users re-enroll. This matches the v1 app's schema-v2 forced-re-enrollment policy but applies even more aggressively since v2 has no published "v1 → v2" migration contract to honour.
- **No dual-codepath.** No gating that lets v2 speak the legacy PKCS#11 mobile wire to accommodate an older host. If the host does not advertise `SPI_CAP_MOBILE_PROFILE_V1`, v2 refuses to bootstrap with a clear "firmware/host too old" error.

This gives the implementer maximum freedom to simplify the transport stack, flatten abstractions, and collapse the Rust surface into what the semantic command model actually needs — ideally ≤ 500 LOC of Rust for the entire HSM-transport layer.

## 1) Objective

Deliver app-v2's side of the PKCS#11 retirement with a clean-sheet Rust core, native bridges (Android Kotlin + iOS Swift) that speak only the semantic command surface, a simulator / SoftHSM mirroring firmware behaviour for offline development, the capability-token self-revoke flow, and silent-wipe detection on cold start.

## 2) Required implementation scope

### 2.1 Rust core — full refactor

Restructure `rust-core/kayten-keymanager/` around the semantic model:

- **Delete** anything with PKCS#11 lineage: `slot_manager_pkcs11.rs`, `object_registry.rs`, `session_table.rs`, any `Pkcs11*` types, `SessionHandle`/`ObjectHandle` newtypes. Do NOT keep these as deprecated wrappers — delete the types AND their call sites.
- **Add** the four new slot bands as a Rust-side mirror of the proto-generated `CryptoKeyId` enum:
  ```rust
  pub mod crypto_key_id {
      pub const IDENTITY_PRIV: u32          = 22;
      pub const IDENTITY_PUB: u32           = 23;
      pub const SPK_PRIV_FIRST: u32         = 35;
      pub const SPK_PUB_LAST: u32           = 44;
      pub const MSG_BOOTSTRAP_SECRET: u32   = 64;
      pub const OPK_PRIV_FIRST: u32         = 70;
      pub const OPK_PUB_LAST: u32           = 109;

      // New bands (master §9b)
      pub const SECURE_CHANNEL_FIRST: u32   = 110;
      pub const SECURE_CHANNEL_LAST: u32    = 117;
      pub const VOICE_EPHEMERAL_FIRST: u32  = 118;
      pub const VOICE_EPHEMERAL_LAST: u32   = 129;
      pub const CONVERSATION_FIRST: u32     = 130;
      pub const CONVERSATION_LAST: u32      = 191;
      pub const EPHEMERAL_FIRST: u32        = 192;
      pub const EPHEMERAL_LAST: u32         = 239;
  }
  ```
- **Add** `translator.rs` — mandatory, not follow-up. Mirror the Dart/Kotlin/Go reference translators published under `kayten-proto/references/translators/`. Signature:
  ```rust
  pub fn provisioning_state_from_wire(b: u8) -> ProvisioningState;
  pub fn provisioning_state_to_wire(s: ProvisioningState) -> u8;
  pub fn wipe_reason_from_wire(b: u8) -> WipeReason;
  pub fn wipe_reason_to_wire(r: WipeReason) -> u8;
  ```
- **Collapse** any multi-layer abstraction (`Transport` → `SessionMultiplexer` → `KeyStore` → `PrimitiveExecutor`) into a minimal `HsmClient` with methods keyed to semantic commands (`login_user`, `change_user_pin`, `generate_messaging_key`, `read_public_key`, `delete_messaging_key`, `rotate_spk`, `generate_random`, `issue_self_revoke_capability`, `revoke_device_by_capability`, …). Aim for one method per SPI command plus one method per server capability RPC.

### 2.2 Android bridge (Kotlin)

`NativeHsmTransport.kt` is a thin pass-through to the Rust core via UniFFI / JNI:

- Delete any Kotlin-side key-registry / handle-translation code.
- Import `translator.kt` from `kayten-proto/references/translators/` (copy into the app-v2 tree — keeps build-graph simple).
- `0x45 LOGIN_USER_V1` routes through `CryptoOperations.executeViaSecureEnvelope` (the `0x55` wrapper) whenever `SPI_CAP_MOBILE_PROFILE_V1` is advertised AND the build is hardware-prod. Same rule as `0x46`.
- `0x44 PROVISION_DEVICE_V1` remains plaintext-outer (parent §10 #14 / §3.1.1).

### 2.3 iOS bridge (Swift)

`ExternalHsmClient.swift` same pattern. Add `translator.swift` (mandatory in this PR, not follow-up).

### 2.4 Simulator / SoftHSM

Mirror firmware semantics exactly:

- Handlers for `0x60..0x68` (`0x67` only in dev-firmware context per master §2.4.1).
- **Init-pin exponential throttle** matching firmware's table (master §5.1.1): free 0..2 fails, then 10 s → 60 s → 600 s → 3600 s. Return `SPI_MOBILE_STATUS_INIT_PIN_THROTTLED (0xF5)` when in backoff window.
- **NVM-style write-order contract** on simulated `0x44` (master §5.1.2): simulate per-block atomic writes with injection hooks for mid-step crash tests.
- **New band allocation** for simulator slots: secure-channel 110..117, voice 118..129, conversation 130..191, ephemeral 192..239.
- **`0x68` / `0x65` / `0x66`** wipe flow with RAM nonce + 120 s TTL + single-shot semantics.
- `PROBE_STATE_V1` sets `SPI_CAP_MOBILE_PROFILE_V1 = 1<<6` and `SPI_CAP_DEV_FIRMWARE = 1<<7` (master §2.8 remap — firmware bit 5 is `KAYTEN_HSM_CAP_OPK_V1`).

### 2.5 Capability-token lifecycle (parent §6a.3.1)

Same flow as v1 app:

- Call `DeviceService.IssueSelfRevokeCapability` at enrollment success and on every successful login.
- Persist `capability_jwt` in native outbox companion file using the SAME file names as v1 so cross-app storage is consistent on devices that might run both:
  - Android: `kayten_wipe_revocation_outbox` (`EncryptedSharedPreferences`, `no_backup`, AndroidX `MasterKey.DEFAULT_MASTER_KEY_ALIAS`)
  - iOS: `Application Support/Kayten/WipeRevocationOutbox.plist` (`NSFileProtectionCompleteUnlessOpen`, excluded from iCloud backup)
- On wipe trigger: write outbox entry → attempt live-JWT `RevokeDevice` with 60 s backoff → proceed to local wipe unconditionally.
- `WorkManager` (Android) / `BGAppRefreshTask` (iOS) drains via `DeviceService.RevokeDeviceByCapability` (unauthenticated).

### 2.6 Silent-wipe detection on cold start (parent §6a.3 step 0)

On every app launch, before any other HSM crypto operation beyond `0x43`/`0x49`/`0x66`, probe `GET_RUNTIME_STATUS_V1`. If `provisioning_state == PROVISIONING_REQUIRED` AND local state shows a registered device, run the wipe-recovery flow with `reason = "silent_wipe_detected"`.

### 2.7 Wire → server outbox

Identical native outbox file names as v1 (parent §6a.3).

## 3) Provisioning / wipe behavior

Use parent §6a.3 ordering:

- **Step 0** silent-wipe probe on cold start.
- Durable `capability_jwt`-backed outbox **before** local wipe.
- User-initiated wipe via `0x68` → `0x65` (`0x55`-wrapped in hardware prod).
- **`LOGIN_USER_V1 (0x45)` inner-only in hardware prod** — bare outer rejected with `0xE0` once `SPI_CAP_MOBILE_PROFILE_V1` is advertised. Same rule as `0x46`. Parent §3.1 / §3.1.1 / §4.
- `PROVISION_DEVICE_V1 (0x44)` remains plaintext outer.

State machine matches v1: `PROVISIONING_REQUIRED` → `PROVISIONED_LOGGED_OUT` → `PROVISIONED_LOGGED_IN` + transient `LOCKED_WIPED`.

## 4) Compatibility

- **No backward compatibility with legacy PKCS#11 wire surface.**
- **No backward compatibility with prior app-v2 builds.** First launch post-upgrade drops any existing keystore / wrapped-key DB / cached session state.
- App-v2 **refuses to bootstrap** against a host that does not advertise `SPI_CAP_MOBILE_PROFILE_V1` — surface a clear "firmware / host too old — please update" error. No fallback, no legacy path.
- Wire format for `0x40` / `0x41` / `0x42` unchanged — 1-byte `convKey*` fields carry values from the new bands (130..191) without format change.

## 5) Verification

- Android + iOS bridge compile.
- Rust core builds with `cargo build --all-targets --all-features`; tests with `cargo test --all-features` pass.
- `translator.rs` + `translator.swift` + imported `translator.kt` exist and unit-test round-trip mappings.
- Simulated transport command matrix includes `0x60..0x68` (+ `0x67` in dev context).
- Simulator implements init-pin throttle (matches firmware table exactly).
- Simulator implements write-order atomicity for `0x44` with mid-step crash injection test.
- Wipe and re-provisioning scenarios tested on simulator path.
- **Capability-token roundtrip against kayten-server staging:** enroll → issue → simulate wipe → drain via `RevokeDeviceByCapability` → assert server revoked device.
- **Slot band range-check in simulator:** `READ_PUBLIC_KEY_V1` with id `213` (ephemeral) returns a pub; with id `500` (out-of-band) returns `KEY_ID_NOT_READABLE (0xE1)`.
- **Silent-wipe detection on cold start:** power-cycle simulator mid-wipe; restart app; assert probe triggers revocation-outbox flow.
- **No residual PKCS#11:** `rg "pkcs11|session_handle|object_handle|CKA_|CKO_|CKK_" rust-core/src android/app/src ios/Kayten` returns 0 hits (excluding `docs/archive/`).
- **No legacy PKCS#11 host tolerance:** simulator configured without `SPI_CAP_MOBILE_PROFILE_V1` → app refuses bootstrap with the expected error.

Archive `docs/v2.1/uhsm_host_mobile_gateway_spec_2026-03-27.md` and sibling v2.1 specs under `docs/archive/2026-04-17-pkcs11-retirement/`.

## 6) Out of scope

- Two-phase provisioning (tracked in `2026-04-18-two-phase-provisioning-cross-repo-spec.md` — closes parent §10 #14 residual).
