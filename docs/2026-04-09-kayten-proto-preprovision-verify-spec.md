# 2026-04-09 Kayten Proto Pre-Provision Verify Spec

## Goal

Clarify the `VerifyCodeRequest` contract for real-HSM onboarding before token provisioning and secure-channel establishment.

## Problem Statement

The proto currently exposes these fields on `VerifyCodeRequest`:
- `ed25519_public_key`
- `ecdh_public_key`
- `identity_signature`
- `hsm_serial`
- `hsm_firmware_version`
- `device_name`
- `keystore_ecdh_public_key`
- `attestation_cert_chain`

This can be misread as meaning identity material is always expected during SMS verification.
For fresh real HSM devices, that is incorrect.

A fresh HSM may only be able to provide:
- `hsm_serial`
- `hsm_firmware_version`
- `device_name`

before later provisioning and secure-channel setup establish the cryptographic identity.

## Required Proto Contract Clarification

`VerifyCodeRequest` must document that:
- `phone_number`, `code`, and `hsm_serial` are required for verification
- `hsm_firmware_version` and `device_name` are optional metadata and must be non-fatal when absent
- `ed25519_public_key`, `ecdh_public_key`, and `identity_signature` are optional during the pre-provision phase
- `keystore_ecdh_public_key` and `attestation_cert_chain` are optional during the pre-provision phase
- clients with a fresh real HSM may send those three fields empty during SMS verification
- clients with a fresh real HSM may also send empty attestation-related fields during SMS verification
- real identity material may be registered later after provisioning and secure-channel establishment

For proto3 encoding in this phase:
- optional `bytes` fields are represented as zero-length byte strings on the wire/model surface
- there is no separate nullable wire-level representation that clients should rely on
- server and client implementations must treat zero-length `bytes` as the canonical pre-provision empty value

## Required Comment Direction

The `AuthService.VerifyCode` RPC comment should no longer imply that device identity establishment is always complete in the same step.
The message-field comments should make the phase split explicit.

Suggested wording intent:
- `VerifyCode` verifies the SMS code and registers the device shell / device record
- identity key material may be absent during pre-provision verification on fresh real-HSM devices
- attestation-related material may also be absent during pre-provision verification on fresh real-HSM devices
- later post-provision identity registration remains valid and expected

## Compatibility Requirements

- no field numbers change
- no field names change
- no new RPC
- no backward-incompatible wire change
- this is a contract/documentation clarification, not a schema redesign

## Definition of Done

- proto comments clearly describe pre-provision VerifyCode semantics
- downstream code generators can regenerate without wire changes
- future client implementers will not incorrectly assume a secure channel is mandatory before SMS verification
