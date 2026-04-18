# uHSM-HSM — mobile profile v2 implementation spec

- Date: 2026-04-17
- Scope repo: `/Users/tahir/Repos/uHSM/uHSM-HSM`
- Parent: `2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`
- Status: implementation-ready draft

## 1) Objective

Implement the firmware side of PKCS#11 retirement for mobile by introducing a semantic command surface, deleting CSM/CryIf dependencies for mobile flows, and enforcing lockout-triggered wipe semantics.

## 2) Mandatory outcomes

1. Add `Kayten_Crypto.{c,h}` as the single call-site for mobile crypto operations (parent §2.2 / §7 **uHSM-HSM** row).
2. Add mobile handlers for **`0x60` through `0x68`** in `Kayten_Mobile.c` (`0x67`
   only when `KAYTEN_INIT_PIN_SOURCE == KAYTEN_INIT_PIN_DEV_RESETTABLE` per parent
   §2.4.1). Parent §7 **uHSM-HSM** row lists the same range.
3. Replace legacy provisioning semantics with `PROVISION_DEVICE_V1 (0x44)` (`init_pin` + `user_pin`) implementing the **write-ordering contract (parent §5.1.2)** and **init-pin exponential throttle (parent §5.1.1)**.
4. Implement `Kayten_WipeAllKeyMaterial(reason)` with idempotent power-loss recovery.
5. Extend inner command allow-list for `SECURE_EXECUTE_V1 (0x55)` to include new semantic commands **and `KAYTEN_CMD_LOGIN_USER_V1`** (parent §4 / §3.1.1).
6. **Delete** AUTOSAR CSM / CryIf integration from the mobile firmware profile per parent — not merely `#if` it out: remove `Src/BSW/Csm/`, `Src/GenData/Csm_Cfg.{c,h}`, `Src/GenData/CryIf_Cfg.{c,h}`, `Src/BSW/SchM/SchM_Csm.h`, `Src/BSW/SchM/SchM_CryIf.h`, `Rte_Csm_Type.h`, and scrub `CsmKey_*` / `CryIfKey_*` / `Kayten_Process*` PKCS#11-shaped paths from `kHsm_Kayten.c` until the parent §11 `rg` gates pass.
7. **New NVM slot bands (parent §9b).** Reserve (but do not pre-populate) NVM regions for: secure-channel `110..117`, voice-ephemeral `118..129`, conversation `130..191`, ephemeral `192..239`. Today's firmware has no reservations at these addresses — add them to the NVM layout file now so future Host-side renumbering lands without another firmware bump. **Follow the `kOpk` module pattern** shipped by the 2026-04-16 OPK/DH4 workstream (`Src/BSW/kOpk/` merged as commit `b0b4c46` on develop) — see §2.7.1 for the authoritative template.

### 2.7.1 Module pattern — follow `kOpk` precedent

`Src/BSW/kOpk/` is the reference architecture for all new NVM-backed crypto modules in the mobile profile. Key facts consumers must mirror for the 110..239 bands:

- **Module ownership.** Each new band gets its own module under `Src/BSW/` (e.g. `Src/BSW/kSecureChannel/`, `Src/BSW/kConversation/`, `Src/BSW/kEphemeral/`, `Src/BSW/kVoiceEphemeral/`) OR is implemented inline in `Kayten_Mobile.c` if the band is small (voice ephemeral, secure channel). Rule of thumb: ≥20 slots → new module; ≤15 slots → inline in `Kayten_Mobile.c`.
- **Logical-id ↔ internal-index mapping.** Module exposes `*_IsValidLogicalKeyId(u32)` and converts the wire-visible `crypto_key_id` to an internal bank index. Do NOT use `Crypto_Keys[]` slots or `Pkcs11_ElementNvmMapping` entries.
- **NvM block layout.** Each module owns a contiguous block range in `Src/GenData/NvM_Cfg.{c,h}`. Allocation plan for the 110..239 bands (contiguous with kOpk at `113..132`):

  | Band | Logical crypto_key_id range | NvM block id range | Block count | Block size |
  |---|---|---|---|---|
  | (existing) OPK | 70..109 | 113..132 | 20 | 96 B per pair |
  | (new) Secure-channel | 110..117 | 133..140 | 8 | 96 B per pair (or 64 B if sym-only) |
  | (new) Voice-ephemeral | 118..129 | 141..152 | 12 | 96 B per pair |
  | (new) Conversation | 130..191 | 153..214 | 62 | 96 B per pair |
  | (new) Ephemeral | 192..239 | 215..262 | 48 | 96 B per pair |
  | Throttle state | (non-key) | 263..264 | 2 | 1 B + 8 B |

  Bump `NVM_TOTAL_NUM_OF_BLOCK` to **265** (from current 135 after kOpk). Add matching FEE descriptor rows in `Src/GenData/Fee_Cfg.c` following the pattern `075add5` established for kOpk.

