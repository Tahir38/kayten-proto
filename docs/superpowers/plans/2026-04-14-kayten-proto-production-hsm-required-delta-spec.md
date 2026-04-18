# 2026-04-14 — kayten-proto Production HSM-Required Delta Spec

## 1. Goal

For the production target “message read and send only with connected HSM”, `kayten-proto` must provide a stable wire contract that does not force clients into ambiguous crypto interpretation.

This spec is based on real code review of the current repo state.

## 2. Verified Current State

The proto already contains the currently required messaging contract pieces:

- `iv_inner` and `iv_outer` on `SendMessage`
- `iv_inner` and `iv_outer` on `ReceiveMessage`
- `new_iv_inner`, `new_iv_outer`, and `new_signature` on `EditMessage`
- explicit mixed-rollout precedence comments
- explicit canonical `new_signature` transcript comments
- explicit hardened replay comments stating edits reuse the original message row replay coordinates

The current pinned submodule state in `kayten-app-v2` also already matches this revision line.

## 3. Required Delta

No mandatory new proto field is required right now for the connected-HSM production target.

## 4. Allowed Scope

Proto work is limited to:

- preserving the current semantic messaging contract
- regenerating consumers when needed
- avoiding accidental regression of comments or field semantics

## 5. Do Not Add Yet

Do not add new replay-coordinate fields for edits unless there is a deliberate product decision to move away from the current frozen contract:

- edits currently reuse the original row replay coordinates for hardened reopen
- that contract is already documented

Do not add peer-ratchet wire fields unless the cross-repo ratchet design explicitly needs network-visible metadata. The current blocker is Host/HSM runtime semantics, not proto transport.

## 6. Required Verification

- comments for composite message fields remain intact
- comments for `EditMessage.new_signature` transcript remain intact
- generated artifacts in downstream repos can regenerate cleanly from this revision

## 7. Definition Of Done

1. The current semantic composite messaging contract remains stable.
2. No unnecessary field churn is introduced.
3. Any generated downstream bindings continue to match this contract.
