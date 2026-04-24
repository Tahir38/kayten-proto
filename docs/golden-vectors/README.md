# Golden vectors

Canonical cross-language test vectors for HSM-signed identity transcripts. Every repo that implements signing or verification of these transcripts MUST include a test that loads the relevant JSON file and asserts:

1. Its local canonical-transcript encoder produces bytes equal to `transcript_hex`.
2. The SHA-256 digest of those bytes equals `digest_hex`.
3. The Ed25519 signature in `signature_hex` verifies against `ed25519_identity_pub_hex` and `digest_hex`.

The vectors are self-contained. They include test-only private key material so any consumer repo can reproduce the signature byte-for-byte. The private keys in these vectors MUST NOT be used for any production identity.

## Files

| File | Encoder | Purpose string |
|---|---|---|
| `identity-registration-proof-v1.json` | `canonical-transcript-encoder-v1` | `kayten-identity-registration-v1` |

## Regenerating

Each vector has a matching `gen_*.go` script that produces it deterministically. Running the script must yield a byte-for-byte identical JSON output (modulo Go JSON map-key ordering, which is stable for a fixed input set).

```bash
cd docs/golden-vectors
go run gen_identity_registration_proof_v1.go > identity-registration-proof-v1.json
```

If a transcript format changes, the domain tag MUST also change (e.g. `kayten-identity-registration-v2`), and a new `*-v2.json` vector and generator script must be added. The old vector and script stay in place until every consumer repo has removed v1 support.
