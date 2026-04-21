# CAP_WIRE_IDENTITY_V1 — Cross-Repo Spec (2026-04-20)

## Motivation

The 2026-04-20 sender/receiver log pair (`kayten-app/logs/sender_device_first_message.txt` + `kayten-app/logs/receiver_device_first_message.txt`) captured a receive-side composite verify failure on a fresh 1:1 session:

```
KAY [ChatNotifier] [DEC] FAILED: msg=…
  error=HsmServiceException(HSM_SIGNATURE_INVALID):
  Composite decrypt failed: Signature verification failed for 0x41
```

Derivation succeeded (HKDF chain + anchor hit), outer slot loaded, inner slot prepared. The HSM `0x41 DECRYPT_AND_VERIFY_MSG` failed only because the Ed25519 public key the receiver passed as `signerPublicKey` was wrong.

**Root cause.** `ChatNotifier.decryptMessage` sourced the sender's Ed25519 pub via `KeyRepository.getIdentityKey(userId, deviceId)` → `DeviceApiClient.getIdentityKey(deviceId)` → `DeviceService.GetIdentityKey`. The server handler (`kayten-server/internal/device/handler_grpc.go:213-256` pre-delta) had a documented silent fallback: when the requested `device_id` did not resolve directly, the server returned "the first active device's identity key for that user." For multi-device senders (re-enrolled, second device, historical rows with `user_id` in the `device_id` column), this returned the pub of a different device than the one that actually signed the transcript. Ed25519 verify then failed.

The same failure class was fixed for voice calls on 2026-04-16 by adding `signer_identity_pub` to `CallOffer` / `CallAnswer`. This spec extends the same wire-authoritative pattern to messaging.

## Contract

### Wire additions

| Envelope | Field | Tag | Required |
|---|---|---:|---|
| `SendMessage` | `bytes signer_identity_pub` | 22 | **Yes** (composite-capable sender) |
| `ReceiveMessage` | `bytes signer_identity_pub` | 25 | Relayed verbatim from SendMessage |
| `EditMessage` | `bytes signer_identity_pub` | 16 | **Yes** (composite-capable sender) |
| `GroupSenderKey` | `bytes signer_identity_pub` | 15 | **Yes** (SKDM sender) |

All four fields carry the 32-byte raw Ed25519 public key of the device that produced the corresponding signature (`signature` / `new_signature` / SKDM transcript sig).

### DeviceService surface

Retired:
- `rpc GetIdentityKey(GetIdentityKeyRequest) returns (GetIdentityKeyResponse);` — silent user-id fallback was unsafe. The entire RPC + its request/response messages are deleted. Callers hitting a post-delta server receive `Unimplemented`.

Added:
- `rpc GetDeviceIdentityKey(GetDeviceIdentityKeyRequest) returns (GetDeviceIdentityKeyResponse);` — strict per-device lookup. `device_id` MUST resolve to a registered device row. `NotFound` if it does not — no user-id fallback, no "first active device" resolution, no silent substitution.
- `rpc ListUserDevices(ListUserDevicesRequest) returns (ListUserDevicesResponse);` — enumerates all devices registered under a `user_id`, optionally including revoked devices. Each entry carries `device_id`, `ed25519_pub`, `ecdh_pub`, `curve_type`, `created_at`, `revoked_at`. Used by contact-verify UIs for per-device fingerprint display.

### Behavior

**Sender** (any composite-capable client):
1. Read the local signing device's Ed25519 public key from the HSM (slot `IDENTITY_ED25519_PUBLIC`).
2. Populate `signer_identity_pub` on every outbound composite `SendMessage`, `EditMessage`, `GroupSenderKey`.
3. Legacy (non-composite) senders that populate neither `iv_inner` nor `iv_outer` MAY omit the field.

**Server** (`kayten-server`):
1. Validate `signer_identity_pub` at ingress on every composite envelope: present, length exactly 32 bytes. Reject `InvalidArgument` otherwise.
2. Persist the field on the `messages` row (column `signer_identity_pub BYTEA`, migration 039).
3. Relay the field verbatim on live fan-out (`SendMessage` → `ReceiveMessage`) AND on offline-queue drain (`HandleSync` → per-row `ReceiveMessage`).
4. Optional (recommended): at ingress verify that `signer_identity_pub` matches the `identity_keys` row for `sender_device_id`; mismatch → `FailedPrecondition`. Closes the sender-impersonation attack where a compromised device claims a different device's identity.
5. Never synthesise, rewrite, or substitute the field.

**Receiver**:
1. Persist `ReceiveMessage.signer_identity_pub` verbatim on the local `messages` row (column `signer_identity_pub` / BLOB nullable).
2. On decrypt, source the signer's Ed25519 pub from the persisted row — wire-provided for incoming, local HSM for outgoing.
3. If the row's pub is absent on a non-outgoing decrypt: fail closed with a distinct error (e.g. `SIGNER_IDENTITY_PUB_MISSING`). No fallback to any server lookup.
4. (Future) Populate a TOFU cache keyed by `(user_id, device_id)`; on subsequent messages, compare the wire-provided pub against the cached value; on mismatch emit `KEY_CHANGE_ALERT` and pause the conversation pending user confirmation.

### No backward compatibility

- Clients post-delta refuse to operate against a pre-delta server (retired `GetIdentityKey` returns `Unimplemented`).
- Servers post-delta reject pre-delta composite sends (missing `signer_identity_pub`).
- Rows persisted before the delta have `signer_identity_pub = NULL` and remain legacy-undecryptable under the hardened-mode gate. Pre-release staging accepts this; production rollout (if any pre-delta data needed to survive) would require a separate re-sign migration not included in this spec.

## Status

| Repo | Head (post-delta) | Scope |
|---|---|---|
| `kayten-proto` | `f53735c` | 4 envelope fields + 2 new RPCs + 1 RPC retired. |
| `kayten-server` | `c4d2599` | Strict `GetDeviceIdentityKey`, `ListUserDevices`, ingress validator, fan-out + sync relay, migration 039, 4 fallback tests retired, strict-lookup test added. |
| `kayten-app` | `6ede50f` | Proto submodule bump, Drift schema v13 (`messages.signerIdentityPub`), sender populate, receiver persist + decrypt uses wire pub exclusively, `KeyRepository` strict-mode. |
| `kayten-app-v2` | pending | Same shape as `kayten-app`. |
| `iOS stubs` | pending | Signature updates only (no crypto logic). |

## Deploy order

Strict: **proto → server → app**. Server must ship before app (retired `GetIdentityKey` returns `Unimplemented` post-delta; pre-delta apps fail fast). Rollback path is to keep the three repos aligned on the pre-delta SHAs.

## Follow-ups (out of scope for this spec)

- Migrate the remaining `getIdentityKey` callsites in `kayten-app` (contact verify, call provider, file transfer, group sender key transport, incoming bootstrap) to wire-provided pubs or `ListUserDevices`. Current behavior: each call is now strict (NotFound → null) instead of silent substitution, so the critical decrypt path is already safe.
- Add a client-side `device_identities` TOFU cache + `KEY_CHANGE_ALERT` UI on per-`(user_id, device_id)` pub rotation.
- Extend sender population to `EditMessage` + `GroupSenderKey` (ingress validation + proto fields already in place).
- `kayten-app-v2` + iOS stub parity.
