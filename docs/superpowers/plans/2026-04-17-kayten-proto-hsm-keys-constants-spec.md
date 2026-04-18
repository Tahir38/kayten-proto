# kayten-proto — `hsm_keys.v1` constants package + `GetIdentityKeyResponse.revoked_at`

- **Date:** 2026-04-17
- **Status:** design draft, awaiting approval
- **Repo scope:** `kayten-proto` (submodule at `api/proto` in both `kayten-app` and `kayten-server`; remote `gitea@192.168.2.86:Tahir/kayten-proto.git`).
- **Parent spec (§0):** [`2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`](2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md)
- **Related workstream (lands ahead of this one):** [`2026-04-16-kayten-proto-opk-nvm-band-and-dh4-spec.md`](2026-04-16-kayten-proto-opk-nvm-band-and-dh4-spec.md) — must be merged first. This spec assumes OPK/DH4 constants land as part of §3.6 below.
- **Fan-out rollout position:** Appendix A item **#1** — smallest, unblocks every other repo.
- **Branch name (this workstream):** `feat/2026-04-17-hsm-keys-v1-constants` off whichever base the consumer repos track (kayten-server tracks `develop`; kayten-app pins whatever commit its `api/proto` is set to — see §7).

## 0. Scope

Four atomic, additive changes to the shared proto tree — nothing is removed, nothing is renumbered:

1. **New file** `proto/kayten/hsm_keys/v1/hsm_keys.proto` — new package `kayten.hsm_keys.v1` containing:
   - `enum CryptoKeyId` — canonical firmware key-id band constants. **Identity / SPK / OPK boundaries PLUS four new bands** (§3 below): secure-channel `110..117`, voice ephemeral `118..129`, conversation `130..191`, general ephemeral `192..239` — closes the namespace collision between new identity/SPK bands and today's `kSlotSecureChannelStart..End (9..12)` / `kSlotMessagingStart..End (13..30)` / `kSlotVoiceEpoch* (42..43)`. Replaces the Dart-level `kSlot*` constants and the Kotlin `Pkcs11ObjectRegistry` handle mapping.
   - `enum MobileCommandId` — canonical SPI command ids through **`0x68`**
     (`REQUEST_WIPE_CHALLENGE_V1`, master §5.8; `0x67` dev-only) used across host
     and app code, replacing the scattered `CMD_*` / `Kayten.*` constant soup.
   - `enum ProvisioningState` — the runtime state reported by `0x49 GET_RUNTIME_STATUS_V1`.
   - `enum WipeReason` — cause reported alongside wipe status.
   - `enum CryptoKeyIdBandSize` — cardinalities of the `CryptoKeyId` bands (§3 below includes SPK + OPK + 4 new bands).

2. **Additive field** on an existing message: `GetIdentityKeyResponse.revoked_at : google.protobuf.Timestamp = 4` in `proto/kayten/v1/device.proto`. Unset means active; set means the device identity has been revoked (pin-lockout wipe, user-initiated wipe, or `RevokeDevice` RPC).

3. **Two additive RPCs on `DeviceService`** (§3.5.1 below) — closes the post-wipe auth gap identified in the master-spec §6a.3 review. Additive on `proto/kayten/v1/device.proto`:
   - `rpc IssueSelfRevokeCapability(IssueSelfRevokeCapabilityRequest) returns (IssueSelfRevokeCapabilityResponse)` — Bearer-auth; returns a server-signed `capability_jwt` that survives local wipe.
   - `rpc RevokeDeviceByCapability(RevokeDeviceByCapabilityRequest) returns (RevokeDeviceByCapabilityResponse)` — **unauthenticated**; validates JWT + single-use `jti` and runs the standard `RevokeDevice` path.

4. **Translator reference implementations** — checked in at `references/translators/` (§5 below): `translator.dart`, `translator.kt`, `translator.go` (Rust/Swift follow-up). Single source-of-truth for the wire↔enum mapping that `_UNSPECIFIED = 0` forces every consumer to implement.

No wire-format additions to any other existing message. No enum value renumbers. No field removals. No package renames. Fully proto3-additive.

Buf `breaking` (profile `FILE`) must stay green — see §8.

## 1. Why this comes first in the fan-out

Every other per-repo workstream (uHSM-HSM handler renames, uHSM-Host `kMobileManager` dispatch table, kayten-app removal of `kSlot*`, kayten-app-v2 Rust bindings, kayten-server `DeviceService.RevokeDevice` additions) imports a symbolic key-id or command-id constant. Today those constants are duplicated in four places (Dart `crypto_constants.dart`, Kotlin `HsmConstants.kt`, C header `kPkcs11Manager.h`, Go `service.go`) and they drift — see master §1 pain-points 2 and 4.

Publishing them through the proto toolchain once fixes that drift permanently, because every downstream repo regenerates from the same file. We want the proto package shipped before any repo tries to `import` from it.

This workstream does **no changes to existing protobuf RPCs or message fields**
other than the single additive `GetIdentityKeyResponse.revoked_at` (§3.5). The
new `hsm_keys.proto` file documents SPI **constant** enums only — it does not
alter the gRPC wire for `kayten-server` beyond that field. It is cheap,
reviewable in one sitting, and merges independently of the firmware and app
work that follow in weeks 1–3 of master §8.

## 2. Repo state audit (what's there today)

Verified against `kayten-proto` `origin/develop` at submodule pointer `443e606` (local checkout `heads/develop`; consumer `api/proto` submodules in `kayten-app` and `kayten-server` pin this SHA and use `branch = develop` in `.gitmodules`):

| Item | Current state | Action |
|---|---|---|
| `proto/kayten/hsm_keys/` directory | **does not exist** | Create (new sub-package). |
| `proto/kayten/v1/*.proto` | 9 files: `attestation, auth, calling, common, contacts, conversation, device, messaging, user` | Untouched except `device.proto` (§3.5). |
| `GetIdentityKeyResponse` fields 1–3 | `ed25519_pub`, `ecdh_pub`, `signature` | Append field 4. |
| `device.proto` imports | none currently | Add `import "google/protobuf/timestamp.proto";`. |
| `buf.yaml` | v2, single module `buf.build/kayten/proto` at `path: proto`, lint `STANDARD`, breaking `FILE` | Unchanged. New files pick up the existing module automatically. |
| `buf.gen.yaml` | Go + Dart generators enabled | Unchanged. Consumer repos regenerate their own Rust/Kotlin/Swift bindings (§6). |
| Pre-existing enum naming convention | Mixed. `EnrollmentStatus` uses strict `ENROLLMENT_STATUS_*` prefix; `EnvelopeType` does **not** prefix (`SEND_MESSAGE = 1` etc.). Both pass buf lint STANDARD (`ENUM_VALUE_PREFIX` is not in that category on buf v2). | Use **strict prefix** for this new package (modern convention). No generated-symbol collisions across all consumers. |
| Submodule pins | `kayten-app/.gitmodules` and `kayten-server/.gitmodules` both set `branch = develop` for `api/proto`. | Submodule bump handled in §7. |
| `google/protobuf/timestamp.proto` usage elsewhere | Imported in `common.proto`, `messaging.proto`, `conversation.proto` | Pattern established; add import to `device.proto`. |

