# Cross-repo spec (stub) — two-phase provisioning

- **Date:** 2026-04-18
- **Status:** **SCOPE STUB** — not actively implemented. Queued for Q2/Q3 2026. Promote to full spec + per-repo specs + AI prompts if a trigger condition below fires.
- **Precursor workstream (must merge first):** `2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`. This spec only makes sense on top of the PKCS#11-retirement command surface.
- **Scope:** `kayten-proto`, `uHSM-HSM`, `uHSM-Host`, `kayten-app`, `kayten-app-v2`. No `kayten-server` change.

## 1. Motivation — what this closes

The 2026-04-17 PKCS#11 retirement leaves **one** residual PIN-on-plaintext-SPI exposure, documented in master spec §10 consideration #14:

> `PROVISION_DEVICE_V1 (0x44)` carries `init_pin` + `user_pin` in plaintext outer SPI — one-shot per device lifecycle.

The reason it stays plaintext in the 2026-04-17 release: `SECURE_EXECUTE_V1 (0x55)` is Ed25519-signed by the HSM's *identity* key (`android/…/SecureChannel.kt:562-620`), and `0x44` is the command that *generates* that identity key. A pre-provision `0x55` is physically impossible without downgrading the handshake to unauthenticated ephemeral-only ECDH (rejected — no MitM protection, forks the secure-channel implementation). `init_pin` is a physical possession factor (label-printed), but `user_pin` is a user-chosen secret that ideally never crosses plaintext.

Two-phase provisioning closes this by splitting `0x44` into two atomic commits separated by a `0x55` handshake using the *freshly generated* identity key.

## 2. Current residual (unchanged by this stub)

| Exposure | Duration | Attacker requirement | Accepted in 2026-04-17? |
|---|---|---|---|
| `init_pin` on plaintext SPI during `0x44` | One exposure per provisioning event (initial + any post-wipe re-provision) | Physical FT4222 bus access at provisioning moment | Yes — `init_pin` is a physical possession factor already |
| `user_pin` on plaintext SPI during `0x44` | One exposure per provisioning event (initial + any post-wipe re-provision) | Physical FT4222 bus access at provisioning moment | Yes for low-wipe-frequency deployments; **no** for mass-deployment / high-assurance customers — promote this stub earlier |
| `user_pin` on plaintext SPI during `0x45 LOGIN_USER_V1` | Every login | Physical FT4222 bus access at any time | **No** — closed by 2026-04-17 §3.1.1 (0x45 via 0x55 in hw prod) |
| `user_pin` on plaintext SPI during `0x46 CHANGE_USER_PIN_V1` | Every PIN change | Physical FT4222 bus access at PIN change | **No** — closed by 2026-04-17 §4 (0x46 via 0x55 in hw prod) |

So after this stub spec, the `user_pin` is **fully protected** against SPI-bus adversaries. `init_pin` never leaks because it's plaintext-only-by-design (physical label).

## 3. Design sketch

### 3.1 New intermediate provisioning state

Add a fourth `ProvisioningState` enum variant (wire value `0x03`):

| Wire | Name | Meaning |
|---|---|---|
| `0x00` | `PROVISIONING_REQUIRED` | Fresh / wiped. No keys. (unchanged) |
| `0x01` | `PROVISIONED` | `user_pin` set, identity key present, ready to `LOGIN_USER_V1`. (unchanged, **now reached via 0x03**) |
| `0x02` | `LOCKED_WIPED` | Transient — same as 2026-04-17. (unchanged) |
| **`0x03`** | **`IDENTITY_ESTABLISHED`** (NEW) | Identity key generated + committed. No `user_pin` yet. `0x55` handshake works (identity signs transcript). Only `0x45`/`0x46`/`0x55`/`0x4A`/`0x43`/`0x49` dispatchable, and `0x46` `SET_USER_PIN_V1` inside `0x55` is the mandatory next step. |

### 3.2 New command (phase A)

- **`INIT_PROVISION_V1 (0x44a` — new opcode, likely `0x69` per parent §3.2 free-id pattern):**
  - Request: `[init_pin_len][init_pin]`
  - Server (firmware) flow:
    1. Reject if state != `PROVISIONING_REQUIRED`.
    2. Throttle pre-check (inherits 2026-04-17 §5.1.1 exponential backoff).
    3. Constant-time compare `init_pin`.
    4. On match: reset throttle counter.
    5. NVM write order (inherits 2026-04-17 §5.1.2 contract):
       a. `IDENTITY_KEY_PAIR_BLOCK` ← generated `(IDENTITY_PRIV=22, IDENTITY_PUB=23)` via `Kayten_Crypto_GenerateKeyPair(..., ALGO_ED25519)`
       b. `RUNTIME_STATE_BLOCK` ← `{provisioning_state = IDENTITY_ESTABLISHED}` **[commit marker]**
    6. Response: `[status=OK, provisioning_state=IDENTITY_ESTABLISHED, identity_pub:32]`.
  - `user_pin` does not cross the wire in this phase.

### 3.3 Phase B — 0x55-wrapped CHANGE_USER_PIN_V1

