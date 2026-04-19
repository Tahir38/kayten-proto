# kayten-proto — `hsm_keys.v1` constants + `GetIdentityKeyResponse.revoked_at` + self-revoke capability RPCs — AI prompt

- **Date:** 2026-04-17
- **Repo scope:** `/Users/tahir/Repos/kayten-proto` (git submodule, pinned at `api/proto` in both `kayten-app` and `kayten-server`).
- **Authoritative repo spec:** [`2026-04-17-kayten-proto-hsm-keys-constants-spec.md`](2026-04-17-kayten-proto-hsm-keys-constants-spec.md)
- **Parent spec:** [`2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`](2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md)
- **Sibling fan-out specs (land after this one):**
  - `2026-04-17-uhsm-hsm-mobile-profile-v2-spec.md` + AI prompt
  - `2026-04-17-uhsm-host-kmobile-manager-spec.md` + AI prompt
  - `2026-04-17-kayten-app-pkcs11-retirement-spec.md` + AI prompt
  - `2026-04-17-kayten-app-v2-pkcs11-retirement-spec.md` + AI prompt
  - `2026-04-17-kayten-server-device-wipe-integration-spec.md` + AI prompt

---

```text
You are Claude (Opus or Sonnet) operating inside the `kayten-proto` repository
at `/Users/tahir/Repos/kayten-proto`. This repo is the shared Protobuf
definition tree for the Kayten system. It is consumed as a git submodule by
`kayten-app`, `kayten-app-v2`, and `kayten-server`, and is read directly by
`uHSM-Host` / `uHSM-HSM` C code for symbol reference.

Your task is narrow, fully additive, and unblocks every other per-repo
workstream in the PKCS#11-retirement fan-out. You publish four things so
every other repo can stop duplicating them:

  1. A new file `proto/kayten/hsm_keys/v1/hsm_keys.proto` with the enums
     `CryptoKeyId` (INCLUDING four new band pairs: secure-channel 110..117,
     voice-ephemeral 118..129, conversation 130..191, ephemeral 192..239),
     `CryptoKeyIdBandSize` (INCLUDING four new `*_SLOTS` values),
     `MobileCommandId` (through `0x68 REQUEST_WIPE_CHALLENGE_V1`),
     `ProvisioningState`, and `WipeReason`.

  2. Exactly one additive field on an existing message:
     `google.protobuf.Timestamp revoked_at = 4;` appended to
     `GetIdentityKeyResponse` in `proto/kayten/v1/device.proto`
     (adding `import "google/protobuf/timestamp.proto";` to that file).

  3. Two additive RPCs on the existing `DeviceService` in `device.proto`:
     - `IssueSelfRevokeCapability` (auth-gated; returns a server-signed JWT
        scoped `aud = "self-revoke"`)
     - `RevokeDeviceByCapability` (unauthenticated; validates the JWT and
        runs the standard revocation path server-side)
     plus the four new messages these RPCs reference.

  4. FIVE checked-in translator reference implementations under
     `references/translators/` — `translator.dart`, `translator.kt`,
     `translator.go`, `translator.rs`, `translator.swift` — implementing
     `provisioningStateFromWire/toWire` and `wipeReasonFromWire/toWire`
     mechanical mappings. All five are MANDATORY in this PR (product
     direction: kayten-app-v2 regenerates Rust + Swift bindings as part
     of its own workstream and needs the reference files at the same
     proto SHA it bumps to).

You MUST NOT:
- remove, rename, or renumber any existing message field
- change any existing enum value
- add any new RPC beyond the two capability RPCs
- add new status-code enums (status codes stay as a C header in uHSM-Host)
- touch `buf.yaml` or `buf.gen.yaml`
- bump the submodule in the consumer repos (those are separate PRs — see
  repo spec §7)

Follow `2026-04-17-kayten-proto-hsm-keys-constants-spec.md` verbatim. Where
master spec §9 and the per-repo spec §3 disagree, the per-repo spec wins —
it applies enum-value prefixing and `_UNSPECIFIED = 0` proto3-compliance.

======================================================================
MANDATORY READING (in order)
======================================================================

1. `docs/superpowers/plans/2026-04-17-kayten-proto-hsm-keys-constants-spec.md`
   — authoritative spec. Pay attention to §3 (enum bodies with new bands),
     §3.5.1 (new RPCs and their wire contracts), §5.1 (translator bodies
     to copy verbatim), §8 (verification grep commands).

2. `docs/superpowers/plans/2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`
   — parent spec. Read §2.6, §2.7, §3.1.1 (PIN-protection rules),
     §5.1.1 (init-pin throttle — surfaces as C header `0xF5`, not proto),
     §6a.3, §6a.3.1 (capability token lifecycle), §9, §9a, §9b (new
     slot bands), §9c (capability RPC contract), §9d (translators).

3. Existing proto repo layout + conventions:
   - `proto/kayten/v1/attestation.proto` — see `EnrollmentStatus` for the
     strict-prefix convention you MUST copy.
   - `proto/kayten/v1/common.proto` — see `google.protobuf.Timestamp`
     import style.
   - `proto/kayten/v1/device.proto` — the file you are editing. Fields
     1..3 of `GetIdentityKeyResponse` MUST stay unchanged. Existing RPCs
     on `DeviceService` MUST stay unchanged.
   - `buf.yaml` / `buf.gen.yaml` — DO NOT edit.

4. Sibling OPK/DH4 proto workstream that MUST be merged first:
   `docs/superpowers/plans/2026-04-16-kayten-proto-opk-nvm-band-and-dh4-spec.md`
   If it already ships `GENERATE_OPK_V1 = 0x5C` or OPK band boundaries in
   a different file/enum, de-duplicate during your write (per-repo spec §3.6).

======================================================================
STEP-BY-STEP TASK LIST
======================================================================

Task 1 — Branch
  Create branch `feat/2026-04-17-hsm-keys-v1-constants` off `develop`.

Task 2 — New file `proto/kayten/hsm_keys/v1/hsm_keys.proto`
  Copy the full file body from per-repo spec §3 verbatim. Key invariants:

  a) `syntax = "proto3"; package kayten.hsm_keys.v1;`
  b) `option go_package = "github.com/kayten-gmbh/kayten-server/pkg/generated/kayten/hsm_keys/v1;hsmkeysv1";`
  c) Five enums in order: `CryptoKeyId`, `CryptoKeyIdBandSize`,
     `MobileCommandId`, `ProvisioningState`, `WipeReason`.
  d) Every enum value MUST be prefixed with its full SCREAMING_SNAKE_CASE
     enum name. Master spec §9 uses unprefixed names — per-repo spec §4
     overrides that.
  e) Every enum MUST declare `<NAME>_UNSPECIFIED = 0` as its first value.
  f) `CryptoKeyId` MUST include:
     - existing bands: identity (22/23), SPK (35..44), msg bootstrap (64),
       OPK (70..109)
     - **FOUR NEW BANDS (per-repo spec §3, master §9b):**
         secure-channel   110..117
         voice-ephemeral  118..129
         conversation     130..191
         ephemeral        192..239
     Only publish `*_FIRST` / `*_LAST` boundary markers per band — do NOT
     enumerate every individual id.
  g) `CryptoKeyIdBandSize` MUST include the four new `*_SLOTS` values:
     SECURE_CHANNEL_SLOTS = 8, VOICE_EPHEMERAL_SLOTS = 12,
     CONVERSATION_SLOTS = 62, EPHEMERAL_SLOTS = 48 (per-repo spec §3).
  h) `MobileCommandId` includes EVERY mobile SPI command id through
     **`0x68 REQUEST_WIPE_CHALLENGE_V1`**. Wire bytes MUST match master
     tables §3.1 + §3.2. For `0x65`..`0x68` use **decimal** literals
     `101`..`104` with `// 0x65` style inline comments (per-repo §3.0.1).
     Include `DEV_SET_INIT_PIN_V1 = 103` with a comment noting DEV-only.
  i) For `ProvisioningState` and `WipeReason`, note the wire-vs-enum
     numeric divergence inline via `// wire 0xNN` comments. proto3 forces
     `_UNSPECIFIED = 0`, so enum numeric values are `wire + 1`.