No hidden surprises. Nothing to reconcile before writing the patch.

## 3. `hsm_keys.proto` — full file content

Create **`proto/kayten/hsm_keys/v1/hsm_keys.proto`**:

```proto
syntax = "proto3";

// Canonical constants for the Kayten mobile <-> HSM SPI surface.
// Every repo (uHSM-Host, uHSM-HSM, kayten-app, kayten-app-v2, kayten-server)
// imports its firmware key-id boundaries and SPI command ids from here to
// eliminate the historical 4-way duplication across Dart, Kotlin, C, and Go.
//
// Wire-format note: these enum values encode the exact u8 / u32 numeric
// values that appear on the SPI wire. A Go/Rust/Dart/Kotlin consumer can
// cast the generated enum directly to its integer representation.
//
// See `docs/superpowers/plans/2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`
// sections 2.6, 2.7, 3, 5, and 9 for the source-of-truth design.
package kayten.hsm_keys.v1;

option go_package = "github.com/kayten-gmbh/kayten-server/pkg/generated/kayten/hsm_keys/v1;hsmkeysv1";

// Firmware-assigned `crypto_key_id` boundaries. See master spec §3.1, §5.3
// (`READ_PUBLIC_KEY_V1` permitted id ranges) and §6a.2 (`Kayten_WipeAllKeyMaterial`
// zeroization walk).
//
// Encoding: every enum value here is the literal u32 that appears on the wire
// as a `[crypto_key_id:4LE]` field. Adjacent private/public pairs are at
// `id` / `id+1`.
//
// Off-band ids (1..21, 24..34, 45..63, 110..) are available for future bands
// (SPK band occupies 35..44 inclusive per above).
// Firmware MUST reject ids outside the declared bands with
// `SPI_MOBILE_STATUS_KEY_ID_NOT_READABLE` (`0xE1`) for reads,
// `SPI_MOBILE_STATUS_KEY_ID_ROTATE_ONLY` (`0xE2`) for SPK-private writes,
// or `SPI_MOBILE_STATUS_KEY_ID_STALE` (`0xE3`) for stale rotate arguments.
enum CryptoKeyId {
  CRYPTO_KEY_ID_UNSPECIFIED = 0;

  // Identity key pair (Ed25519).
  CRYPTO_KEY_ID_IDENTITY_PRIV = 22;
  CRYPTO_KEY_ID_IDENTITY_PUB  = 23;

  // Signed Prekey band — odd = private, even = public.
  // Five simultaneously-valid pairs: 35/36, 37/38, 39/40, 41/42, 43/44.
  CRYPTO_KEY_ID_SPK_PRIV_FIRST = 35;
  CRYPTO_KEY_ID_SPK_PUB_FIRST  = 36;
  CRYPTO_KEY_ID_SPK_PRIV_LAST  = 43;
  CRYPTO_KEY_ID_SPK_PUB_LAST   = 44;

  // Messaging bootstrap secret (symmetric; single slot).
  CRYPTO_KEY_ID_MSG_BOOTSTRAP_SECRET = 64;

  // One-Time Prekey band — 20 pairs from 70/71 through 108/109 (master §5.3, §6a.2).
  // Private: 70,72,…,108. Public: 71,73,…,109. (SPK band uses odd private / even public;
  // OPK band uses the opposite pairing — do not copy SPK parity wording here.)
  // Consumed OPKs are zeroed in-place and remain reserved in the band.
  CRYPTO_KEY_ID_OPK_PRIV_FIRST = 70;
  CRYPTO_KEY_ID_OPK_PUB_FIRST  = 71;
  CRYPTO_KEY_ID_OPK_PRIV_LAST  = 108;
  CRYPTO_KEY_ID_OPK_PUB_LAST   = 109;

  // ===================================================================
  // NEW BANDS (master §9b) — close the collision between today's
  // kayten-app logical slot numbering (9..12 secure-channel, 13..30
  // messaging LRU, 42..43 voice ephemeral) and this spec's identity
  // (22/23) + SPK (35..44) bands. Renumbering internal slots into these
  // bands is a firmware + app config change only; the 1-byte convKey*
  // fields on 0x40/0x41/0x42 carry the new numeric values unchanged
  // (all values fit in 0..255).
  // ===================================================================

  // Secure-channel session keys — used for 0x55 SECURE_EXECUTE_V1
  // transport (Doppel-AES inner + outer). 8 slots = 4 concurrent
  // secure-execute sessions × 2 keys each. Today's
  // kSlotSecureChannelStart..End (9..12) migrates here.
  CRYPTO_KEY_ID_SECURE_CHANNEL_FIRST = 110;
  CRYPTO_KEY_ID_SECURE_CHANNEL_LAST  = 117;

  // Voice-call ephemeral + epoch keys — per-call P-256 ECDH + derived
  // epoch keys. 12 slots = 3 concurrent calls × 4 keys each. Today's
  // kSlotVoiceEpochInner/Outer (42..43) migrates into this band.
  CRYPTO_KEY_ID_VOICE_EPHEMERAL_FIRST = 118;
  CRYPTO_KEY_ID_VOICE_EPHEMERAL_LAST  = 129;

  // Per-conversation messaging keys — LRU-managed by the app's
  // ConversationSlotManager. 62 slots = 31 conversations × 2 keys
  // (convKeyInner + convKeyOuter). Today's
  // kSlotMessagingStart..End (13..30) migrates here.
  CRYPTO_KEY_ID_CONVERSATION_FIRST = 130;
  CRYPTO_KEY_ID_CONVERSATION_LAST  = 191;

  // General-purpose ephemeral keys — firmware-assigned ids returned by
  // GENERATE_MESSAGING_KEY_V1 { kind = EPHEMERAL_ECDH } and readable
  // via READ_PUBLIC_KEY_V1. 48 slots. Used for X3DH ephemeral ECDH
  // keys, voice-call transcript-signed ephemerals, etc.
  CRYPTO_KEY_ID_EPHEMERAL_FIRST = 192;
  CRYPTO_KEY_ID_EPHEMERAL_LAST  = 239;

  // 240..255 reserved for future bands.
}

// Cardinalities that ride alongside `CryptoKeyId` — exposed as a separate
// enum so consumers can reason about band sizes without hand-computing from
// FIRST / LAST gaps.
enum CryptoKeyIdBandSize {
  CRYPTO_KEY_ID_BAND_SIZE_UNSPECIFIED            = 0;
  CRYPTO_KEY_ID_BAND_SIZE_SPK_PAIRS              = 5;
  CRYPTO_KEY_ID_BAND_SIZE_OPK_PAIRS              = 20;
  // New bands (master §9b):
  CRYPTO_KEY_ID_BAND_SIZE_SECURE_CHANNEL_SLOTS   = 8;
  CRYPTO_KEY_ID_BAND_SIZE_VOICE_EPHEMERAL_SLOTS  = 12;
  CRYPTO_KEY_ID_BAND_SIZE_CONVERSATION_SLOTS     = 62;
  CRYPTO_KEY_ID_BAND_SIZE_EPHEMERAL_SLOTS        = 48;
}

// SPI command ids on the mobile surface. Every value here matches a byte
// emitted in the `cmd:1` position of `[A5 5A][length:2LE][cmd:1]…`. Firmware
// rejects any other id with `SPI_STATUS_CMD_NOT_AVAILABLE` (`0xE0`).
//
// Grouping (for readers):
//  * 0x40..0x42 — existing messaging runtime
//  * 0x43..0x4F — provisioning + session + storage + voice-frame family
//  * 0x53..0x5C — call, secure-exec, messaging-key-exchange, epoch, OPK
//  * 0x60..0x64 — NEW §3.2 PKCS#11 replacements; 0x65..0x66 wipe cmds;
//                 0x68 wipe-challenge; 0x67 DEV-only init-pin setter
enum MobileCommandId {
  MOBILE_COMMAND_ID_UNSPECIFIED = 0;

  // Messaging runtime (pre-existing)
  MOBILE_COMMAND_ID_ENCRYPT_AND_SIGN_MSG    = 0x40;
  MOBILE_COMMAND_ID_DECRYPT_AND_VERIFY_MSG  = 0x41;
  MOBILE_COMMAND_ID_FULL_RATCHET_STEP       = 0x42;

  // Provisioning / session / voice
  MOBILE_COMMAND_ID_PROBE_STATE_V1              = 0x43;
  MOBILE_COMMAND_ID_PROVISION_DEVICE_V1         = 0x44;  // renamed from PROVISION_TOKEN_V1
  MOBILE_COMMAND_ID_LOGIN_USER_V1               = 0x45;
  MOBILE_COMMAND_ID_CHANGE_USER_PIN_V1          = 0x46;
  MOBILE_COMMAND_ID_READ_IDENTITY_PUBLIC_V1     = 0x47;
  MOBILE_COMMAND_ID_IDENTITY_SIGN_V1            = 0x48;
  MOBILE_COMMAND_ID_GET_RUNTIME_STATUS_V1       = 0x49;
  MOBILE_COMMAND_ID_LOGOUT_USER_V1              = 0x4A;
  MOBILE_COMMAND_ID_VOICE_ENCRYPT_FRAME_V1      = 0x4B;
  MOBILE_COMMAND_ID_VOICE_DECRYPT_FRAME_V1      = 0x4C;
  MOBILE_COMMAND_ID_RESET_SESSION_STATE_V1      = 0x4D;
  MOBILE_COMMAND_ID_WRAP_STORAGE_ROOT_KEY_V1    = 0x4E;
  MOBILE_COMMAND_ID_UNWRAP_STORAGE_ROOT_KEY_V1  = 0x4F;

  // Call + secure-exec
  MOBILE_COMMAND_ID_CALL_KEY_AGREE_V1           = 0x53;
  MOBILE_COMMAND_ID_CALL_TEARDOWN_V1            = 0x54;
  MOBILE_COMMAND_ID_SECURE_EXECUTE_V1           = 0x55;

  // Messaging key exchange + epoch + OPK
  MOBILE_COMMAND_ID_MSG_KEY_EXCHANGE_V1             = 0x57;
  MOBILE_COMMAND_ID_GET_HSM_DET_BUFFER_V1           = 0x59;
  MOBILE_COMMAND_ID_PEER_RATCHET_STEP_V1            = 0x5A;
  MOBILE_COMMAND_ID_MSG_KEY_EXCHANGE_RESPONDER_V1   = 0x5B;
  MOBILE_COMMAND_ID_GENERATE_OPK_V1                 = 0x5C;

  // Pre-existing voice-mode / epoch / HKDF (not in 0x60 block because
  // assigned earlier, kept here for a complete map)
  MOBILE_COMMAND_ID_HKDF_DERIVE         = 0x3A;
  MOBILE_COMMAND_ID_GENERATE_EPOCH_KEY  = 0x3B;
  MOBILE_COMMAND_ID_SET_VOICE_MODE      = 0x3D;
  MOBILE_COMMAND_ID_CLEAR_VOICE_MODE    = 0x3E;

  // NEW §3.2 — replace PKCS#11 `C_GenerateKeyPair`, `C_GetAttributeValue`,
  // `C_DestroyObject`, `C_GenerateRandom` in the mobile dispatch.
  MOBILE_COMMAND_ID_GENERATE_MESSAGING_KEY_V1 = 0x60;
  MOBILE_COMMAND_ID_READ_PUBLIC_KEY_V1        = 0x61;
  MOBILE_COMMAND_ID_DELETE_MESSAGING_KEY_V1   = 0x62;
  MOBILE_COMMAND_ID_ROTATE_SPK_V1             = 0x63;
  MOBILE_COMMAND_ID_GENERATE_RANDOM_V1        = 0x64;

  // NEW §3.2 — wipe flows (decimal = wire byte; see §3.0.1 below)
  MOBILE_COMMAND_ID_USER_INITIATED_WIPE_V1     = 101; // 0x65
  MOBILE_COMMAND_ID_GET_WIPE_STATUS_V1         = 102; // 0x66
  // DEV firmware only — PROD omits handler; prod boards return 0xE0.
  MOBILE_COMMAND_ID_DEV_SET_INIT_PIN_V1        = 103; // 0x67
  MOBILE_COMMAND_ID_REQUEST_WIPE_CHALLENGE_V1  = 104; // 0x68 — master §5.8
}

// Runtime provisioning state as reported in `GET_RUNTIME_STATUS_V1` (§5.7)
// and in the `provisioning_state:1` byte of `PROVISION_DEVICE_V1` responses (§5.1).
//
// Wire encoding: master spec §5.1 / §5.7 use u8 wire values
//   0x00 = PROVISIONING_REQUIRED (fresh or wiped)
//   0x01 = PROVISIONED
//   0x02 = LOCKED_WIPED (transient — observable for at most one response;
//                        on next boot / next 0x49 the device reports
//                        PROVISIONING_REQUIRED with last_wipe_reason set)
//
// Generated-enum numeric values diverge from wire values by +1 because
// proto3 forces 0 = UNSPECIFIED. Consumers MUST translate via a small
// wire<->enum map (see §5 of the per-repo spec for a reference impl).
enum ProvisioningState {
  PROVISIONING_STATE_UNSPECIFIED           = 0;
  PROVISIONING_STATE_PROVISIONING_REQUIRED = 1;  // wire 0x00
  PROVISIONING_STATE_PROVISIONED           = 2;  // wire 0x01
  PROVISIONING_STATE_LOCKED_WIPED          = 3;  // wire 0x02 (transient)
}

// Cause of the most-recent HSM wipe, as reported by
// `GET_RUNTIME_STATUS_V1` (§5.7) and `GET_WIPE_STATUS_V1` (§5.8).
//
// Same wire-vs-enum numbering caveat as `ProvisioningState`.
enum WipeReason {
  WIPE_REASON_UNSPECIFIED            = 0;
  WIPE_REASON_NONE                   = 1;  // wire 0x00
  WIPE_REASON_PIN_LOCKOUT            = 2;  // wire 0x01
  WIPE_REASON_USER_REQUEST           = 3;  // wire 0x02
  WIPE_REASON_ATTESTATION_FAILED     = 4;  // wire 0x03
  WIPE_REASON_PIN_LOCKOUT_RECOVERY   = 5;  // wire 0x04 — post-power-loss
                                           // recovery heuristic fired
                                           // (§6a.2 final paragraph)
}
```

