# Canonical transcript encoder v1

**Status:** Normative, frozen on the date of this file.
**Applies to:** Cross-repo identity production readiness v1 — proto, server, app, Host, HSM.

## Why this document exists

Every HSM-signed identity transcript in Kayten must produce the **exact same byte sequence** and the **exact same SHA-256 digest** on every supported platform: Go (server), Dart (app), Kotlin (Android plugin), Swift (iOS), and C (HSM firmware). If the encodings diverge even by a single byte the signature will not verify and a legitimate device registration will fail — or worse, two repos will silently agree on different transcripts and weaken the signature's meaning.

This document pins the encoder. Any transcript-producing change must update this document and the golden vector in `docs/golden-vectors/` in lockstep. Repos that implement signing or verification paths MUST include a test that compares their encoder output byte-for-byte against the golden vector.

## Encoding rules

1. **Network byte order.** All integers are serialized big-endian (network order), unsigned unless noted. Signed integers (Unix millisecond timestamps) use two's-complement big-endian.
2. **No padding.** No alignment padding is inserted between fields.
3. **No separators.** Fields are concatenated directly. Length prefixes disambiguate variable-length fields.
4. **Length prefix.** Every variable-length byte/string field is preceded by a 4-byte big-endian unsigned length (`uint32_be`). Zero-length fields encode as a 4-byte zero length followed by zero payload bytes. Length prefixes are mandatory even for fixed-size byte fields like public keys — this keeps the encoder uniform and makes cross-language tests simpler.
5. **Strings are UTF-8.** All `string` fields are encoded as their UTF-8 bytes, with no BOM, no null terminator, and no normalization. The caller is responsible for ensuring identifiers are already in their canonical form (e.g. UUIDs as lowercase 36-character strings).
6. **Enums are 4-byte fixed-width.** Protobuf enum values are serialized as `uint32_be` of the numeric enum value. The `_UNSPECIFIED = 0` variant MUST NOT appear in a production transcript.
7. **Domain tag is the first field and is fixed-width.** The 32-byte domain tag is emitted with NO length prefix — it is always 32 bytes. This is the only exception to the length-prefix rule. Field order matters: the domain tag comes first so that a verifier can reject an unknown purpose before spending work on the rest of the transcript.
8. **No protobuf wire format.** Raw protobuf serialization (`proto.Marshal`) is NOT used as the transcript. Go and Dart protobuf runtimes do not guarantee byte-identical output across versions, and field-skipping rules differ by runtime. This encoder is independent of protobuf's wire format.
9. **Hash.** The transcript bytes are hashed with SHA-256. The 32-byte digest is signed with Ed25519. The signature covers the digest, not the raw transcript.
10. **Versioning.** The encoder version is pinned by the domain tag. A new encoder version requires a new domain tag string (e.g. `kayten-identity-registration-v2`). Mixing v1 and v2 transcripts is impossible by construction — the domain tag will not match.

## Domain tag constants

Each signing purpose has a fixed 32-byte domain tag. The tag is the SHA-256 digest of the ASCII purpose string, with no trailing newline or whitespace.

| Purpose | Source string | Domain tag (hex, 32 bytes) |
|---|---|---|
| Identity registration | `kayten-identity-registration-v1` | `40547e31bafc49be8ae9f98e821b4dcce0e8ebea336ec0c0b653e0ced525e4d6` |
| Linking attestation | `kayten-link-attestation-v1` | `55e8fb4ee562746610ddb31a2cf0c5ff9deb8a6a68fb2e5edd967e7d402a8516` |
| WebSocket auth | `kayten-ws-auth-v1` | `a9da6bdb4ba71aa8c5a74ff8509fd672d76c7019650f4ec699bf3b8a8ab51d8c` |

Note: the existing `LinkingAttestation` message in `device.proto` uses a legacy null-separated transcript format documented in its field comment and predates this encoder. New transcripts MUST use this encoder. When the linking attestation path is next revised (v2), it will migrate to this encoder; until then the two formats coexist and are distinguished by their domain tags.

## IdentityRegistrationProof v1 transcript

Field order is fixed. Every field is mandatory; omitted-on-wire fields encode as zero-length or zero-value.