- **Module init hook.** Each module adds an `EcuM_StartupTwoCallout` entry — same pattern as `9c9293b` for kOpk (`kOpk_Init` called alongside `Crypto_BB_Hwa_SelfTest_P256`).
- **Consume semantics.** If the band is one-time-use (OPK-style), the consume function MUST zeroize RAM + invalidate NvM block before returning, regardless of the downstream ECDH outcome (kOpk §2.5 precedent). For the new bands, conversation + ephemeral are re-usable; only future one-time-use bands need the consume-and-burn contract.
- **Status-code mapping.** Module-private return codes (e.g. `KOPK_E_EMPTY = 0x10`) live inside the module's header; handler maps to a `KAYTEN_STATUS_*` mobile status byte at dispatch time. Stay inside the AUTOSAR module-specific return-code range `0x02..0x3F` (per `Std_Types` convention).

## 3) Files and modules to change

- Add: `Src/BSW/kHsm/Kayten_Crypto.c`
- Add: `Src/BSW/kHsm/Kayten_Crypto.h`
- Add: `Src/BSW/kHsm/Kayten_Mobile.c` (new semantic dispatch handlers)
- Update: `Src/BSW/kHsm/kHsm_Kayten.c`
- Update: `Src/BSW/kHsm/kHsm_Kayten.h`
- Update: NVM layout header (`Src/BSW/NvM/NvM_Cfg.h` or equivalent) — add block ids for new slot bands + `init_pin_fail_count` + `last_failed_init_pin_unix`.
- **Delete** (Week 3 cut-over, gated by `KAYTEN_HSM_MOBILE_PROFILE_V2`): CSM/CryIf trees and PKCS#11-shaped mobile handlers per parent §7 — interim Week 1 may compile with profile **STD_OFF** for bisect, but the **target** tree matches parent §2.2 (no residual `Csm_*` / `CryIf_*` on the mobile path).

## 4) Command contracts to implement

- `0x60 GENERATE_MESSAGING_KEY_V1`
- `0x61 READ_PUBLIC_KEY_V1` — **must accept ids in the new ephemeral band `192..239`** for voice-call and X3DH-ephemeral pub reads (parent §9b).
- `0x62 DELETE_MESSAGING_KEY_V1`
- `0x63 ROTATE_SPK_V1` (SPK retention: **current + previous** privates max two non-zero — master §5.5)
- `0x64 GENERATE_RANDOM_V1`
- `0x65 USER_INITIATED_WIPE_V1` (requires RAM `wipe_nonce` from `0x68`; SECURE_EXECUTE-wrapped in hardware prod)
- `0x66 GET_WIPE_STATUS_V1`
- `0x67 DEV_SET_INIT_PIN_V1` (`#if KAYTEN_INIT_PIN_SOURCE == KAYTEN_INIT_PIN_DEV_RESETTABLE`)
- `0x68 REQUEST_WIPE_CHALLENGE_V1` (TRNG nonce, TTL, single-shot RAM state; cleared on logout / successful `0x65` / power-loss)

Command payload/response format follows master spec §5.

### 4.1 Additional firmware state