### 3.0.1 `CryptoKeyId` wire typing + decimal wipe command literals

- **`CryptoKeyId`:** the protobuf `enum` carries **band boundaries and sentinel
  names only**. Valid in-band ids (e.g. SPK private `37`, conversation slot
  `145`, ephemeral `213`) appear on the wire without a dedicated enum member —
  consumers MUST treat SPI `crypto_key_id` as `uint32` at the application
  boundary and compare against `*_FIRST` / `*_LAST` constants (same pattern for
  all four new bands: secure-channel, voice-ephemeral, conversation, ephemeral).
- **`MobileCommandId` values `101`..`104` (`0x65`..`0x68`):** checked-in
  `hsm_keys.proto` uses **decimal** literals as in the §3 snippet so older
  `protoc` releases never reject hex `0x…` enum initializers. Older command ids
  may remain hex until a normalization pass.
- **New status code `SPI_MOBILE_STATUS_INIT_PIN_THROTTLED (0xF5)`** (master §5.1.1)
  — not carried in any enum in this proto; lives as a C header constant in
  `uHSM-Host/Src/Mobile/Appl/kMobileManager.h` alongside the other
  `SPI_MOBILE_STATUS_*` codes. Apps surface it as a typed Dart/Kotlin error
  (`HsmStatusException.initPinThrottled`) — no proto work here.

