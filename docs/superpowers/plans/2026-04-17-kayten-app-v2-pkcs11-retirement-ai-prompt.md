# kayten-app-v2 PKCS#11 retirement — full Rust refactor + capability-token + silent-wipe — AI prompt

- **Date:** 2026-04-17
- **Repo scope:** `/Users/tahir/Repos/kayten-app-v2`
- **Authoritative repo spec:** [`2026-04-17-kayten-app-v2-pkcs11-retirement-spec.md`](2026-04-17-kayten-app-v2-pkcs11-retirement-spec.md)
- **Parent spec:** [`2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`](2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md)
- **Sibling reference (bigger surface, more constraints):** [`2026-04-17-kayten-app-pkcs11-retirement-spec.md`](2026-04-17-kayten-app-pkcs11-retirement-spec.md) — v1 flow. v2 does NOT have to mirror v1's migration gymnastics — see §0 of this repo spec.
- **Upstream dependencies (merge order):** proto → server → firmware + host in parallel.

---

```text
You are Claude (Opus or Sonnet) operating inside the `kayten-app-v2`
repo at `/Users/tahir/Repos/kayten-app-v2`. This is the next-gen app
with a Rust core (`rust-core/kayten-keymanager`) + Android (Kotlin) +
iOS (Swift) bridges + a simulator transport for dev.

Your task has EXPLICITLY EXPANDED authority compared to the v1 workstream:

  1. NO backwards compatibility with the legacy PKCS#11 mobile wire.
  2. NO backwards compatibility with prior kayten-app-v2 builds
     (no schema migration path — drop everything, re-enroll).
  3. FULL refactor of rust-core/kayten-keymanager is authorized.
     Restructure, rename, rewrite as needed.
  4. ZERO PKCS#11 residue anywhere in the tree. Delete types AND their
     call sites. No deprecated wrappers. No `#if`-guarded legacy.
  5. Rust + Swift translator reference implementations are MANDATORY
     in this PR (not follow-up).

Beyond the free-hand cleanup, also implement: the four new slot bands
(110..239), the capability-token self-revoke lifecycle (parent §6a.3.1),
silent-wipe detection on cold start (parent §6a.3 step 0), and
hardware-prod `0x45` via `0x55` inner dispatch (parent §3.1.1 / §4).

If the host does not advertise `SPI_CAP_MOBILE_PROFILE_V1`, v2 MUST
refuse to bootstrap with a clear "firmware / host too old" error. No
fallback, no legacy path.

======================================================================
MANDATORY READING (in order)
======================================================================

1. `docs/superpowers/plans/2026-04-17-kayten-app-v2-pkcs11-retirement-spec.md`
   — authoritative scope for this repo. §0 has the explicit "no
     backwards compat, full refactor authorized" language — internalize
     it before starting.

2. `docs/superpowers/plans/2026-04-17-kayten-app-pkcs11-retirement-spec.md`
   — v1 sibling. Much of the user-facing flow is the same. Refer for
     rationale on each step — but do NOT carry v1's compatibility
     gymnastics (schema v1→v2 path, legacy fallback, `HsmCommand.Legacy`
     delete dance) because v2 does not need them.

3. `docs/superpowers/plans/2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`
   — parent spec. §3.1.1 (PIN-protection rules), §6a.3, §6a.3.1
     (capability lifecycle), §9b (new slot bands), §9c (capability RPC
     contract), §10 #14 (plaintext 0x44 residual), §13 follow-ups.

4. `docs/superpowers/plans/2026-04-17-kayten-proto-hsm-keys-constants-spec.md`
   §5.1 — translator reference implementations. Copy the mechanical
     shape verbatim into `translator.rs` and `translator.swift`.

5. Existing v2 layout (to understand what to delete):
   - `rust-core/kayten-keymanager/src/` — current implementation. Expect
     to rewrite most of it. Keep only the pieces that are purely
     semantic (e.g. HKDF helpers, AES-GCM wrappers) — everything with
     a PKCS#11 vocabulary goes away.
   - `android/app/src/main/java/com/kayten/.../NativeHsmTransport.kt`
   - `ios/Kayten/.../ExternalHsmClient.swift`
   - `rust-core/kayten-keymanager/src/simulated_transport.rs` or
     equivalent — the simulator.
   - `docs/v2.1/uhsm_host_mobile_gateway_spec_2026-03-27.md` — archive.

======================================================================
STEP-BY-STEP TASK LIST
======================================================================