Task 3 — Edit `proto/kayten/v1/device.proto`
  FOUR additive changes, nothing else:

  a) Add (near the top, below the existing `option go_package`):
       import "google/protobuf/timestamp.proto";
  b) Append field 4 to `GetIdentityKeyResponse`:
       // Populated by kayten-server whenever devices.revoked_at
       // IS NOT NULL. See master spec §6a.4.
       google.protobuf.Timestamp revoked_at = 4;
  c) Append two RPCs to the existing `service DeviceService { … }` block
     (after the existing RPCs — do NOT insert between them):

       rpc IssueSelfRevokeCapability(IssueSelfRevokeCapabilityRequest)
           returns (IssueSelfRevokeCapabilityResponse);
       rpc RevokeDeviceByCapability(RevokeDeviceByCapabilityRequest)
           returns (RevokeDeviceByCapabilityResponse);

  d) Append four new messages at the bottom of `device.proto` (NOT inside
     any existing message). Copy bodies from per-repo spec §3.5.1 verbatim:
       - IssueSelfRevokeCapabilityRequest   (empty)
       - IssueSelfRevokeCapabilityResponse  (capability_jwt:string, expires_at:Timestamp)
       - RevokeDeviceByCapabilityRequest    (capability_jwt:string, reason:string)
       - RevokeDeviceByCapabilityResponse   (empty)

  DO NOT touch fields 1..3 of `GetIdentityKeyResponse`. DO NOT touch any
  other existing message or existing RPC.