### 3.1 Why not put the wire values on `= 0x00, = 0x01`?

proto3 reserves enum value `0` for the `_UNSPECIFIED` sentinel, so we cannot set wire 0x00 directly. The tradeoff:

- **Chosen**: enum value = (wire + 1) with `_UNSPECIFIED = 0`. Forces every consumer to translate via a tiny `wire_to_enum()` helper. Safe against uninitialized reads.
- **Rejected**: enum value = wire (e.g. `PROVISIONING_STATE_PROVISIONING_REQUIRED = 0`). Collides with `_UNSPECIFIED`. Cannot distinguish "field was not set on the wire" from "state = REQUIRED".

The ~10-line helper per consumer (Kotlin, Dart, Rust, Go, Swift) is a one-time cost. Sample Kotlin:

```kotlin
fun ProvisioningState.fromWire(b: Byte): ProvisioningState = when (b.toInt() and 0xFF) {
  0x00 -> ProvisioningState.PROVISIONING_STATE_PROVISIONING_REQUIRED
  0x01 -> ProvisioningState.PROVISIONING_STATE_PROVISIONED
  0x02 -> ProvisioningState.PROVISIONING_STATE_LOCKED_WIPED
  else -> ProvisioningState.PROVISIONING_STATE_UNSPECIFIED
}
```

### 3.2 Why five OPK boundary constants, not twenty?

Firmware owns the band. Consumers only need the four boundary markers (`OPK_PRIV_FIRST`, `OPK_PUB_FIRST`, `OPK_PRIV_LAST`, `OPK_PUB_LAST`) plus the cardinality in `CryptoKeyIdBandSize`. Emitting 20 explicit ids would couple every proto-regen to the OPK-pool size decision, which is a firmware concern only.

### 3.3 `CRYPTO_KEY_ID_MSG_BOOTSTRAP_SECRET = 64`

Parked on the discontinuous id 64 to mirror firmware's slot 64 (`uHSM-HSM/Src/BSW/kHsm/kHsm_Kayten.c` bootstrap-secret NVM mapping). No other consumer needs to read/write it directly, so it's here purely to name it symbolically for `Kayten_WipeAllKeyMaterial` step-2 enumeration (master §6a.2).

### 3.4 Omissions (intentional)

- **No `PinRetryCount` enum.** The wire byte is a bounded counter 0..3, not a discriminant. Keep it as `uint32` in response structs where it's surfaced; no value in enumifying.
- **No `SpiStatusCode` enum** in this file. Status codes live in `kayten-proto` in a **separate file** (`proto/kayten/hsm_keys/v1/spi_status.proto`) **if and when** a consumer asks for them as a proto enum. As of this workstream, uHSM-Host publishes them as a C header, consumers mirror as-needed. Revisit in a follow-up spec.
- **No `WipeScope` enum.** Every wipe is a full-device wipe (master §6a.2). No partial-wipe policy ever enters this enum.

## 3.5 `device.proto` additive field

Edit **`proto/kayten/v1/device.proto`**:

```proto
syntax = "proto3";

package kayten.v1;

option go_package = "github.com/kayten-gmbh/kayten-server/pkg/generated/kayten/v1;kaytenv1";

import "google/protobuf/timestamp.proto";  // NEW

// … (service + other messages unchanged) …

message GetIdentityKeyResponse {
  bytes ed25519_pub = 1;
  bytes ecdh_pub    = 2;
  bytes signature   = 3;

  // NEW — when set, the device has been revoked (PIN-lockout wipe,
  // user-initiated wipe, or admin `RevokeDevice`). Consumers MUST refuse
  // to start a new conversation with this identity and raise a
  // KEY_CHANGE_ALERT-driven re-verification UX. Unset on active devices.
  //
  // Populated by `kayten-server` whenever `devices.revoked_at IS NOT NULL`
  // (see master §6a.4 and the `kayten-server-device-wipe-integration` spec).
  google.protobuf.Timestamp revoked_at = 4;
}
```

Nothing else in `device.proto` changes. Field numbers 1..3 preserved. `google.protobuf.Timestamp` is already established in other files (`common.proto` line 7) so the import style matches.

### 3.5.1 `DeviceService` additive RPCs — capability-based self-revoke (master §9c)

Two additive RPCs on the existing `DeviceService` service in `device.proto`. Closes the post-wipe authentication gap identified in master-spec §6a.3 review: after local wipe deletes the JWT refresh token from Keychain/Keystore, the app's outbox drain needs an auth path that does not depend on live server session state.

