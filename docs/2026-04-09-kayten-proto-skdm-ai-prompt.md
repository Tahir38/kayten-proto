You are working in `/Users/tahir/Repos/kayten-proto`.

Goal:
Implement the protobuf part of the SKDM redesign for group messaging.

Authoritative docs:
- `/Users/tahir/Repos/kayten-app/docs/superpowers/plans/2026-04-09-group-file-skdm-cross-repo-spec-v1.md`
- `/Users/tahir/Repos/kayten-app/docs/superpowers/plans/2026-04-09-kayten-proto-skdm-spec.md`

Required work:
1. Update `proto/kayten/v1/messaging.proto`.
2. Redesign `GroupSenderKey` to carry ratchet-protected ciphertext metadata instead of the old `sender_key` blob.
3. Add `SkdmPayload`.
4. Keep `Envelope.group_sender_key = 80` and `EnvelopeType_GROUP_SENDER_KEY`.
5. Do not add any firmware/HSM proto message.
6. Do not add file-key proto fields in this phase.

Required final `GroupSenderKey` shape:
- `conversation_id = 1`
- `reserved 2`
- `target_user_id = 3`
- `sender_user_id = 4`
- `sender_device_id = 5`
- `ciphertext = 6`
- `iv = 7`
- `signature = 8`
- `ratchet_pub = 9`
- `ratchet_seq = 10`
- `tag_inner = 11`
- `tag_outer = 12`

Required final `SkdmPayload` shape:
- `conversation_id = 1`
- `chain_id = 2`
- `inner_seed = 3` (32 bytes)
- `outer_seed = 4` (32 bytes)
- `signing_pub = 5`

Definition of done:
- proto compiles
- comments describe the new semantics accurately
- no old `sender_key` blob contract remains

At the end, report:
- files changed
- exact proto changes
- any downstream repos that must regenerate immediately