Task 1 — Branch + proto bump + regen
  Create `feat/2026-04-17-pkcs11-retirement-full-refactor` off `develop`.
  Bump `api/proto` submodule to the merged-kayten-proto SHA. Regenerate
  bindings for all three languages:
    Rust:    `cargo xtask gen-proto` (or build.rs with prost)
    Kotlin:  whatever the Android build invokes (may be same `buf generate`)
    Swift:   swift-protobuf generator (iOS build script)
  Commit submodule bump + regen diffs in an early commit.

Task 2 — Rust core: full refactor
  Clean-sheet the `rust-core/kayten-keymanager/` module layout. Guidance:

  a) DELETE on sight (do NOT rename, do NOT deprecate):
     - Any file with `pkcs11` in its name or path.
     - Any type named `*SessionHandle*`, `*ObjectHandle*`,
       `*SlotId*` (the legacy Dart-style logical slot), `*Pkcs11*`,
       `*ObjectRegistry*`, `*AttributeTemplate*`.
     - Any enum variant or constant with a `CK` / `CKA_` / `CKO_` / `CKK_` prefix.
     Use `rg` to find them; delete each and every call site.

  b) REBUILD the module layout around the semantic command model.
     Target structure (suggestive, rearrange freely):

       rust-core/kayten-keymanager/src/
         lib.rs
         crypto_key_id.rs      -- band constants, mirrors proto CryptoKeyId
         translator.rs         -- NEW — mandatory (Task 3)
         hsm_client.rs         -- one method per SPI command, one method per server RPC
         secure_channel.rs     -- 0x55 ECDH handshake + session cipher
         wipe_outbox.rs        -- capability-token outbox abstraction
         simulator/            -- soft-HSM implementation (Task 6)
           mod.rs
           provisioning.rs     -- 0x44 with write-order + throttle
           wipe.rs             -- 0x65/0x66/0x68
           ...

     Aim for ≤ 500 LOC of transport-layer Rust when done. If you exceed
     that significantly, the refactor has not gone deep enough.

  c) `HsmClient` method signature pattern (semantic only):

       impl HsmClient {
           pub async fn probe_state(&self) -> Result<ProbeStateV1, HsmError>;
           pub async fn provision_device(&self, init_pin: &[u8], user_pin: &[u8])
               -> Result<ProvisionDeviceResponse, HsmError>;
           pub async fn login_user(&self, user_pin: &[u8])
               -> Result<LoginUserResponse, HsmError>;  // routed through 0x55 in hw-prod
           pub async fn change_user_pin(&self, old: &[u8], new: &[u8])
               -> Result<(), HsmError>;
           pub async fn generate_messaging_key(&self, kind: MessagingKeyKind)
               -> Result<GenerateMessagingKeyResponse, HsmError>;
           pub async fn read_public_key(&self, crypto_key_id: u32)
               -> Result<Vec<u8>, HsmError>;
           pub async fn delete_messaging_key(&self, crypto_key_id: u32)
               -> Result<(), HsmError>;
           pub async fn rotate_spk(&self, old_spk_id: u32)
               -> Result<RotateSpkResponse, HsmError>;
           pub async fn generate_random(&self, length: u16)
               -> Result<Vec<u8>, HsmError>;
           pub async fn request_wipe_challenge(&self)
               -> Result<WipeChallenge, HsmError>;
           pub async fn user_initiated_wipe(&self, user_pin: &[u8], nonce: [u8; 32])
               -> Result<(), HsmError>;
           pub async fn get_wipe_status(&self)
               -> Result<WipeStatus, HsmError>;
           pub async fn get_runtime_status(&self)
               -> Result<RuntimeStatusV1, HsmError>;
           // ... messaging runtime 0x40/0x41/0x42, voice 0x4B/0x4C, etc.
       }

  d) Add `crypto_key_id` module with the full band map (master §9b):
     identity 22/23, SPK 35..44, msg_bootstrap 64, OPK 70..109,
     secure_channel 110..117, voice_ephemeral 118..129, conversation
     130..191, ephemeral 192..239.

  e) Fail-closed bootstrap: on `probe_state`, if
     `cap_bits & CAP_MOBILE_PROFILE_V1 == 0` → return
     `HsmError::HostTooOld` with message "firmware / host too old —
     please update". No legacy-path fallback.