Task 4 — Translator reference implementations (ALL FIVE MANDATORY)
  Create directory `references/translators/` and add five files:
    - references/translators/translator.dart
    - references/translators/translator.kt
    - references/translators/translator.go
    - references/translators/translator.rs
    - references/translators/translator.swift
  Copy body verbatim from per-repo spec §5.1. Each file implements
  `provisioningStateFromWire/toWire` + `wipeReasonFromWire/toWire`
  (mechanical mappings, no business logic). Include round-trip unit
  tests where the language supports them inline (Rust `#[cfg(test)]`,
  Swift XCTest stub, optional in Go/Kotlin/Dart).
  The `translator.rs` and `translator.swift` files target the Rust /
  Swift proto bindings that `kayten-app-v2` regenerates in its own
  workstream — they compile against those bindings, not against
  anything in this repo's `gen/` tree. That's OK; they're reference
  source, not built artifacts.

Task 5 — Buf checks
  Run inside `kayten-proto` root:
    buf lint
    buf breaking --against '.git#branch=develop'
  Both MUST exit 0 with zero output.
  - `buf lint` failures likely mean you violated enum-prefix (Task 2d) or
    mis-ordered `_UNSPECIFIED` (Task 2e).
  - `buf breaking` failures mean you modified an existing field (Task 3) —
    revert and redo as append-only.

Task 6 — Regenerate
  Run: `buf generate`
  Expected new files (check in):
    gen/go/kayten/hsm_keys/v1/hsm_keys.pb.go
    gen/dart/kayten/hsm_keys/v1/hsm_keys.pb.dart
    gen/dart/kayten/hsm_keys/v1/hsm_keys.pbenum.dart
    gen/dart/kayten/hsm_keys/v1/hsm_keys.pbjson.dart
    gen/dart/kayten/hsm_keys/v1/hsm_keys.pbserver.dart
  Expected diffs to existing generated files:
    gen/go/kayten/v1/device.pb.go      — RevokedAt field; new request/response
                                         messages; new client+server RPC stubs
    gen/go/kayten/v1/device_grpc.pb.go — IssueSelfRevokeCapability +
                                         RevokeDeviceByCapability stubs
    gen/dart/kayten/v1/device.pb.dart  — revoked_at accessor; new messages
    gen/dart/kayten/v1/device.pbgrpc.dart — new client methods
  Commit the regen output alongside the proto edits.

Task 7 — No consumer bumps
  DO NOT bump the submodule in `kayten-app`, `kayten-app-v2`, or
  `kayten-server` in this PR. Those are separate PRs per per-repo spec §7.

