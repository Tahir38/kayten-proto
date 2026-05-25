# Golden vectors

Canonical cross-language test vectors for HSM-signed identity transcripts. Every repo that implements signing or verification of these transcripts MUST include a test that loads the relevant JSON file and asserts:

1. Its local canonical-transcript encoder produces bytes equal to `transcript_hex`.
2. The SHA-256 digest of those bytes equals `digest_hex`.
3. The Ed25519 signature in `signature_hex` verifies against `ed25519_identity_pub_hex` and `digest_hex`.

The vectors are self-contained. They include test-only private key material so any consumer repo can reproduce the signature byte-for-byte. The private keys in these vectors MUST NOT be used for any production identity.

## Files

| File | Encoder | Purpose string | Path |
|---|---|---|---|
| `identity-registration-proof-v1.json` | `canonical-transcript-encoder-v1` | `kayten-identity-registration-v1` | bare digest — `signature = Ed25519(SHA-256(transcript))`. Locks the SoftHSM / pre-WP2-B1 firmware (`KAYTEN_FW_VERSION_MINOR=0`) contract. |
| `identity-registration-proof-v1.1-purpose-bound.json` | `canonical-transcript-encoder-v1` | `kayten-identity-registration-v1` | wrapped digest — `signature = Ed25519(SHA-256(purposeByte ‖ SHA-256(transcript)))`. Locks the WP2 B1 firmware-side purpose-binding contract (`KAYTEN_FW_VERSION_MINOR=1`). |

Both vectors share the same encoder, the same domain tag, the same inputs, and the same `wire_digest_hex` / `transcript_hex`. Only the final hash + sign step differs. This is intentional: the host/SDK builds one digest and hands it to the HSM; whether the HSM returns a bare or wrapped signature is a firmware-version property, not an encoder property.

### Which vector to assert against?

| Code path | Vector |
|---|---|
| Server proof verifier (cross-fleet) | **MUST verify against BOTH** — the server dual-verifies during the fleet rollout window. The server SHOULD try the wrapped form first and fall back to bare, then drop the bare fallback after the fleet is fully on `fw_minor=1`. Fleet status is observable via the `0x49 GET_RUNTIME_STATUS_V1` `fw_minor` byte (gated on `KAYTEN_HSM_CAP_RUNTIME_STATUS_V2`). |
| Hardware HSM firmware (uHSM-HSM ≥ `fw_minor=1`) | wrapped (`identity-registration-proof-v1.1-purpose-bound.json`) |
| SoftHSM (kayten-app `software_hsm_provider.dart`, kayten-app-v2 `soft_hsm_mobile.rs`) | bare (`identity-registration-proof-v1.json`) — unless and until the SoftHSM is upgraded to mirror the firmware wrap |
| Encoder-only tests (transcript byte / domain tag / wire digest equality) | either — the encoder output is byte-identical |

## Regenerating

Each vector has a matching `gen_*.go` script that produces it deterministically. Running the script must yield a byte-for-byte identical JSON output (modulo Go JSON map-key ordering, which is stable for a fixed input set).

```bash
cd docs/golden-vectors
go run gen_identity_registration_proof_v1.go               > identity-registration-proof-v1.json
go run gen_identity_registration_proof_v1_1_purpose_bound.go > identity-registration-proof-v1.1-purpose-bound.json
```

If the transcript format changes, the domain tag MUST also change (e.g. `kayten-identity-registration-v2`), and a new `*-v2.json` vector and generator script must be added. The old vectors and scripts stay in place until every consumer repo has removed v1 support.

## Conference frame IV vectors

`conference-frame-iv-v1.json` pins the **conference-only** key derivation + per-sender AES-GCM IV layout for hardware group calls (kayten-app spec `2026-05-25-hardware-group-call-enablement-design.md` §15.2 derivation + §15.6 IV layout). Every repo that derives conference keys or assembles a conference frame IV MUST load this file and assert byte-for-byte equality:

- **Derivation (`derivation` block, §15.2):** `HKDF-SHA256(IKM = conference_root, salt = call_id, info = label, L = size)` with the frozen `-v01` labels (`kayten-conf-voice-epoch-inner/outer-v01`, `kayten-conf-video-epoch-v01`, `kayten-conf-voice-ivbase-inner/outer-v01`, `kayten-conf-video-ivbase-v01`) → `expected_hex`. The labels are owned by spec §15.2; no consumer may re-spell or re-version them.
- **Voice IV (12B, §15.6):** `[ivBase:3B][memberIndex:1B][counter:8B big-endian]`
- **Video IV (12B, §15.6):** `[ivBase:7B][memberIndex:1B][counter:4B big-endian]`

Consumers: the firmware off-target harness (`mobile-hsm-gateway/hsm/tests/offtarget`, plan 3), the Kotlin `ConferenceFrameIv` builder (`kayten-app`, plan 5 voice), and the C++ video cryptor conference path (`kayten-app/android/app/src/main/cpp/kayten_video_frame_crypto/`, plan 6). The shipped **1:1** layouts (voice `[ivBase:4B][counter:8B]`, video `[ivBase:8B][counter:4B]`) are NOT covered here and MUST NOT change.

`counter` is the decimal counter; `counter_hex` (present on vectors above `2^53`) is the exact big-endian counter bytes for parsers that lose float precision on large JSON numbers. `member_index` is the authenticated per-sender index (0–4) from the signed `GroupRoster`. Inputs are test-only.