- App establishes `0x55 SECURE_EXECUTE_V1` handshake using the freshly-generated identity key (state `IDENTITY_ESTABLISHED` allows `0x55`).
- App sends inner `CHANGE_USER_PIN_V1 (0x46) { current = "", new = user_pin }` — empty `current` is a sentinel recognized only in state `IDENTITY_ESTABLISHED` (reject with `0xED SESSION_NOT_READY` in any other state).
- Firmware flow:
  1. Verify state == `IDENTITY_ESTABLISHED`.
  2. Verify `current.len() == 0` (sentinel).
  3. Write `USER_PIN_BLOCK` ← `{user_pin, initialized=1, retries=3}`.
  4. Write `RUNTIME_STATE_BLOCK` ← `{provisioning_state = PROVISIONED}` **[commit marker]**.
- Response: `[status=OK]`.

### 3.4 Atomicity across the two phases

A crash between phase A commit and phase B commit leaves the device in state `IDENTITY_ESTABLISHED` — identity key is valid and stable, but no `user_pin`. Recovery: the app retries phase B on next cold-start (silent-state probe already exists in 2026-04-17 §6a.3 step 0 — extend the decision table to handle `IDENTITY_ESTABLISHED` as "resume phase B with freshly prompted `user_pin`").

A hostile interruption during phase A (attacker cuts SPI between write 3a and 3b) leaves `PROVISIONING_REQUIRED`; retry is idempotent (overwrites identity NVM with fresh key — fine because no partner has seen this identity).

### 3.5 Proto deltas

- `ProvisioningState.IDENTITY_ESTABLISHED` (wire `0x03`, enum value `4` because UNSPECIFIED=0 offset).
- `MobileCommandId.INIT_PROVISION_V1 = 0x69` (or next free after `0x68`).
- No new `ChangeUserPinV1` variant — existing message reused with empty `current` sentinel.

### 3.6 App UX impact

Currently: one screen collects both PINs, single submit → single `0x44` → home.

After: one screen collects both PINs, single submit → `0x69 INIT_PROVISION_V1` → `0x55` handshake → `0x46 SET_USER_PIN_V1` (inside `0x55`) → home. No user-visible change. Minor internal state machine expansion in `lib/features/auth/providers/enrollment_manager.dart` and the Rust-core `HsmClient::provision_device` method.

## 4. Cost estimate

| Repo | Work | Estimate |
|---|---|---|
| kayten-proto | New enum value + new command id + new RPC param contract (if any) | 0.5 day |
| uHSM-HSM | New `IDENTITY_ESTABLISHED` state handling; new `0x69` handler; `0x46` sentinel semantics; state transition tests | 3 days |
| uHSM-Host | Forward `0x69` pass-through; state-dispatch table update | 0.5 day |
| kayten-app (v1) | `EnrollmentManager` two-step flow; schema unchanged since identity is already committed before `user_pin` in 2026-04-17 write order; minor UX polish on interrupted-provision recovery | 2 days |
| kayten-app-v2 | Same flow in Rust core (`HsmClient::provision_device` becomes a two-call sequence); bridge work ≈ zero | 1 day |

**Total ≈ 7 working days across one dev**, plus 2 days test matrix + cross-repo QA.

## 5. Trigger conditions for promotion

Promote this stub to a full spec + per-repo specs + AI prompts if any of these fire:

1. **Mass deployment** — factory-line, MDM-managed enterprise bulk provisioning, or any deployment posture where SPI-bus access during provisioning (initial OR post-wipe re-provision) is realistic rather than narrow end-user home/office setup. The residual's "narrow attack surface" argument in master §10 #14 relies on the customer's operational posture, not just the product's technical posture — any enterprise deal with a concrete security-audit checkpoint in its sales cycle should treat this trigger as already fired.
2. **High-assurance vertical customer ask** — defense, gov, regulated finance sector asks for a SOC 2 / FIPS 140-3 audit checkpoint.
3. **Compliance audit finding** — any third-party security audit explicitly flags plaintext `user_pin` on the provisioning wire.
4. **Threat-model shift** — if future workstreams add a USB-over-air path (BLE HSM, WiFi HSM proxy), the "physical FT4222 bus access" precondition weakens dramatically; the residual becomes materially exploitable.
5. **Developer request** — if implementing the 2026-04-17 release surfaces that the `init_pin`-only phase is cleaner to reason about (plausible: simpler state machine, better separation of concerns), consider promoting on engineering-hygiene grounds.

## 6. Non-triggers (explicit)

- **Solo consumer / end-user deployments** alone do not trigger promotion — the 2026-04-17 release is sufficient.
- **Routine pentest** that mentions the residual without classifying it as a finding — expected; the residual is disclosed in master spec §10 #14 by design.

## 7. References

- Parent 2026-04-17 master spec — §3.1 / §3.1.1 / §5.1 / §10 #14 / §13 follow-ups.
- Research background (why 0x55 pre-identity is impossible): `android/app/src/main/kotlin/com/kayten/uhsm/hsm/SecureChannel.kt:562-620`.
- `kayten-proto` enum-value offset conventions: `2026-04-17-kayten-proto-hsm-keys-constants-spec.md §3.0.1`.

## 8. When to write full specs

If / when promoted:
1. Full cross-repo master spec (this stub expanded) — name: `2026-XX-XX-cross-repo-two-phase-provisioning-spec.md`.
2. Per-repo specs: `kayten-proto`, `uHSM-HSM`, `uHSM-Host`, `kayten-app`, `kayten-app-v2`.
3. AI prompts for each repo following the 2026-04-17 template (self-contained, mandatory-reading + task list + verification + guardrails + return format).
4. Sequencing: same three-week fan-out as 2026-04-17 (proto → server-none-this-time → firmware + host → apps).