Task 3 — `translator.rs` (MANDATORY in this PR)
  Create `rust-core/kayten-keymanager/src/translator.rs`. Copy the
  mechanical mapping from `kayten-proto/references/translators/translator.go`
  (structurally identical, just in Rust syntax):

  ```rust
  use crate::hsm_keys_v1::{ProvisioningState, WipeReason};

  pub fn provisioning_state_from_wire(w: u8) -> ProvisioningState {
      match w {
          0x00 => ProvisioningState::ProvisioningRequired,
          0x01 => ProvisioningState::Provisioned,
          0x02 => ProvisioningState::LockedWiped,
          _    => ProvisioningState::Unspecified,
      }
  }

  pub fn provisioning_state_to_wire(s: ProvisioningState) -> u8 {
      match s {
          ProvisioningState::ProvisioningRequired => 0x00,
          ProvisioningState::Provisioned          => 0x01,
          ProvisioningState::LockedWiped          => 0x02,
          _                                       => 0xFF,
      }
  }

  pub fn wipe_reason_from_wire(w: u8) -> WipeReason {
      match w {
          0x00 => WipeReason::None,
          0x01 => WipeReason::PinLockout,
          0x02 => WipeReason::UserRequest,
          0x03 => WipeReason::AttestationFailed,
          0x04 => WipeReason::PinLockoutRecovery,
          _    => WipeReason::Unspecified,
      }
  }

  pub fn wipe_reason_to_wire(r: WipeReason) -> u8 {
      match r {
          WipeReason::None               => 0x00,
          WipeReason::PinLockout         => 0x01,
          WipeReason::UserRequest        => 0x02,
          WipeReason::AttestationFailed  => 0x03,
          WipeReason::PinLockoutRecovery => 0x04,
          _                              => 0xFF,
      }
  }

  #[cfg(test)]
  mod tests {
      use super::*;
      #[test]
      fn round_trip_provisioning_state() {
          for wire in &[0x00u8, 0x01, 0x02] {
              assert_eq!(provisioning_state_to_wire(provisioning_state_from_wire(*wire)), *wire);
          }
      }
      #[test]
      fn round_trip_wipe_reason() {
          for wire in &[0x00u8, 0x01, 0x02, 0x03, 0x04] {
              assert_eq!(wipe_reason_to_wire(wipe_reason_from_wire(*wire)), *wire);
          }
      }
      #[test]
      fn unknown_wire_maps_to_unspecified() {
          assert!(matches!(provisioning_state_from_wire(0xAA), ProvisioningState::Unspecified));
          assert!(matches!(wipe_reason_from_wire(0xBB), WipeReason::Unspecified));
      }
  }
  ```

  Include the round-trip unit tests as shown.

Task 4 — Android bridge (Kotlin)
  `NativeHsmTransport.kt`:
  - Delete all PKCS#11 vocabulary.
  - Thin pass-through to Rust core via UniFFI or direct JNI binding.
  - Copy `translator.kt` body from
    `kayten-proto/references/translators/translator.kt` into the app-v2
    tree (single source still lives in kayten-proto; this is a build-
    graph simplification so Android can build offline).
  - `LOGIN_USER_V1 (0x45)` in hw-prod profile → route through `0x55`
    inner dispatch. Same gate on `SPI_CAP_MOBILE_PROFILE_V1`. Dev/
    staging keep bare-outer fast path.

Task 5 — iOS bridge (Swift)
  `ExternalHsmClient.swift`:
  - Delete all PKCS#11 vocabulary.
  - Add `translator.swift` — MANDATORY in this PR. Mirror `translator.kt`
    in idiomatic Swift:

    ```swift
    // kayten-app-v2 — translator.swift
    import Foundation
    // Assumes SwiftProtobuf-generated ProvisioningState / WipeReason enums.

    public func provisioningStateFromWire(_ w: UInt8) -> ProvisioningState {
        switch w {
        case 0x00: return .provisioningStateProvisioningRequired
        case 0x01: return .provisioningStateProvisioned
        case 0x02: return .provisioningStateLockedWiped
        default:   return .provisioningStateUnspecified
        }
    }

    public func provisioningStateToWire(_ s: ProvisioningState) -> UInt8 {
        switch s {
        case .provisioningStateProvisioningRequired: return 0x00
        case .provisioningStateProvisioned:           return 0x01
        case .provisioningStateLockedWiped:           return 0x02
        default:                                       return 0xFF
        }
    }

    public func wipeReasonFromWire(_ w: UInt8) -> WipeReason {
        switch w {
        case 0x00: return .wipeReasonNone
        case 0x01: return .wipeReasonPinLockout
        case 0x02: return .wipeReasonUserRequest
        case 0x03: return .wipeReasonAttestationFailed
        case 0x04: return .wipeReasonPinLockoutRecovery
        default:   return .wipeReasonUnspecified
        }
    }

    public func wipeReasonToWire(_ r: WipeReason) -> UInt8 {
        switch r {
        case .wipeReasonNone:               return 0x00
        case .wipeReasonPinLockout:         return 0x01
        case .wipeReasonUserRequest:        return 0x02
        case .wipeReasonAttestationFailed:  return 0x03
        case .wipeReasonPinLockoutRecovery: return 0x04
        default:                             return 0xFF
        }
    }
    ```

    Include XCTest round-trip cases analogous to the Rust tests.

