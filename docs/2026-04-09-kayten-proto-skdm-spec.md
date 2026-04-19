# kayten-proto SKDM Spec

Date: 2026-04-09
Repo: `kayten-proto`
Authority:
- `2026-04-09-group-file-skdm-cross-repo-spec-v1.md`

## Scope

Update the shared protobuf schema for group sender-key distribution.

This repo does not define application-level JSON attachment payloads, so file
transfer does not require a proto change in this phase.

## Required changes

Primary file:
- `proto/kayten/v1/messaging.proto`

### 1. Redesign `GroupSenderKey`

Replace the old device-bound `sender_key` blob transport with the new
ratchet-protected payload carrier.

Required final shape:

```protobuf
message GroupSenderKey {
  string conversation_id = 1;
  reserved 2;
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

### 2. Add `SkdmPayload`

Add:

```protobuf
message SkdmPayload {
  string conversation_id = 1;
  uint32 chain_id = 2;
  bytes inner_seed = 3;
  bytes outer_seed = 4;
  bytes signing_pub = 5;
}
```

Validation notes:
- `inner_seed` and `outer_seed` are application-level 32-byte values
- protobuf cannot enforce exact length, but comments must say “exactly 32 bytes”

### 3. Keep envelope type stable

Do not add a new envelope type.

Keep:
- `Envelope.group_sender_key = 80`
- `EnvelopeType_GROUP_SENDER_KEY`

The semantic change is inside `GroupSenderKey`, not in the envelope enum.

## Non-goals

Do not:
- add a new firmware-related message
- add file-key fields to protobuf unless the attachment JSON design is
  explicitly reversed later
- keep the old `sender_key` blob contract

## Verification

- proto compiles cleanly
- generated stubs succeed in downstream repos
- comments in `messaging.proto` explain that `GroupSenderKey` now carries
  ratchet-protected ciphertext, not a device-local wrapped blob

## Done criteria

- `GroupSenderKey` final shape matches this spec
- `SkdmPayload` exists
- no old `sender_key` blob contract remains in comments
