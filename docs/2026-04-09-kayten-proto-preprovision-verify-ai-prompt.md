You are working in /Users/tahir/Repos/kayten-proto.

Goal:
Clarify the protobuf/API contract for `VerifyCodeRequest` so fresh real-HSM devices can perform SMS verification before provisioning and secure-channel establishment.

Authoritative spec:
- /Users/tahir/Repos/kayten-proto/docs/2026-04-09-kayten-proto-preprovision-verify-spec.md

Required work:
1. Update `proto/kayten/v1/auth.proto` comments for:
   - `AuthService.VerifyCode`
   - `VerifyCodeRequest`
2. Make it explicit that during pre-provision verification on a fresh real HSM:
   - `phone_number`, `code`, and `hsm_serial` are required
   - `hsm_firmware_version` and `device_name` are optional metadata
   - `ed25519_public_key`, `ecdh_public_key`, and `identity_signature` may be empty
   - `keystore_ecdh_public_key` and `attestation_cert_chain` may also be empty
3. Clarify that for proto3 `bytes` fields, the pre-provision empty representation is zero-length bytes.
4. Clarify that real identity and attestation material may be registered later after provisioning and secure-channel setup.
5. Keep the wire format unchanged.
6. Regenerate protobuf outputs if this repo normally tracks generated artifacts for the touched surface.

Do not:
- change field numbers
- rename fields
- add a new RPC
- redesign the auth flow in proto

Definition of done:
- proto comments unambiguously document the pre-provision VerifyCode contract
- no wire-level incompatibility is introduced
- generated outputs are updated only if required by repo convention

At the end, report:
- files changed
- whether generated outputs changed
- exact contract wording clarified