Task 8 — Self-verification (run these greps; ALL must match)
    # 1. Package present in generated Go.
    grep -c "package hsmkeysv1" gen/go/kayten/hsm_keys/v1/hsm_keys.pb.go
    # => at least 1

    # 2. RevokedAt field appears in exactly one place in Go.
    grep -c "RevokedAt.*\*timestamppb.Timestamp" gen/go/kayten/v1/device.pb.go
    # => 1

    # 3. Each new enum has an UNSPECIFIED sentinel.
    grep -c "_UNSPECIFIED = 0;" proto/kayten/hsm_keys/v1/hsm_keys.proto
    # => 5

    # 4. No unprefixed master-spec names leaked in.
    grep -E "^\s+(IDENTITY_PRIV|IDENTITY_PUB|SPK_PRIV_FIRST)\s+=" \
      proto/kayten/hsm_keys/v1/hsm_keys.proto
    # => zero hits

    # 5. New bands present.
    grep -E "CRYPTO_KEY_ID_SECURE_CHANNEL_FIRST\s+=\s+110" \
      proto/kayten/hsm_keys/v1/hsm_keys.proto
    grep -E "CRYPTO_KEY_ID_VOICE_EPHEMERAL_FIRST\s+=\s+118" \
      proto/kayten/hsm_keys/v1/hsm_keys.proto
    grep -E "CRYPTO_KEY_ID_CONVERSATION_FIRST\s+=\s+130" \
      proto/kayten/hsm_keys/v1/hsm_keys.proto
    grep -E "CRYPTO_KEY_ID_EPHEMERAL_FIRST\s+=\s+192" \
      proto/kayten/hsm_keys/v1/hsm_keys.proto
    # => each exactly one hit

    # 6. Wipe-command decimal literals match wire bytes.
    grep -E "MOBILE_COMMAND_ID_USER_INITIATED_WIPE_V1\s+=\s+101" \
      proto/kayten/hsm_keys/v1/hsm_keys.proto
    grep -E "MOBILE_COMMAND_ID_REQUEST_WIPE_CHALLENGE_V1\s+=\s+104" \
      proto/kayten/hsm_keys/v1/hsm_keys.proto
    # => each exactly one hit

    # 7. Capability RPCs wired up.
    grep -c "rpc IssueSelfRevokeCapability" proto/kayten/v1/device.proto
    grep -c "rpc RevokeDeviceByCapability" proto/kayten/v1/device.proto
    # => 1 each

    # 8. Translator refs exist — all five mandatory.
    ls references/translators/translator.dart \
       references/translators/translator.kt \
       references/translators/translator.go \
       references/translators/translator.rs \
       references/translators/translator.swift
    # => all five files exist

    # 9. Buf green.
    buf lint && buf breaking --against '.git#branch=develop' && echo OK
    # => prints OK

Task 9 — PR message
  Use the commit message template from per-repo spec §9 as the PR
  description top block. Mention:
  - Link to per-repo spec file path.
  - Link to master spec file path.
  - One-liner: "Unblocks PKCS#11-retirement fan-out master §8 weeks 1-3
    + closes post-wipe revocation-auth gap (master §6a.3 / §9c)."
  DO NOT claim "all consumers regenerated" — consumers regenerate in
  their own subsequent PRs.

======================================================================
GUARDRAILS
======================================================================

- Additive-only across existing protos. Fail fast on any buf-breaking warning.
- Prefixed enum values. Master §9 has unprefixed names; per-repo §3 overrides.
- `_UNSPECIFIED = 0` first in every enum.
- Four NEW CryptoKeyId band pairs (secure-channel, voice-ephemeral,
  conversation, ephemeral) — do not omit them.
- Four NEW CryptoKeyIdBandSize `*_SLOTS` values — do not omit them.
- Two NEW RPCs on DeviceService — do not invent any third RPC.
- Do NOT generate a `SpiStatusCode` enum. Per-repo spec §10 defers it.
- Do NOT modify `buf.yaml` or `buf.gen.yaml`.
- Do NOT bump submodule pins in consumer repos.
- Do NOT delete anything.

======================================================================
WHEN DONE, RETURN
======================================================================

- Branch name and head SHA.
- Output of `buf lint`, `buf breaking --against '.git#branch=develop'`,
  `buf generate` (all must exit 0).
- `ls -la gen/go/kayten/hsm_keys/` + `ls -la gen/dart/kayten/hsm_keys/`
  + `ls references/translators/` so reviewer confirms artifacts.
- Line-count of committed diff (proto + gen + translators).
- Confirmation that no consumer repo (`kayten-app`, `kayten-app-v2`,
  `kayten-server`) was touched by your PR.
- Any risks you noticed that the spec did not cover.
```