```proto
// Add to the existing service DeviceService { … } block in device.proto.
// Order of the RPC block inside the service matters for git diff minimality:
// append after the existing RPCs, do not insert between them.

service DeviceService {
  // ... existing RPCs (RegisterIdentityKey, GetIdentityKey, RevokeDevice, …) ...

  // Auth-gated (Bearer JWT). Called at enrollment success and on every
  // successful LOGIN. Server issues a single-use signed capability JWT
  // scoped to "revoke the caller's device". The app MUST persist the
  // returned capability_jwt in a native outbox companion file OUTSIDE
  // the Keychain/Keystore wipe scope (master §6a.3 / §6a.3.1).
  rpc IssueSelfRevokeCapability(IssueSelfRevokeCapabilityRequest)
      returns (IssueSelfRevokeCapabilityResponse);

  // UNAUTHENTICATED. Called from the app's wipe-revocation-outbox drain
  // (WorkManager / BGAppRefreshTask) after the local wipe has deleted
  // the JWT refresh token. Server validates the capability_jwt
  // signature + expiry + single-use jti; on success runs the standard
  // RevokeDevice path (prekey retirement, inbound queue cleanup,
  // DEVICE_REVOKED / KEY_CHANGE_ALERT fan-out, identity_keys.revoked_at
  // populated). See master §9c for the full server contract.
  rpc RevokeDeviceByCapability(RevokeDeviceByCapabilityRequest)
      returns (RevokeDeviceByCapabilityResponse);
}

message IssueSelfRevokeCapabilityRequest {
  // Intentionally empty — device_id is derived from auth context server-side.
}

message IssueSelfRevokeCapabilityResponse {
  // Compact JWT (HS256) with payload:
  //   { iss: "kayten-server", sub: <device_id>,
  //     aud: "self-revoke", jti: <uuid-v4>,
  //     iat: <unix-seconds>, exp: <iat + 30d> }
  // Signed with server-side `revocation_signing_key` (rotatable).
  string capability_jwt                  = 1;
  google.protobuf.Timestamp expires_at   = 2;
}

message RevokeDeviceByCapabilityRequest {
  string capability_jwt = 1;   // exactly the JWT issued by IssueSelfRevokeCapability
  string reason         = 2;   // free-form: "pin_lockout_wipe" | "user_requested"
                               //            | "silent_wipe_detected" | …
}

message RevokeDeviceByCapabilityResponse {
  // Empty. gRPC status carries outcome:
  //   OK                    — revoke applied, jti marked used
  //   FAILED_PRECONDITION   — jti already used (idempotent success; app clears outbox)
  //   UNAUTHENTICATED       — signature invalid or exp in past
  //   RESOURCE_EXHAUSTED    — rate-limited (3 req/sub/min)
}
```

**Server-side state** (authoritative in `kayten-server-device-wipe-integration-spec.md` — this file only documents the proto contract):
- `revocation_signing_key` — HS256 symmetric secret loaded from the existing server config secret store. Rotatable (old key retained for grace period to validate in-flight tokens).
- Table `revocation_capability_jti(jti UUID PK, device_id TEXT NOT NULL, issued_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL, used_at TIMESTAMPTZ NULL, superseded_at TIMESTAMPTZ NULL)`.
- Unique partial index `WHERE used_at IS NULL AND superseded_at IS NULL` on `(device_id)` so each device has at most one *active* capability at a time; `IssueSelfRevokeCapability` marks prior-active rows `superseded_at = NOW()` before inserting the new row.
- `RevokeDeviceByCapability` flow: verify HS256 signature → verify `exp > now` → verify `aud == "self-revoke"` → `UPDATE … SET used_at = NOW() WHERE jti = $1 AND used_at IS NULL RETURNING device_id` → on 1-row success, run the same `RevokeDevice` internal path → on 0-row, return `FAILED_PRECONDITION`.
- Migration file: `kayten-server/migrations/014_revocation_capability_jti.up.sql` + matching `.down.sql`.

**Rate-limit:** `RevokeDeviceByCapability` capped at ≤ 3 requests per JWT `sub` per minute; IP-based rate-limit layered on top. Rationale: a leaked capability can only revoke once per minute at worst, and exactly once per device-lifecycle (single-use jti).

**Idempotency:** the app drain treats `OK` and `FAILED_PRECONDITION(jti-used)` as equivalent success (clears outbox in both cases). The first capability drain after a wipe may race with the live-JWT `RevokeDevice` path from §6a.3 step 1 — this is expected; second caller gets `FAILED_PRECONDITION`.

### 3.6 Interaction with the parallel OPK/DH4 proto workstream

The earlier `2026-04-16-kayten-proto-opk-nvm-band-and-dh4-spec.md` may already have landed its own constants (e.g. `CAP_OPK_V1 = 0x10`, `STATUS_MSG_KE_OPK_INVALID = 0xE2`, OPK-band boundary markers). Before writing, rebase this branch on top of that merge:

- If `CryptoKeyId.CRYPTO_KEY_ID_OPK_PRIV_FIRST = 70` / `_LAST = 108` and `MobileCommandId.MOBILE_COMMAND_ID_GENERATE_OPK_V1 = 0x5C` are already present in an earlier proto file → delete those duplicates in this new file.
- If the earlier workstream parked them in `kayten.v1` (flat package) → in this workstream either (a) re-export them from `hsm_keys.v1` or (b) accept both and prefer `hsm_keys.v1` in new consumers. Decision deferred to the implementer at merge time; document whichever path in the PR.

## 4. Pre-existing enum conventions — apply to new file

- Prefix every enum value with the full SCREAMING_SNAKE_CASE enum name. Matches `ENROLLMENT_STATUS_*` (attestation.proto), keeps `buf lint STANDARD` + the `DEFAULT` extra rules happy if the team ever opts into them.
- `_UNSPECIFIED = 0` mandatory first value.
- Wire values > 0 noted as inline comments (`// wire 0x00`).
- Package name ends in `.v1` — matches the existing `kayten.v1` convention.
- `option go_package = …` uses the `github.com/kayten-gmbh/...` prefix identical to the existing files, with the `;hsmkeysv1` short package alias.
- No `option java_package` or `option csharp_namespace` — the existing repo doesn't set them, keep noise out.

## 5. Consumer translation helpers (reference)

Each downstream spec will include a language-appropriate wire<->enum translator. This spec publishes the canonical **contract** for that translator so all five consumers do the same thing:

```
ProvisioningState:
  wire 0x00 <-> PROVISIONING_STATE_PROVISIONING_REQUIRED
  wire 0x01 <-> PROVISIONING_STATE_PROVISIONED
  wire 0x02 <-> PROVISIONING_STATE_LOCKED_WIPED
  (any other wire byte) -> PROVISIONING_STATE_UNSPECIFIED  (error path)

WipeReason:
  wire 0x00 <-> WIPE_REASON_NONE
  wire 0x01 <-> WIPE_REASON_PIN_LOCKOUT
  wire 0x02 <-> WIPE_REASON_USER_REQUEST
  wire 0x03 <-> WIPE_REASON_ATTESTATION_FAILED
  wire 0x04 <-> WIPE_REASON_PIN_LOCKOUT_RECOVERY
  (any other wire byte) -> WIPE_REASON_UNSPECIFIED  (error path)

MobileCommandId:
  wire byte N <-> MOBILE_COMMAND_ID_<name> where numeric value == N
  (any unmapped byte) -> return SPI_STATUS_CMD_NOT_AVAILABLE (0xE0) without
                         attempting to look up a handler

CryptoKeyId:
  wire u32 N <-> CRYPTO_KEY_ID_<name> where numeric value == N, or
                 CRYPTO_KEY_ID_UNSPECIFIED if N doesn't match a declared
                 boundary (firmware still accepts the numeric id — the enum
                 is purely symbolic for id-**boundary** constants)
```

Point (4) is worth calling out: `CryptoKeyId` is a **sparse** enum of band markers, not a dense catalog of every valid id. A runtime call with `crypto_key_id = 37` (second-SPK-private), `crypto_key_id = 145` (conversation slot), or `crypto_key_id = 213` (ephemeral) is perfectly valid; it just isn't a declared enum symbol — consumers check it against `FIRST..LAST` bounds for the matching band, not against enum membership.

### 5.1 Checked-in translator reference implementations (master §9d)

To prevent the 5 consumer languages from drifting, this repo ships canonical translators under `references/translators/`. Each file ≤ 40 lines, mechanical mapping only, no business logic. **All five are mandatory in this PR** — `kayten-app-v2` (full refactor, no-backwards-compat) regenerates Rust and Swift bindings as part of its own workstream, so the reference files must be available at the same SHA the v2 workstream bumps to.

- `references/translators/translator.dart` — consumed by `kayten-app` and `kayten-app-v2` Dart surface.
- `references/translators/translator.kt` — consumed by both apps' Kotlin `HsmPlugin`.
- `references/translators/translator.go` — consumed by `kayten-server`.
- `references/translators/translator.rs` — consumed by `kayten-app-v2` rust-core.
- `references/translators/translator.swift` — consumed by `kayten-app-v2` iOS bridge.