Task 6 — Simulator / SoftHSM
  Under `rust-core/kayten-keymanager/src/simulator/`:
  - Handlers for `0x60..0x68` (`0x67` only in dev-firmware context).
  - Init-pin exponential throttle matching firmware table exactly
    (master §5.1.1): free 0..2 fails, then 10/60/600/3600 s.
    Return `SPI_MOBILE_STATUS_INIT_PIN_THROTTLED (0xF5)` when in window.
  - NVM-style write-order contract on simulated `0x44` (master §5.1.2).
    Add crash-injection hooks (`#[cfg(test)]` feature) so tests can
    interrupt between the three commit points.
  - New band allocation: secure_channel 110..117, voice 118..129,
    conversation 130..191, ephemeral 192..239.
  - `0x68` RAM nonce + 120 s TTL + single-shot; cleared on logout /
    successful `0x65` / power-cycle.
  - `PROBE_STATE_V1` sets `SPI_CAP_MOBILE_PROFILE_V1 = 1<<6` and
    `SPI_CAP_DEV_FIRMWARE = 1<<7` (parent §2.8 remap — firmware bit 5 is `KAYTEN_HSM_CAP_OPK_V1`).

Task 7 — Capability-token lifecycle (parent §6a.3 / §6a.3.1)
  `rust-core/kayten-keymanager/src/wipe_outbox.rs`:
  - Abstract `WipeOutbox` trait with methods matching v1's Dart
    interface (`write_capability_token`, `read_capability_token`,
    `enqueue_entry`, `list_entries`, `remove_entry`).
  - Android impl via UniFFI callback to the Kotlin
    `EncryptedSharedPreferences` backend (file name
    `kayten_wipe_revocation_outbox` — SAME as v1).
  - iOS impl via UniFFI callback to the Swift plist backend (file
    `Application Support/Kayten/WipeRevocationOutbox.plist` — SAME as v1).
  - Simulator stub: in-memory.
  - Wire into enrollment + login flows: call
    `HsmClient::issue_self_revoke_capability()` after each, store result.

Task 8 — Silent-wipe detection on cold start
  In the app-v2 boot coordinator (Kotlin / Swift entrypoint), invoke
  Rust-core `probe_and_detect_silent_wipe()` before any other HSM op:

    pub async fn probe_and_detect_silent_wipe(&self)
        -> Result<BootDecision, HsmError>
    {
        let runtime = self.get_runtime_status().await?;
        if runtime.provisioning_state == ProvisioningState::ProvisioningRequired
           && self.local_state.registered_device_id.is_some()
        {
            return Ok(BootDecision::SilentWipeDetected {
                last_wipe_reason: runtime.last_wipe_reason,
            });
        }
        // ... other boot decisions
    }

  On `SilentWipeDetected`, enqueue outbox entry with
  `reason = "silent_wipe_detected"` + `runtime.last_wipe_reason`, run
  local wipe, navigate to provisioning.

Task 9 — Hardware-prod `0x45` via `0x55` inner dispatch
  In `hsm_client.rs::login_user()`: when the cached `probe_state()`
  shows `SPI_CAP_MOBILE_PROFILE_V1 = 1` AND the build is compiled with
  `cfg(feature = "hardware_prod")`, build the request as an inner
  `0x55` payload. Otherwise bare-outer.

Task 10 — No-legacy-host refusal
  `probe_state` → if capability bit absent, return
  `HsmError::HostTooOld { message: "Firmware or host is too old.
  Please update to a version that supports mobile profile v1." }`.
  Boot coordinator surfaces this as a hard error screen; no retry,
  no "continue with reduced features" path.

