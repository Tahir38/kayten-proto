# WP1 status — kayten-proto identity rotation contract v1

**Plan:** /Users/tahir/Repos/kayten-app/docs/superpowers/plans/2026-04-24-cross-repo-identity-production-readiness-plan.md §WP1
**Branch:** feature/identity-production-readiness-v1
**Commit:** see `git log` on this branch — the commit cannot cite its own SHA
**Status:** Proto contract landed. Consumer repos must bump submodule and regenerate stubs.

## Contract surface added

### common.proto
- Enum `ServerCapability` new values: `SERVER_CAPABILITY_IDENTITY_PROOF_V1 = 3`, `SERVER_CAPABILITY_BACKEND_RESET_STATE_V1 = 4`
- Message `BackendResetState` (new): fields 1–6 (environment, identity_dev_reset_epoch, identity_dev_reset_generation, reset_at_unix_ms, reset_reason, reset_on_startup_enabled)

### device.proto
- RPC `RequestIdentityRegistrationNonce` (new): request/response pair
- Message `RequestIdentityRegistrationNonceRequest` (new): fields 1–4 (user_id, device_id, hsm_serial, ed25519_identity_pub)
- Message `RequestIdentityRegistrationNonceResponse` (new): fields 1–3 (phone_binding_id, server_nonce, expires_at_unix_ms)
- Message `IdentityRegistrationProof` (new): fields 1–13 (domain_tag, user_id, phone_binding_id, device_id, hsm_serial, ed25519_identity_pub, ecdh_identity_pub, commit_state, commit_epoch, issued_at_unix_ms, server_nonce, client_nonce, signature)
- Message `IdentityRotationAuth` (new): Method enum (METHOD_UNSPECIFIED, METHOD_FIRST_COMMIT, METHOD_RECENT_SMS_REVERIFY, METHOD_ACCOUNT_RECOVERY_TOKEN, METHOD_PRIMARY_DEVICE_ATTESTATION); fields 1–4 (method, sms_reverify_id, account_recovery_token, linking_attestation)
- Message `RegisterIdentityKeyRequest`: added fields 9–11 (hsm_serial, proof, rotation_auth); field 8 (linking_attestation) marked DEPRECATED
- Message `DeviceIdentity`: added fields 7–12 (identity_commit_epoch, identity_commit_state, server_identity_version, current_rotation_id, post_rotation_pending_verify, rotated_at_unix_ms)

## Normative docs added

- `docs/canonical-transcript-encoder-v1.md` — pinned encoder rules, field order, domain-tag constants.
- `docs/golden-vectors/identity-registration-proof-v1.json` — deterministic test vector with valid Ed25519 signature.
- `docs/golden-vectors/gen_identity_registration_proof_v1.go` — reproducible generator.
- `docs/golden-vectors/README.md` — vector documentation.

## Verification results

- `buf lint` — clean on new additions (only pre-existing naming warnings unrelated to WP1).
- `buf build` — pass.
- `buf breaking --against develop` — no breaking changes (all additive).
- Golden vector — regenerates byte-identical; signature self-verifies inside generator.
- `buf generate` (Go + Dart) — succeeds; generated symbols present in common.pb.go, device.pb.go, device_grpc.pb.go.

## Not included in WP1 (deferred)

- Receiver-warning advisory fields on `SendMessage` / `ReceiveMessage` / `EditMessage` / `GroupSenderKey` (`sender_identity_commit_epoch`, `sender_identity_rotation_id`, `post_rotation_pending_verify`). `signer_identity_pub` already on the wire via CAP_WIRE_IDENTITY_V1 covers the cryptographically authoritative binding. Advisory fields should land together with WP4 server propagation.

## Consumer work unblocked

- WP2 uHSM-HSM — can now add IDENTITY_SIGN_V1 purpose enum matching `kayten-identity-registration-v1` domain tag.
- WP3 uHSM-Host — contract now defines fields the Host forwards verbatim.
- WP4 kayten-server — can implement nonce RPC handler, proof verifier, and lineage table against the new messages. Must include a golden-vector-loading verifier test.
- WP5 kayten-app — can implement HSM probe → nonce request → transcript build → HSM sign → RegisterIdentityKey flow. Must include a golden-vector-loading encoder test.
- WP9 dev reset — `BackendResetState` shape pinned; server needs a publication endpoint and app needs a generation-change detector.