```
Offset   Size      Field
------   -------   ------------------------------------------------------
0        32        domain_tag (IDENTITY_REGISTRATION_DOMAIN_TAG_V1)
32       4         len(user_id)                     = uint32_be
36       N1        user_id                          UTF-8 bytes
.        4         len(phone_binding_id)            = uint32_be
.        N2        phone_binding_id                 UTF-8 bytes
.        4         len(device_id)                   = uint32_be
.        N3        device_id                        UTF-8 bytes
.        4         len(hsm_serial)                  = uint32_be
.        N4        hsm_serial                       UTF-8 bytes
.        4         len(ed25519_identity_pub)        = uint32_be (always 32)
.        32        ed25519_identity_pub             raw pubkey
.        4         len(ecdh_identity_pub)           = uint32_be (65 for P-256 uncompressed, 33 for compressed)
.        N5        ecdh_identity_pub                raw pubkey
.        4         identity_commit_state            uint32_be (enum value; 2 = COMMITTED)
.        8         identity_commit_epoch            uint64_be
.        8         issued_at_unix_ms                int64_be (two's complement)
.        4         len(server_nonce)                = uint32_be (always 32)
.        32        server_nonce                     raw bytes
.        4         len(client_nonce)                = uint32_be (always 32)
.        32        client_nonce                     raw bytes
```

Constants to pin in implementations:

- `ed25519_identity_pub` length is exactly 32. Encoders MUST reject any other length.
- `server_nonce` and `client_nonce` lengths are exactly 32 each. Encoders MUST reject any other length.
- `ecdh_identity_pub` length is either 65 (P-256 uncompressed, leading `0x04`) or 33 (P-256 compressed, leading `0x02`/`0x03`). Encoders MUST NOT emit a pub in any other encoding.
- `identity_commit_state` MUST equal `IDENTITY_COMMIT_STATE_COMMITTED` (2) for every production proof. `UNSPECIFIED` (0) is invalid.
- `identity_commit_epoch` MUST be ≥ 1 for any accepted proof. Zero is invalid.
- `issued_at_unix_ms` MUST be within the server's clock-skew tolerance. The server records the single-use nonce and rejects replays independently of timestamp drift.

## Reference pseudocode

```
func EncodeIdentityRegistrationProofTranscript(p Proof) []byte {
  w := newByteWriter()
  w.writeFixed(p.DomainTag)            // 32 bytes, panics if len != 32
  w.writeLenPrefixedUtf8(p.UserId)
  w.writeLenPrefixedUtf8(p.PhoneBindingId)
  w.writeLenPrefixedUtf8(p.DeviceId)
  w.writeLenPrefixedUtf8(p.HsmSerial)
  w.writeLenPrefixedBytes(p.Ed25519IdentityPub)  // enforce len == 32
  w.writeLenPrefixedBytes(p.EcdhIdentityPub)     // enforce len == 33 or 65
  w.writeU32(uint32(p.IdentityCommitState))
  w.writeU64(p.IdentityCommitEpoch)
  w.writeI64(p.IssuedAtUnixMs)
  w.writeLenPrefixedBytes(p.ServerNonce)         // enforce len == 32
  w.writeLenPrefixedBytes(p.ClientNonce)         // enforce len == 32
  return w.bytes()
}

digest := sha256(EncodeIdentityRegistrationProofTranscript(p))
signature := ed25519.Sign(identityPriv, digest)
```

`writeLenPrefixedUtf8(s)` emits `uint32_be(len(utf8(s))) || utf8(s)`. `writeLenPrefixedBytes(b)` emits `uint32_be(len(b)) || b`. `writeU32`/`writeU64`/`writeI64` emit the integer in big-endian byte order with no length prefix.

## Test requirements

Every repo that produces or verifies identity registration transcripts MUST include a golden-vector test that:

1. Loads the fixed test vector from `docs/golden-vectors/identity-registration-proof-v1.json`.
2. Runs the repo's local encoder with the test-vector inputs.
3. Asserts the raw transcript bytes match the `transcript_hex` field.
4. Asserts the SHA-256 digest matches the `digest_hex` field.
5. Verifies the Ed25519 signature in the vector against the vector's pub and digest.

Any repo that fails this test is not permitted to ship to staging or production. Broken transcripts produce silent signature mismatches which the server fails closed on — users see "identity registration failed" with no explanation.

## Non-goals

- This encoder is not a general-purpose serialization format. It exists solely to produce deterministic bytes for HSM signing.
- It does not include proto field numbers. If a field is added or removed in the future, the domain tag MUST be rotated (e.g. `…-v2`). Silent field additions are forbidden.
- It does not attempt to be self-describing. A parser cannot reconstruct the field names from the bytes; the field order is part of the shared contract.