Task 11 — Archive legacy specs
  `git mv docs/v2.1/uhsm_host_mobile_gateway_spec_2026-03-27.md
          docs/archive/2026-04-17-pkcs11-retirement/` and any siblings.

======================================================================
SELF-VERIFICATION BEFORE PR
======================================================================

  # Rust core builds.
  (cd rust-core && cargo build --all-targets --all-features)
  # => success

  # Rust core tests.
  (cd rust-core && cargo test --all-features)
  # => all pass

  # Translator round-trip tests pass in all three languages.
  (cd rust-core && cargo test translator)
  # => pass
  (cd android && ./gradlew :app:testDebugUnitTest --tests "*Translator*")
  # => pass
  (cd ios && xcodebuild test -scheme Kayten -only-testing:KaytenTests/TranslatorTests)
  # => pass

  # Android + iOS bridges compile.
  (cd android && ./gradlew :app:assembleDebug)
  (cd ios && xcodebuild build -scheme Kayten -configuration Debug -sdk iphonesimulator)
  # => success

  # Zero PKCS#11 residue anywhere.
  rg "pkcs11|PKCS11|session_handle|object_handle|CKA_|CKO_|CKK_|SessionHandle|ObjectHandle" \
     rust-core/src android/app/src ios/Kayten
  # => 0 hits (excluding docs/archive/ and generated proto stubs)

  # Slot bands present in Rust.
  rg "SECURE_CHANNEL_FIRST:\s*u32\s*=\s*110|CONVERSATION_FIRST:\s*u32\s*=\s*130|EPHEMERAL_FIRST:\s*u32\s*=\s*192" \
     rust-core/kayten-keymanager/src/crypto_key_id.rs
  # => 3 hits

  # Translator files exist.
  ls rust-core/kayten-keymanager/src/translator.rs \
     ios/Kayten/Translator.swift
  # => both exist

  # Simulator command matrix.
  (cd rust-core && cargo test -p kayten-keymanager simulator::tests)
  # => tests for 0x44, 0x45, 0x46, 0x60..0x68 all green

  # No-legacy-host refusal wired.
  rg "HostTooOld|MOBILE_PROFILE_V1" rust-core/kayten-keymanager/src/hsm_client.rs
  # => at least 2 hits

  # Capability outbox references.
  rg "issue_self_revoke_capability|revoke_device_by_capability" \
     rust-core/ android/app/src ios/Kayten
  # => multiple hits per RPC

  # Archive move committed.
  test ! -f docs/v2.1/uhsm_host_mobile_gateway_spec_2026-03-27.md
  test -f docs/archive/2026-04-17-pkcs11-retirement/uhsm_host_mobile_gateway_spec_2026-03-27.md
  # => both true

======================================================================
GUARDRAILS
======================================================================

- ZERO `pkcs11` / `PKCS#11` / `session_handle` / `object_handle` /
  `CKA_*` / `CKO_*` / `CKK_*` anywhere in active source (docs/archive/
  is fine).
- `translator.rs` + `translator.swift` are REQUIRED — not optional,
  not follow-up.
- `capability_jwt` MUST NOT be written to Keychain / Keystore / any
  wiped-on-local-wipe storage.
- `0x44 PROVISION_DEVICE_V1` MUST remain plaintext-outer. Attempting
  to 0x55-wrap it on a fresh HSM will fail (handshake needs identity
  key that 0x44 generates).
- `0x45 LOGIN_USER_V1` inner-dispatch gated on cap-bit advertisement.
- App-v2 refuses bootstrap when cap-bit is absent. Do NOT add a
  fallback. Do NOT add a flag to override.
- Simulator throttle MUST match firmware table exactly.
- No migration tooling for prior app-v2 builds. On first launch post-
  upgrade, drop any existing DB / Keychain / Keystore entries cleanly
  and treat the device as fresh.
- DO NOT implement two-phase provisioning — it's a separate
  follow-up (tracked in 2026-04-18-two-phase-provisioning-cross-repo-spec.md).

======================================================================
WHEN DONE, RETURN
======================================================================

- Branch name + head SHA.
- Build logs for Rust + Android + iOS.
- Rust LOC counts before/after (expect significant net reduction).
- Test summary per layer (Rust, Kotlin, Swift).
- Before/after module-tree snapshot of rust-core/kayten-keymanager/src/.
- List of deleted files + deleted types (for reviewer confidence that
  the "zero PKCS#11 residue" guarantee holds).
- Verification-gate output.
- Risk list.
```
