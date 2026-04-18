# Group + File SKDM Cross-Repo Spec V1

Date: 2026-04-09
Status: Ready for repo-local implementation specs and AI worker execution
Applies to:
- `kayten-proto`
- `kayten-server`
- `kayten-app`
- `kayten-app-v2`

Not in scope:
- `uHSM-HSM`
- `uHSM-Host`

## 1. Goal

Fix cross-device group-key and file-key distribution without adding any new
HSM firmware primitive.

The final model is:
- `1:1 sessions` remain the only cross-device secure transport primitive
- `0x4E/0x4F` remain device-bound local persistence only
- `group sender keys` are distributed over existing 1:1 sessions
- `file content keys` are carried inside the already encrypted message
  plaintext, not as device-bound wrapped blobs

## 2. Final decisions

### 2.1 Group messaging

Group sender-key distribution uses a dedicated `GROUP_SENDER_KEY` envelope,
but the payload inside that envelope is no longer a device-local wrapped blob.

Instead:
- sender builds `SkdmPayload`
- sender encrypts that payload using the normal 1:1 message/session path to the
  target recipient
- resulting ciphertext + ratchet metadata are carried in `GroupSenderKey`

No backward compatibility is required for this redesign.

### 2.2 File transfer

File content keys are no longer transported as:
- `wrappedContentKeyInner`
- `wrappedContentKeyOuter`

Those fields are device-bound and structurally wrong cross-device.

Instead:
- file content keys are derived as raw 32-byte seeds
- the message plaintext attachment metadata schema is bumped from
  `attachment-v1` to `attachment-v2`
- `attachment-v2` carries raw content-key seed material in base64 form
- this is acceptable because the attachment payload already lives inside the
  normal message plaintext, which is protected by the existing 1:1 or group
  encryption layer

No proto change is required for file-key distribution if `AttachmentPayload`
remains an application-level JSON payload inside the encrypted message body.

### 2.3 No new HSM primitive

There is no new wrap/unwrap/derive firmware command in this design.

All cross-device transport is protocol-layer:
- 1:1 ratchet-protected message
- group sender-key fanout message

All local persistence remains device-bound:
- `wrapStorageRootKey` / `unwrapStorageRootKey`

## 3. Canonical wire contracts

### 3.1 `GroupSenderKey`

`GroupSenderKey` is redesigned to carry the encrypted sender-key distribution
payload and the metadata needed for the recipient to decrypt it through the
existing 1:1 session machinery.

Canonical message shape:

```protobuf
message GroupSenderKey {
  string conversation_id = 1;
  reserved 2; // old sender_key blob removed
  string target_user_id = 3;

  string sender_user_id = 4;
  string sender_device_id = 5;

  bytes ciphertext = 6;
  bytes iv = 7;
  bytes signature = 8;
  bytes ratchet_pub = 9;
  int32 ratchet_seq = 10;
  bytes tag_inner = 11;
  bytes tag_outer = 12;
}
```

### 3.2 `SkdmPayload`

`SkdmPayload` is the plaintext distributed per recipient through the encrypted
`GroupSenderKey` message.

Canonical message shape:

```protobuf
message SkdmPayload {
  string conversation_id = 1;
  uint32 chain_id = 2;
  bytes inner_seed = 3;   // exactly 32 bytes
  bytes outer_seed = 4;   // exactly 32 bytes
  bytes signing_pub = 5;  // sender Ed25519 identity pub
}
```

### 3.3 `AttachmentPayload` schema bump

`AttachmentPayload` moves from `attachment-v1` to `attachment-v2`.

`attachment-v2` removes:
- `wrappedContentKeyInner`
- `wrappedContentKeyOuter`

and adds:
- `innerSeed`
- `outerSeed`

Both are base64-encoded 32-byte values stored inside the encrypted message
plaintext.

Canonical JSON shape:

```json
{
  "schema": "attachment-v2",
  "name": "example.jpg",
  "mimeType": "image/jpeg",
  "sizeBytes": 123456,
  "fileReferenceId": "file-123",
  "innerSeed": "<base64 32 bytes>",
  "outerSeed": "<base64 32 bytes>",
  "sha256Hash": "<hex-or-base64-hash>"
}
```

## 4. Repo responsibilities

### 4.1 `kayten-proto`

Must:
- redesign `GroupSenderKey`
- add `SkdmPayload`
- regenerate consumers

Must not:
- add file-key proto fields unless the attachment JSON design is explicitly
  abandoned later

### 4.2 `kayten-server`

Must:
- relay redesigned `GROUP_SENDER_KEY`
- preserve fields unchanged
- keep the server cryptographically blind

Must not:
- derive or interpret any sender/file keys

### 4.3 `kayten-app`

Must:
- generate sender-key seeds as raw 32-byte material
- distribute them per recipient using the 1:1 encryption path
- persist received seeds locally via `0x4E/0x4F`
- migrate file attachment payloads to `attachment-v2`
- remove hardware-mode fail-closed guards after the new design is in place

### 4.4 `kayten-app-v2`

Must:
- mirror the `kayten-app` protocol and storage behavior
- implement the same `GroupSenderKey` decrypt/store path
- implement `attachment-v2`

Must not:
- ship group/file hardware-mode support until the existing hardware 1:1
  bootstrap path is complete end to end

## 5. Hard prerequisites

Before enabling runtime behavior on external HSM:
- `kayten-app` 1:1 Option A path must remain green
- `kayten-app-v2` hardware bootstrap must complete beyond `0x57`, including
  the ratchet-seed derive path

## 6. Definition of done

The design is complete when:
- no cross-device key transport uses `wrapKey(..., kSlotDbKeyInner)`
- `GroupSenderKey` carries encrypted 1:1 payload, not a device-bound blob
- file attachment metadata no longer carries device-bound wrapped content keys
- server remains a blind relay
- no uHSM command changes are required