- RAM-backed **wipe challenge slot:** `{ nonce[32], valid_until_unix, active }` for `0x68` / `0x65` pairing (`SPI_MOBILE_STATUS_WIPE_NONCE_INVALID (0xEF)` on mismatch per parent §7).
- **NVM-backed init-pin throttle state:** `{ init_pin_fail_count:u8, last_failed_init_pin_unix:u64 }` per parent §5.1.1. Persists across reboots. Cleared only on successful `0x44`. Drives `SPI_MOBILE_STATUS_INIT_PIN_THROTTLED (0xF5)`.
- **`KAYTEN_IS_ALLOWED_INNER_CMD`:** include `KAYTEN_CMD_LOGIN_USER_V1`, `KAYTEN_CMD_CHANGE_USER_PIN_V1`, and all commands listed in parent §4 (including new semantic inner ops).

## 5) Provisioning and PIN semantics

### 5.1 `PROVISION_DEVICE_V1 (0x44)` flow (parent §5.1)

Firmware behaviour must match parent §5.1 verbatim:

1. Reject if `provisioning_state != PROVISIONING_REQUIRED` → `SPI_MOBILE_STATUS_ALREADY_PROVISIONED (0xE4)`.
2. **Throttle pre-check:** if `now - last_failed_init_pin_unix < throttle_delay(init_pin_fail_count)`, return `SPI_MOBILE_STATUS_INIT_PIN_THROTTLED (0xF5)` **without attempting init_pin compare** (prevents timing oracles).
3. Constant-time compare `init_pin` against factory-fused NVM. Mismatch: `init_pin_fail_count++`; `last_failed_init_pin_unix = now`; persist both atomically via NvM block write; return `SPI_MOBILE_STATUS_INIT_PIN_INVALID (0xE5)`.
4. On match: reset `init_pin_fail_count = 0`; persist.
5. **NVM write order — each atomic at NvM block layer:**
   ```
   Step 5a:  NvM_WriteBlock(KAYTEN_NVM_USER_PIN_BLOCK,
                           {user_pin, initialized=1, retries=3});
             WaitUntil(NvM_GetJobStatus == OK);

   Step 5b:  NvM_WriteBlock(KAYTEN_NVM_IDENTITY_KEY_PAIR_BLOCK,
                           generated_pair_from Kayten_Crypto_GenerateKeyPair(
                               CRYPTO_KEY_ID_IDENTITY_PRIV=22,
                               CRYPTO_KEY_ID_IDENTITY_PUB=23,
                               ALGO_ED25519));
             WaitUntil(NvM_GetJobStatus == OK);

   Step 5c:  NvM_WriteBlock(KAYTEN_NVM_RUNTIME_STATE_BLOCK,
                           {provisioning_state = PROVISIONED,
                            last_wipe_reason unchanged,
                            remaining_pin_retries = 3});       // COMMIT MARKER
             WaitUntil(NvM_GetJobStatus == OK);
   ```
6. Emit `{status=OK, provisioning_state=PROVISIONED, identity_pub:32, crc}` on the SPI.

Atomicity: no journal needed. Any crash before step 5c leaves `provisioning_state == PROVISIONING_REQUIRED`; a retry of `0x44` idempotently overwrites steps 5a/5b and then commits 5c.

### 5.1.1 Throttle table (parent §5.1.1)

```
static const uint32_t throttle_delay_s[] = { 0, 0, 0, 10, 60, 600, 3600 };

uint32_t throttle_delay(uint8_t fail_count) {
    if (fail_count >= sizeof(throttle_delay_s)/sizeof(throttle_delay_s[0])) {
        return throttle_delay_s[sizeof(throttle_delay_s)/sizeof(throttle_delay_s[0]) - 1];  // cap at 3600s
    }
    return throttle_delay_s[fail_count];
}
```

### 5.2 `LOGIN_USER_V1 (0x45)` — inner-only in hardware prod

Per parent §3.1 / §3.1.1 / §4: firmware rejects bare outer `0x45` with `SPI_STATUS_CMD_NOT_AVAILABLE (0xE0)` once `SPI_CAP_MOBILE_PROFILE_V1` is advertised and the hardware-prod profile is active. The inner-dispatch path (invoked by `SECURE_EXECUTE_V1` handler) runs the standard login flow — 3-strikes lockout → wipe with `reason = PIN_LOCKOUT`.

