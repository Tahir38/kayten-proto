# Kayten Proto Edit And Runtime Comment Delta Spec

Date: 2026-04-14
Repo: `kayten-proto`
Status: Code-reviewed delta spec

## 1. Verified Current State

The current proto already contains:

- `SendMessage.iv_inner`
- `SendMessage.iv_outer`
- `ReceiveMessage.iv_inner`
- `ReceiveMessage.iv_outer`
- `EditMessage.new_iv_inner`
- `EditMessage.new_iv_outer`
- `EditMessage.new_signature`

The proto does NOT contain:

- `edit_ratchet_epoch`
- `edit_message_counter`

## 2. Mandatory Delta

No mandatory new field delta is required right now if the product freezes the current edit contract:

- encrypted edits reuse the original message row's replay coordinates
- edits do not advance the chain in the hardened local-history model

However, that contract is not visible enough in proto comments today.

The mandatory proto delta is therefore comment-contract clarification:

1. `EditMessage.new_ratchet_seq` is legacy compatibility data and is not authoritative replay metadata for hardened historical reopen
2. hardened edit replay in the current architecture reuses the original message row's replay coordinates
3. `EditMessage.new_signature` signs the canonical composite edit
   transcript, not arbitrary repo-local bytes
4. the canonical composite edit transcript is:
   - domain prefix `kayten-edit-v1`
   - `message_id`
   - `conversation_id`
   - `edit_seq`
   - `new_ratchet_seq`
   - `new_ciphertext`
   - `new_iv_inner`
   - `new_iv_outer`
   - `new_tag_outer`
5. if the product later wants edit-specific replay coordinates, that
   requires a separate additive proto delta

## 3. Conditional Future Delta

Only if product/security intentionally changes the edit contract away from “reuse original replay coordinates”, then add:

- `edit_ratchet_epoch`
- `edit_message_counter`

This is not mandatory under the current reviewed app behavior.

## 4. Definition Of Done

This delta is done only when the proto comments make the current hardened edit contract explicit, pin `new_signature` to the canonical transcript, and no worker can reasonably misread `new_ratchet_seq` as the final authoritative replay coordinate.