Each file MUST implement four functions (names in the host language's idiomatic casing):
- `provisioningStateFromWire(uint8) → ProvisioningState` (unknown wire → `UNSPECIFIED`)
- `provisioningStateToWire(ProvisioningState) → uint8` (`UNSPECIFIED` → `0xFF` sentinel)
- `wipeReasonFromWire(uint8) → WipeReason` (unknown wire → `UNSPECIFIED`)
- `wipeReasonToWire(WipeReason) → uint8` (`UNSPECIFIED` → `0xFF` sentinel)

Reference Dart (`translator.dart`):

```dart
// kayten-proto — references/translators/translator.dart
// Mechanical wire↔enum translator. proto3 forces _UNSPECIFIED = 0, so enum
// numeric values are (wire + 1). This file is the single source of truth for
// that mapping — copy into consumer repos, do not re-implement.
import 'package:kayten_proto/hsm_keys/v1/hsm_keys.pb.dart';

ProvisioningState provisioningStateFromWire(int wire) {
  switch (wire & 0xFF) {
    case 0x00: return ProvisioningState.PROVISIONING_STATE_PROVISIONING_REQUIRED;
    case 0x01: return ProvisioningState.PROVISIONING_STATE_PROVISIONED;
    case 0x02: return ProvisioningState.PROVISIONING_STATE_LOCKED_WIPED;
    default:   return ProvisioningState.PROVISIONING_STATE_UNSPECIFIED;
  }
}

int provisioningStateToWire(ProvisioningState s) {
  switch (s) {
    case ProvisioningState.PROVISIONING_STATE_PROVISIONING_REQUIRED: return 0x00;
    case ProvisioningState.PROVISIONING_STATE_PROVISIONED:           return 0x01;
    case ProvisioningState.PROVISIONING_STATE_LOCKED_WIPED:          return 0x02;
    default:                                                         return 0xFF;
  }
}

WipeReason wipeReasonFromWire(int wire) {
  switch (wire & 0xFF) {
    case 0x00: return WipeReason.WIPE_REASON_NONE;
    case 0x01: return WipeReason.WIPE_REASON_PIN_LOCKOUT;
    case 0x02: return WipeReason.WIPE_REASON_USER_REQUEST;
    case 0x03: return WipeReason.WIPE_REASON_ATTESTATION_FAILED;
    case 0x04: return WipeReason.WIPE_REASON_PIN_LOCKOUT_RECOVERY;
    default:   return WipeReason.WIPE_REASON_UNSPECIFIED;
  }
}

int wipeReasonToWire(WipeReason r) {
  switch (r) {
    case WipeReason.WIPE_REASON_NONE:                 return 0x00;
    case WipeReason.WIPE_REASON_PIN_LOCKOUT:          return 0x01;
    case WipeReason.WIPE_REASON_USER_REQUEST:         return 0x02;
    case WipeReason.WIPE_REASON_ATTESTATION_FAILED:   return 0x03;
    case WipeReason.WIPE_REASON_PIN_LOCKOUT_RECOVERY: return 0x04;
    default:                                          return 0xFF;
  }
}
```

Reference Kotlin (`translator.kt`):

```kotlin
// kayten-proto — references/translators/translator.kt
package com.kayten.proto.hsm_keys.v1

import kayten.hsm_keys.v1.HsmKeys.ProvisioningState
import kayten.hsm_keys.v1.HsmKeys.WipeReason

fun provisioningStateFromWire(wire: Byte): ProvisioningState = when (wire.toInt() and 0xFF) {
  0x00 -> ProvisioningState.PROVISIONING_STATE_PROVISIONING_REQUIRED
  0x01 -> ProvisioningState.PROVISIONING_STATE_PROVISIONED
  0x02 -> ProvisioningState.PROVISIONING_STATE_LOCKED_WIPED
  else -> ProvisioningState.PROVISIONING_STATE_UNSPECIFIED
}

fun provisioningStateToWire(s: ProvisioningState): Int = when (s) {
  ProvisioningState.PROVISIONING_STATE_PROVISIONING_REQUIRED -> 0x00
  ProvisioningState.PROVISIONING_STATE_PROVISIONED           -> 0x01
  ProvisioningState.PROVISIONING_STATE_LOCKED_WIPED          -> 0x02
  else                                                        -> 0xFF
}

fun wipeReasonFromWire(wire: Byte): WipeReason = when (wire.toInt() and 0xFF) {
  0x00 -> WipeReason.WIPE_REASON_NONE
  0x01 -> WipeReason.WIPE_REASON_PIN_LOCKOUT
  0x02 -> WipeReason.WIPE_REASON_USER_REQUEST
  0x03 -> WipeReason.WIPE_REASON_ATTESTATION_FAILED
  0x04 -> WipeReason.WIPE_REASON_PIN_LOCKOUT_RECOVERY
  else -> WipeReason.WIPE_REASON_UNSPECIFIED
}

fun wipeReasonToWire(r: WipeReason): Int = when (r) {
  WipeReason.WIPE_REASON_NONE                 -> 0x00
  WipeReason.WIPE_REASON_PIN_LOCKOUT          -> 0x01
  WipeReason.WIPE_REASON_USER_REQUEST         -> 0x02
  WipeReason.WIPE_REASON_ATTESTATION_FAILED   -> 0x03
  WipeReason.WIPE_REASON_PIN_LOCKOUT_RECOVERY -> 0x04
  else                                         -> 0xFF
}
```

Reference Go (`translator.go`):

```go
// kayten-proto — references/translators/translator.go
package translators

import (
    hsmkeysv1 "github.com/kayten-gmbh/kayten-server/pkg/generated/kayten/hsm_keys/v1"
)

func ProvisioningStateFromWire(w byte) hsmkeysv1.ProvisioningState {
    switch w {
    case 0x00: return hsmkeysv1.ProvisioningState_PROVISIONING_STATE_PROVISIONING_REQUIRED
    case 0x01: return hsmkeysv1.ProvisioningState_PROVISIONING_STATE_PROVISIONED
    case 0x02: return hsmkeysv1.ProvisioningState_PROVISIONING_STATE_LOCKED_WIPED
    default:   return hsmkeysv1.ProvisioningState_PROVISIONING_STATE_UNSPECIFIED
    }
}

func ProvisioningStateToWire(s hsmkeysv1.ProvisioningState) byte {
    switch s {
    case hsmkeysv1.ProvisioningState_PROVISIONING_STATE_PROVISIONING_REQUIRED: return 0x00
    case hsmkeysv1.ProvisioningState_PROVISIONING_STATE_PROVISIONED:           return 0x01
    case hsmkeysv1.ProvisioningState_PROVISIONING_STATE_LOCKED_WIPED:          return 0x02
    default:                                                                    return 0xFF
    }
}

func WipeReasonFromWire(w byte) hsmkeysv1.WipeReason {
    switch w {
    case 0x00: return hsmkeysv1.WipeReason_WIPE_REASON_NONE
    case 0x01: return hsmkeysv1.WipeReason_WIPE_REASON_PIN_LOCKOUT
    case 0x02: return hsmkeysv1.WipeReason_WIPE_REASON_USER_REQUEST
    case 0x03: return hsmkeysv1.WipeReason_WIPE_REASON_ATTESTATION_FAILED
    case 0x04: return hsmkeysv1.WipeReason_WIPE_REASON_PIN_LOCKOUT_RECOVERY
    default:   return hsmkeysv1.WipeReason_WIPE_REASON_UNSPECIFIED
    }
}

func WipeReasonToWire(r hsmkeysv1.WipeReason) byte {
    switch r {
    case hsmkeysv1.WipeReason_WIPE_REASON_NONE:                 return 0x00
    case hsmkeysv1.WipeReason_WIPE_REASON_PIN_LOCKOUT:          return 0x01
    case hsmkeysv1.WipeReason_WIPE_REASON_USER_REQUEST:         return 0x02
    case hsmkeysv1.WipeReason_WIPE_REASON_ATTESTATION_FAILED:   return 0x03
    case hsmkeysv1.WipeReason_WIPE_REASON_PIN_LOCKOUT_RECOVERY: return 0x04
    default:                                                     return 0xFF
    }
}
```

Reference Rust (`translator.rs`):

```rust
// kayten-proto — references/translators/translator.rs
// Mechanical wire↔enum translator. proto3 forces _UNSPECIFIED = 0, so enum
// numeric values are (wire + 1). Single source of truth for the mapping —
// copy into consumer repos, do not re-implement.
//
// Consumer note: `kayten_proto` crate comes from prost-generated output of
// kayten.hsm_keys.v1.hsm_keys.proto. Variant names follow prost defaults.

use kayten_proto::hsm_keys::v1::{ProvisioningState, WipeReason};

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

Reference Swift (`translator.swift`):

```swift
// kayten-proto — references/translators/translator.swift
// Mechanical wire↔enum translator. proto3 forces _UNSPECIFIED = 0, so enum
// numeric values are (wire + 1). Single source of truth for the mapping —
// copy into consumer repos, do not re-implement.
//
// Assumes swift-protobuf-generated ProvisioningState / WipeReason enums
// from kayten.hsm_keys.v1.hsm_keys.proto.
import Foundation

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

Buf / lint impact: none. These are non-proto files in a sibling directory; `buf lint` scans only `.proto` files inside `proto/`. Consumers pick these up by copying the file contents into their own tree (not submodule-referenced) so build systems don't have cross-repo language-toolchain dependencies.

## 6. Code generation

### 6.1 Go + Dart — generated in this repo via `buf generate`

`buf.gen.yaml` already configures:

```
plugins:
  - remote: buf.build/protocolbuffers/go   → gen/go
  - remote: buf.build/grpc/go              → gen/go
  - local:  protoc-gen-dart                → gen/dart  (opt: grpc)
```

After adding the two files, run:

```
cd kayten-proto
buf lint
buf breaking --against '.git#branch=develop'
buf generate
git status gen/
```

Expected new files under `gen/go/`:
- `gen/go/kayten/hsm_keys/v1/hsm_keys.pb.go` (plus gRPC pair if any — there are no services, so only `.pb.go`)

Expected new files under `gen/dart/`:
- `gen/dart/kayten/hsm_keys/v1/hsm_keys.pb.dart`
- `gen/dart/kayten/hsm_keys/v1/hsm_keys.pbenum.dart`
- `gen/dart/kayten/hsm_keys/v1/hsm_keys.pbjson.dart`
- `gen/dart/kayten/hsm_keys/v1/hsm_keys.pbserver.dart` (empty; Dart generator always emits this)

Expected changes to existing generated files:
- `gen/go/kayten/v1/device.pb.go` — `GetIdentityKeyResponse` gains `RevokedAt *timestamppb.Timestamp` field.
- `gen/dart/kayten/v1/device.pb.dart` — `GetIdentityKeyResponse` gains a `revoked_at` getter/setter.

Commit `gen/` additions with the source change in the same PR.

### 6.2 Rust / Kotlin / Swift — consumer repos regenerate

These bindings are not produced by `kayten-proto`'s `buf.gen.yaml`. Consumer repos generate them locally:

- **kayten-app-v2** (`rust-core/`): `prost-build` called from `build.rs`, or regenerate via `cargo xtask gen-proto`. Will need a new Rust module declaration `pub mod hsm_keys_v1;` in the generated tree root.
- **kayten-app-v2 Android** (`android/app/src/main/java/com/kayten/.../generated/`): regenerate from the Rust/Swift wrapping path used by the app-v2 build pipeline. If the app-v2 regen tool reads `buf.gen.yaml`, the Kotlin generator must be added there (out of scope for this workstream — see the kayten-app-v2 per-repo spec §3 of that sibling).
- **kayten-app-v2 iOS** (`ios/Kayten/.../Generated/`): SwiftProtobuf via swift-protobuf generator; regenerate via the iOS build script.
- **kayten-app v1**: already uses the `gen/dart` tree checked into the submodule. No extra regen step in v1.
- **kayten-server**: uses `gen/go`. Same — no extra step.

Call-out: the sibling specs for kayten-app-v2 and kayten-app MUST regenerate their checked-in bindings as part of their own PRs, **after** this proto PR merges. That bump is tracked in §7.

## 7. Submodule bump procedure

Because `kayten-app` and `kayten-server` both consume `kayten-proto` as a git submodule at `api/proto`, landing this change is a **three-commit dance**:

1. **Commit 1 — proto repo** (`kayten-proto` itself):
   - Branch off `develop`: `feat/2026-04-17-hsm-keys-v1-constants`.
   - Add the two files (`hsm_keys.proto`, `device.proto` edit) + regenerate `gen/`.
   - `buf lint && buf breaking --against '.git#branch=develop' && buf generate`.
   - PR → merge to `develop`.

2. **Commit 2 — kayten-server**:
   - Branch off `develop`: `feat/2026-04-17-proto-hsm-keys-bump`.
   - `git submodule update --remote api/proto` (bumps to the newly-merged `develop` SHA).
   - Regenerate Go stubs if the server has its own regen step; otherwise consume `gen/go/` from the submodule directly.
   - Commit the submodule bump and any generated-file diffs.
   - PR → merge. No logic changes in this commit; the server work that uses `revoked_at` comes later in master §8 week 1 (see the `kayten-server-device-wipe-integration` per-repo spec).

3. **Commit 3 — kayten-app**:
   - Branch off `develop`: `feat/2026-04-17-proto-hsm-keys-bump`.
   - Bump the submodule to the same `develop` SHA as commit 2.
   - Regenerate Dart/Kotlin bindings if the app has a separate regen step; commit those diffs.
   - PR → merge. Again, no logic changes consuming the new symbols in this PR — the kayten-app consumer work lives in master §8 week 2.

This ordering guarantees that kayten-app and kayten-server pin to the same proto SHA and that no downstream spec ever has to ask "which version of hsm_keys.v1?".

## 8. Verification

All checks must pass before merging the proto-repo PR:

```
cd kayten-proto
buf lint                                            # zero warnings
buf breaking --against '.git#branch=develop'         # zero breaking changes
buf generate                                         # exit 0
git status gen/                                      # new files present, no unexpected deletions
git diff proto/ | wc -l                              # <= ~220 lines (this spec's enum tables + device.proto add)
```

Functional checks (post-regen, before submodule bumps):

- `grep -R "hsm_keys.v1" gen/go/`  → at least the `kayten/hsm_keys/v1/hsm_keys.pb.go` file present.
- `grep -R "hsm_keys" gen/dart/`   → corresponding Dart outputs present.
- `grep "revoked_at" gen/go/kayten/v1/device.pb.go` → one hit in `GetIdentityKeyResponse`.
- `grep "revoked_at" gen/dart/kayten/v1/device.pb.dart` → one hit.
- `grep "IssueSelfRevokeCapability" gen/go/kayten/v1/device_grpc.pb.go` → 2+ hits (client + server stubs).
- `grep "RevokeDeviceByCapability" gen/go/kayten/v1/device_grpc.pb.go` → 2+ hits.
- `grep "CRYPTO_KEY_ID_SECURE_CHANNEL_FIRST\|CRYPTO_KEY_ID_CONVERSATION_FIRST\|CRYPTO_KEY_ID_EPHEMERAL_FIRST" gen/go/kayten/hsm_keys/v1/hsm_keys.pb.go` → 3 hits.
- `ls references/translators/` → contains `translator.dart`, `translator.kt`, `translator.go` (optionally `.rs`, `.swift`).

Cross-repo smoke (before bumping kayten-app / kayten-server):

```
# From kayten-server (with proto submodule bumped to the feature branch SHA):
cd kayten-server
go build ./...            # must build — revoked_at is optional timestamppb.Timestamp
```

```
# From kayten-app (with proto submodule bumped):
cd kayten-app
flutter pub get
flutter analyze           # zero new errors
```

Note that at this point **no code consumes** `hsm_keys.v1` symbols, by design — we're publishing the constants so consumer specs (uHSM-HSM, uHSM-Host, kayten-app, kayten-app-v2, kayten-server) can land in parallel over weeks 1–3 of the master rollout.

## 9. Commit message template

```
feat(hsm_keys): add kayten.hsm_keys.v1 constants package + GetIdentityKeyResponse.revoked_at

New file `proto/kayten/hsm_keys/v1/hsm_keys.proto` defining the canonical
`CryptoKeyId`, `MobileCommandId`, `ProvisioningState`, `WipeReason`, and
`CryptoKeyIdBandSize` enums used by every consumer of the mobile SPI
surface (uHSM-Host, uHSM-HSM, kayten-app, kayten-app-v2, kayten-server).

Adds `google.protobuf.Timestamp revoked_at = 4` to `GetIdentityKeyResponse`
in `kayten/v1/device.proto` so partners can distinguish "device revoked"
from "device not found" without polling attestation.

No wire-format changes to any existing message. `buf lint` + `buf breaking
--against develop` both green. Generated `gen/go/` + `gen/dart/` checked in.

Unblocks the PKCS#11-retirement fan-out specs landing in master weeks 1–3
(see docs/superpowers/plans/2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md).

Refs #<issue-id> (master workstream)
```

## 10. Out of scope / follow-ups

- **No `SpiStatusCode` proto.** Status codes stay as a C header in `uHSM-Host` until a concrete consumer asks for a generated Dart/Kotlin/Rust mirror. If asked for later, follow-up spec `2026-04-??-kayten-proto-spi-status-codes-v1.md`.
- **No RPC additions.** `DeviceService.RevokeDevice` already exists; it gains logic changes in `kayten-server` but no wire changes. No new RPCs added to `DeviceService` in this workstream.
- **No `TLV` proto.** The TLV envelope parsers in host/firmware stay hand-rolled C for now. An eventual proto-ification is tracked in master §13 follow-ups.
- **No enum renumber of the shared `kayten.v1.EnvelopeType`.** Pre-existing `SEND_MESSAGE = 1` (unprefixed) stays. Migrating it to `ENVELOPE_TYPE_SEND_MESSAGE` would be a breaking-codegen-name change on all five consumers; not worth it for this workstream.
- **No changes to `kayten-proto/buf.yaml` or `buf.gen.yaml`.** Intentional — we want consumers to get new files via the existing pipeline without touching infrastructure.