Dev/staging builds MAY keep the bare-outer fast path compiled in (same `#if` gate style as `0x46` in the existing Delta B work).

### 5.3 `CHANGE_USER_PIN_V1 (0x46)` — inner-only in hardware prod (unchanged from prior spec)

### 5.4 `GET_RUNTIME_STATUS_V1 (0x49)` response extension

Append (post-length-prefixed — no CRC break):
- `provisioning_state:1`
- `last_wipe_reason:1`
- `remaining_pin_retries:1`

## 6) Wipe procedure requirements

`Kayten_WipeAllKeyMaterial` must wipe:
- identity pair (22, 23)
- SPK band (35..44)
- OPK band (70..109)
- msg bootstrap secret (64)
- SRK and session-scoped key material
- **Any populated new-band slots**: secure-channel `110..117`, voice-ephemeral `118..129`, conversation `130..191`, ephemeral `192..239`

It must persist wipe metadata and recover safely after power-loss. Recovery heuristic from master §6a is required.

`init_pin_fail_count` and `last_failed_init_pin_unix` are **retained** on wipe (the throttle is a factory-label brute-force defense, not a user-data concern).

## 7) Build profiles

- `KAYTEN_INIT_PIN_FACTORY_FUSED`
- `KAYTEN_INIT_PIN_DEV_RESETTABLE`
- `KAYTEN_INIT_PIN_STAGING_PINNED`

Only dev-resettable profile exposes handler `0x67` and sets `SPI_CAP_DEV_FIRMWARE`.

## 8) Status codes (export from `kHsm_Kayten.h`)

All status codes consumed by `kMobileManager` host-side dispatch (parent §7 **uHSM-Host** row):

- `SPI_MOBILE_STATUS_ALREADY_PROVISIONED (0xE4)`
- `SPI_MOBILE_STATUS_INIT_PIN_INVALID (0xE5)`
- `SPI_MOBILE_STATUS_WIPE_NONCE_INVALID (0xEF)`
- **`SPI_MOBILE_STATUS_INIT_PIN_THROTTLED (0xF5)`** (new — parent §5.1.1)
- `SPI_MOBILE_STATUS_DEVICE_WIPED (0xEE)`
- `SPI_MOBILE_STATUS_KEY_ID_NOT_READABLE (0xE1)`
- `SPI_MOBILE_STATUS_KEY_ID_ROTATE_ONLY (0xE2)`
- `SPI_MOBILE_STATUS_KEY_ID_STALE (0xE3)`
- `SPI_MOBILE_STATUS_SESSION_NOT_READY (0xED)`

## 9) Verification gates

- No legacy mobile PKCS#11 command handlers reachable from mobile dispatch.
- New commands pass positive + negative tests (bad key id, stale key id, wrong pin).
- Lockout wipe integration tested (3 bad pins).
- Power-loss mid-wipe test demonstrates eventual full wipe on reboot.
- **Init-pin throttle tested:** 3 free fails → 4th fail triggers 10 s reject → backoff extends to cap; successful `0x44` resets counter.
- **Write-order atomicity tested:** interrupt `0x44` at each of the three NvM commit points; assert retry resumes to `PROVISIONING_REQUIRED`.
- **`LOGIN_USER_V1` inner-only:** bare outer `0x45` in hardware-prod profile rejected with `0xE0`; inner-dispatch path via `0x55` succeeds.
- **New slot band read:** `READ_PUBLIC_KEY_V1 (0x61)` with `crypto_key_id = 213` (example ephemeral) returns the firmware-assigned pub without `KEY_ID_NOT_READABLE`.

## 10) Out of scope

- Host dispatch (`kMobileManager`) and status mapping details (covered by host spec).
- App UI and migration behavior (covered by app specs).
- Capability-token plumbing for `RevokeDeviceByCapability` (server-side only — firmware has no role).
