# uHSM-HSM mobile profile v2 — AI prompt

- **Date:** 2026-04-17
- **Repo scope:** `/Users/tahir/Repos/uHSM/uHSM-HSM`
- **Authoritative repo spec:** [`2026-04-17-uhsm-hsm-mobile-profile-v2-spec.md`](2026-04-17-uhsm-hsm-mobile-profile-v2-spec.md)
- **Parent spec:** [`2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`](2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md)
- **Upstream dependency:** `2026-04-17-kayten-proto-hsm-keys-constants-spec.md` MUST be merged first (provides canonical `CryptoKeyId` band boundaries and `MobileCommandId` numeric values that firmware mirrors).

---

```text
You are Claude (Opus or Sonnet) operating inside the `uHSM-HSM` firmware
repo at `/Users/tahir/Repos/uHSM/uHSM-HSM`. This is TC33x AURIX firmware
in C, using the AUTOSAR stack (today) with NvM for persistent storage.

Your task: implement the firmware side of PKCS#11 retirement for the
mobile SPI surface. Introduce a semantic command dispatcher, delete the
CSM / CryIf AUTOSAR layers from the mobile path, and enforce
lockout-triggered key wipe semantics plus the new init-pin throttle and
write-order atomicity contract.

This work lands behind compile-time gate `KAYTEN_HSM_MOBILE_PROFILE_V2`.
Week 1 merges with it default STD_OFF for bisect safety. Week 3 cut-over
(separate PR) flips it STD_ON and deletes the legacy PKCS#11 handlers.

======================================================================
MANDATORY READING (in order)
======================================================================

1. `docs/superpowers/plans/2026-04-17-uhsm-hsm-mobile-profile-v2-spec.md`
   — authoritative scope for this repo. Pay special attention to:
     §2.3  — introduce write-order contract (§5.1.2) and throttle (§5.1.1)
     §2.7  — reserve NVM slot bands 110..239 (new, parent §9b)
     §4    — command contracts for 0x60..0x68
     §4.1  — RAM wipe-challenge state + NVM throttle state
     §5.1  — PROVISION_DEVICE_V1 flow with NVM write ordering
     §5.1.1 — throttle backoff table and implementation
     §5.2  — LOGIN_USER_V1 inner-only rule (parent §3.1.1 / §4)
     §6    — Kayten_WipeAllKeyMaterial scope (now includes new bands)
     §8    — status codes to export from kHsm_Kayten.h
     §9    — verification gates

2. `docs/superpowers/plans/2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`
   — parent spec. Read §2 (design principles), §3 (command surface),
     §3.1.1 (PIN-protection rules), §4 (inner allow-list — note
     KAYTEN_CMD_LOGIN_USER_V1 is newly added), §5 (wire contracts),
     §5.1 / §5.1.1 / §5.1.2 (provision + throttle + write order),
     §6 / §6a (state machine + wipe), §9b (new NVM slot bands),
     §10 (security considerations), §11 (`rg` gates you must pass).

3. Existing firmware layout:
   - `Src/BSW/kHsm/kHsm_Kayten.c` — today's PKCS#11-shaped dispatcher;
     ~6 `default:` fall-through branches at lines 5368..6181 that rewrite
     CsmKey_* → CryIfKey_* → Crypto_kHW*. Remove these.
   - `Src/BSW/Csm/`, `Src/GenData/Csm_Cfg.{c,h}`, `Src/GenData/CryIf_Cfg.{c,h}`,
     `Src/BSW/SchM/SchM_Csm.h`, `Src/BSW/SchM/SchM_CryIf.h`,
     `Rte_Csm_Type.h` — all slated for deletion (Week 3 cut-over; you
     may leave them in place for Week 1 behind STD_OFF gate).
   - NVM layout header (typically `Src/BSW/NvM/NvM_Cfg.h` or
     `Src/GenData/NvM_Cfg.h`) — where block-ids are defined.

======================================================================
STEP-BY-STEP TASK LIST
======================================================================

Task 1 — Branch
  Create branch `feat/2026-04-17-hsm-mobile-profile-v2` off `develop`.
  Gate all new code behind `#if KAYTEN_HSM_MOBILE_PROFILE_V2 == STD_ON`
  except the NVM block-id reservations (those are layout constants, safe
  to always compile).

Task 2 — New files
  a) `Src/BSW/kHsm/Kayten_Crypto.c` + `.h` — single call-site for mobile
     crypto. Exposes:
       Kayten_Crypto_GenerateKeyPair(u32 priv_id, u32 pub_id, u8 algo) -> status
       Kayten_Crypto_EcdhCompute(u32 priv_id, const u8* peer_pub, u8* out_shared) -> status
       Kayten_Crypto_EcdsaSign(u32 priv_id, const u8* msg, u16 len, u8* out_sig) -> status
       Kayten_Crypto_AesGcmEncrypt(u32 key_id, const u8* iv, const u8* aad, u16 aad_len,
                                   const u8* pt, u16 pt_len, u8* out_ct, u8* out_tag) -> status
       Kayten_Crypto_AesGcmDecrypt(...)  -- mirror
       Kayten_Crypto_HkdfExpand(u32 prk_id, const u8* info, u16 info_len,
                                u8* out_okm, u16 okm_len, u32 out_key_id) -> status
       Kayten_Crypto_TrngBytes(u8* out, u16 len) -> status
     All calls route to `Crypto_kHW` / `Crypto_kSW` primitives directly —
     no `Csm_*` or `CryIf_*` references. Keep ~500 LOC total.

  b) `Src/BSW/kHsm/Kayten_Mobile.c` — semantic command handlers for
     0x60..0x68 (see Task 5). Include `Kayten_WipeAllKeyMaterial(reason)`.

Task 3 — NVM layout additions (parent §2.8 + repo §2.7.1 kOpk-pattern)
  Edit `Src/GenData/NvM_Cfg.{c,h}` to reserve block ids per repo spec
  §2.7.1 table (kOpk precedent at block ids 113..132 stays intact):

    | Band                        | crypto_key_id | NvM block ids | Count | Block size |
    |-----------------------------|---------------|---------------|-------|------------|
    | Identity key pair           | 22/23         | existing      |  1    | 96 B       |
    | SPK band                    | 35..44        | existing      |  5    | 96 B/pair  |
    | MSG bootstrap secret        | 64            | existing      |  1    | 32 B       |
    | OPK band (shipped 2026-04-16)| 70..109      | 113..132      | 20    | 96 B/pair  |
    | Secure-channel (NEW)        | 110..117      | 133..140      |  8    | 96 B/pair  |
    | Voice-ephemeral (NEW)       | 118..129      | 141..152      | 12    | 96 B/pair  |
    | Conversation (NEW)          | 130..191      | 153..214      | 62    | 96 B/pair  |
    | Ephemeral (NEW)             | 192..239      | 215..262      | 48    | 96 B/pair  |
    | init_pin_fail_count (NEW)   | (non-key)     | 263           |  1    | 1 B        |
    | last_failed_init_pin_unix (NEW)| (non-key)  | 264           |  1    | 8 B        |
    | USER_PIN_BLOCK              | (non-key)     | existing      |  1    | ~          |
    | RUNTIME_STATE_BLOCK         | (non-key)     | existing      |  1    | ~          |

  Bump `NVM_TOTAL_NUM_OF_BLOCK` to 265 (from current 135 after kOpk).
  Add matching FEE descriptor rows in `Src/GenData/Fee_Cfg.c` per the
  kOpk commit `075add5` pattern ("20 matching FEE blocks for kOpk NvM
  band") — size FEE blocks by concrete record byte count, not a generic
  default.

  Reserve descriptors, don't pre-populate; firmware accesses them
  on-demand when the Host writes to a new-band id.

  If a band exceeds ~15 slots (conversation 62, ephemeral 48), create a
  dedicated module directory under `Src/BSW/k<BandName>/` mirroring
  `Src/BSW/kOpk/` structure (see repo spec §2.7.1). Small bands
  (secure-channel 8, voice-ephemeral 12) may stay inline in
  `Kayten_Mobile.c`.

Task 4 — Implement `Kayten_WipeAllKeyMaterial(reason)`
  Per repo spec §6 and parent §6a.2:
  1. Zeroize RAM: voice-mode state, epoch keys, secure-execute transport
     keys, bootstrap-secret RAM copy, wipe-challenge nonce if pending.
  2. Walk `Kayten_Crypto_KeyStore[]` and zeroize every active entry in:
     - identity (22, 23)
     - SPK band (35..44)
     - OPK band (70..109)
     - msg bootstrap (64)
     - new bands (110..239) if populated
     - SRK
  3. For each zeroized entry, `NvM_WriteBlock` its NVM slot with 0x00
     bytes; wait for `NvM_MultiBlockJob_Callout(NVM_WRITE_BLOCK)` OK.
  4. Clear `KAYTEN_NVM_USER_PIN_BLOCK`: zero PIN bytes, initialized=0,
     retries=3.
  5. RETAIN `init_pin_block` (factory-fused) AND `init_pin_fail_count`
     AND `last_failed_init_pin_unix` (throttle state is brute-force
     defense, not user data).
  6. Write `KAYTEN_NVM_RUNTIME_STATE_BLOCK`:
       last_wipe_reason = reason
       last_wipe_unix   = kCurrentUnix()
       provisioning_state = PROVISIONING_REQUIRED
  7. Emit `KAYTEN_DET_WIPE_COMPLETED`.
  Recovery heuristic (§6a.2 final paragraph): on boot, if
  `provisioning_state == PROVISIONED && user_pin_initialized == 0`, run
  `Kayten_WipeAllKeyMaterial(PIN_LOCKOUT_RECOVERY)` before accepting cmds.

Task 5 — Implement mobile handlers in `Kayten_Mobile.c`

  5.1 `0x44 PROVISION_DEVICE_V1` — clean-sheet handler per repo spec §5.1:
      1. Reject if state != PROVISIONING_REQUIRED → 0xE4
      2. Throttle pre-check (repo spec §5.1.1):
           delay = throttle_delay_s[min(init_pin_fail_count, 6)];
           if (now - last_failed_init_pin_unix < delay) return 0xF5;
         (do NOT even read init_pin on throttle — prevents timing oracle)
      3. Constant-time compare init_pin. On mismatch: increment counter +
         persist last_failed_init_pin_unix + return 0xE5.
      4. On match: reset counter to 0 + persist.
      5. NVM write order, each atomic (repo spec §5.1 code block):
           step 5a: USER_PIN_BLOCK     — wait for NvM OK
           step 5b: IDENTITY_KEY_PAIR  — call Kayten_Crypto_GenerateKeyPair
                                        (priv=22, pub=23, ALGO_ED25519)
                                        then wait for NvM OK
           step 5c: RUNTIME_STATE      — state=PROVISIONED (COMMIT MARKER)
                                        wait for NvM OK
      6. Emit {status=OK, provisioning_state=PROVISIONED, identity_pub:32}.

  5.2 `0x45 LOGIN_USER_V1`:
      - Bare outer rejected with 0xE0 in hardware-prod profile when
        SPI_CAP_MOBILE_PROFILE_V1 is advertised (parent §3.1.1 / §4).
        Add to the outer-dispatch gate alongside `0x46`.
      - Inner dispatch (called from `0x55 SECURE_EXECUTE_V1` handler):
        run standard login flow — 3-strikes → Kayten_WipeAllKeyMaterial(
        PIN_LOCKOUT) → return 0xEE (DEVICE_WIPED).
      - Dev/staging keep bare-outer fast path (same #if gate style as 0x46).

  5.3 `0x46 CHANGE_USER_PIN_V1` — unchanged from prior spec (inner-only
      in hw prod).

  5.4 `0x49 GET_RUNTIME_STATUS_V1` — append new fields:
        [provisioning_state:1][last_wipe_reason:1][remaining_pin_retries:1]

  5.5 `0x60 GENERATE_MESSAGING_KEY_V1`:
      - kind=SPK: allocate next free SPK ring slot, generate P-256,
        sign pub with identity key, demote old current→previous, zeroize
        older-than-previous (master §5.5 retention: ≤2 privates non-zero).
      - kind=EPHEMERAL_ECDH: allocate from ephemeral band (192..239),
        generate P-256, return {priv_id, pub_id, pub:64, zero-sig:64}.

  5.6 `0x61 READ_PUBLIC_KEY_V1` — accept crypto_key_id in the permitted
      bands (parent §5.3 + new bands §9b):
        23 (identity pub), 36/38/40/42/44 (SPK pub), 71,73,…,109 (OPK pub),
        118..129 pub-half (voice ephemeral), 193,195,…,239 (ephemeral pub)
      Return public_key_len + raw public bytes. Reject other ids with 0xE1.

  5.7 `0x62 DELETE_MESSAGING_KEY_V1` — permit OPK private + ephemeral-band
      private + conversation-band private. Reject SPK private with 0xE2
      (must use 0x63). Reject identity with 0xE1.

  5.8 `0x63 ROTATE_SPK_V1` — atomic per master §5.5 (generate new, demote
      current→previous, zeroize older-than-previous).

  5.9 `0x64 GENERATE_RANDOM_V1` — `Kayten_Crypto_TrngBytes`, bounded ≤ 512B.
      Auth-session gated.

  5.10 `0x65 USER_INITIATED_WIPE_V1` — per master §5.8:
       - Verify RAM wipe_nonce matches AND now <= valid_until. Wrong/
         expired → 0xEF (WIPE_NONCE_INVALID).
       - Verify user_pin (reuse login compare).
       - Burn nonce THEN call Kayten_WipeAllKeyMaterial(USER_REQUEST).
       Must be SECURE_EXECUTE-wrapped in hw prod.

  5.11 `0x66 GET_WIPE_STATUS_V1` — pre-auth, returns {last_wipe_reason,
       last_wipe_unix} from RUNTIME_STATE_BLOCK.

  5.12 `0x67 DEV_SET_INIT_PIN_V1` — only compiled when
       KAYTEN_INIT_PIN_SOURCE == KAYTEN_INIT_PIN_DEV_RESETTABLE.
       Requires state == PROVISIONING_REQUIRED. Rewrites init_pin NVM.

  5.13 `0x68 REQUEST_WIPE_CHALLENGE_V1` — auth-session required. TRNG
       32-byte nonce, TTL 120s, single-shot RAM. Cleared on logout,
       successful 0x65, or superseding challenge.

Task 6 — Extend `KAYTEN_IS_ALLOWED_INNER_CMD` for 0x55
  Add to the allow-list in `kHsm_Kayten.h`:
    KAYTEN_CMD_LOGIN_USER_V1            -- NEW (parent §4 / §3.1.1)
    KAYTEN_CMD_CHANGE_USER_PIN_V1
    KAYTEN_CMD_ENCRYPT_AND_SIGN
    KAYTEN_CMD_DECRYPT_AND_VERIFY
    KAYTEN_CMD_FULL_RATCHET_STEP
    KAYTEN_CMD_VOICE_ENCRYPT_FRAME
    KAYTEN_CMD_VOICE_DECRYPT_FRAME
    KAYTEN_CMD_CALL_KEY_AGREE
    KAYTEN_CMD_CALL_TEARDOWN
    KAYTEN_CMD_IDENTITY_SIGN
    KAYTEN_CMD_MSG_KEY_EXCHANGE
    KAYTEN_CMD_MSG_KEY_EXCHANGE_RESPONDER
    KAYTEN_CMD_PEER_RATCHET_STEP
    KAYTEN_CMD_GENERATE_OPK
    KAYTEN_CMD_GENERATE_MESSAGING_KEY
    KAYTEN_CMD_READ_PUBLIC_KEY
    KAYTEN_CMD_DELETE_MESSAGING_KEY
    KAYTEN_CMD_ROTATE_SPK
    KAYTEN_CMD_GENERATE_RANDOM
    KAYTEN_CMD_USER_INITIATED_WIPE
    KAYTEN_CMD_GENERATE_EPOCH_KEY
    KAYTEN_CMD_HKDF_DERIVE
  Also update `Kayten_GetInnerResponseLen` in `kHsm_Kayten.c` with
  entries for every new inner command (mirrors the existing pattern;
  missing entries cause the SECURE_EXECUTE wrapper to silently drop the
  response — see CLAUDE.md 2026-04-16 fix).

Task 7 — Export status codes in `kHsm_Kayten.h`
  Add/verify constants: 0xE0, 0xE1, 0xE2, 0xE3, 0xED, 0xEE, 0xE4, 0xE5,
  0xEF, AND **0xF5 SPI_MOBILE_STATUS_INIT_PIN_THROTTLED** (new).

Task 8 — Capability bit in PROBE_STATE_V1 response
  Set `SPI_CAP_MOBILE_PROFILE_V1 = 1 << 6` (parent §2.8 remap — firmware bit 5 is `KAYTEN_HSM_CAP_OPK_V1`) whenever this build includes
  the semantic dispatch (i.e. `KAYTEN_HSM_MOBILE_PROFILE_V2 == STD_ON`).
  Set `SPI_CAP_DEV_FIRMWARE = 1 << 7` (parent §2.8 remap) when `KAYTEN_INIT_PIN_SOURCE ==
  KAYTEN_INIT_PIN_DEV_RESETTABLE`.

Task 9 — Delete CSM / CryIf (Week 3 cut-over readiness)
  Under `#if KAYTEN_HSM_MOBILE_PROFILE_V2 == STD_ON`, ensure `rg` gates
  from parent §11 pass on the build artifact:
    rg "Csm_|CsmKey_|CryIf_|CryIfKey_" Src/BSW/kHsm/ Src/BSW/Kayten_Crypto*
    # => 0 hits
    test -d Src/BSW/Csm    # non-existent (Week 3)
  For Week 1 (default STD_OFF), files may still exist — but `Kayten_Mobile.c`
  and `Kayten_Crypto.c` MUST compile cleanly without any `Csm_*` /
  `CryIf_*` references even when the gate is off.

Task 10 — Tests
  Write or update unit tests for:
  - Fresh provision (`0x44` happy path) — state transitions REQUIRED → PROVISIONED.
  - Init-pin throttle — 3 free fails, 4th triggers 10s, backoff escalates
    per table, reset on success.
  - Power-loss mid-provision — crash between steps 5a/5b/5c; retry recovers.
  - 3-strikes login → wipe — assert identity + SPK + OPK + bootstrap NVM blocks
    all zero post-wipe; throttle state preserved.
  - Silent-wipe power-loss — interrupt wipe mid-walk; boot recovery completes.
  - `0x45` bare outer in hw-prod profile → 0xE0.
  - `0x45` via `0x55` inner dispatch → standard login flow succeeds.
  - `0x61` accepts new-band ids; rejects out-of-band with 0xE1.
  - `0x68` nonce single-shot; replay after 0x65 success returns 0xEF.

======================================================================
SELF-VERIFICATION BEFORE PR
======================================================================

  # Firmware builds cleanly for both dev and prod profiles.
  ./build/scripts/build_prod.sh && ./build/scripts/build_dev.sh
  # => both succeed

  # 0x67 handler only in dev binary.
  nm build/dev/uHSM-HSM.elf  | grep -E "HandleDevSetInitPin|Kayten_ProcessDevSetInitPin"
  # => 1+ hits
  nm build/prod/uHSM-HSM.elf | grep -E "HandleDevSetInitPin|Kayten_ProcessDevSetInitPin"
  # => 0 hits

  # No CSM / CryIf leakage on mobile path in STD_ON build.
  rg "Csm_|CsmKey_|CryIf_|CryIfKey_" Src/BSW/kHsm/ Src/BSW/Kayten_*
  # => 0 hits

  # New status code exported.
  rg "SPI_MOBILE_STATUS_INIT_PIN_THROTTLED\s+0xF5" Src/BSW/kHsm/kHsm_Kayten.h
  # => exactly 1 hit

  # Inner allow-list updated.
  rg "KAYTEN_CMD_LOGIN_USER_V1" Src/BSW/kHsm/kHsm_Kayten.h
  # => at least 1 hit (allow-list) + 1 hit (GetInnerResponseLen)

  # Unit tests pass (the repo's test harness command — likely `cmake --build build --target test` or similar).
  ./build/scripts/run_unit_tests.sh
  # => all pass

======================================================================
GUARDRAILS
======================================================================

- Every new code path gated behind `#if KAYTEN_HSM_MOBILE_PROFILE_V2 == STD_ON`
  (except NVM layout constants).
- No `Csm_*` or `CryIf_*` references in any new file (`Kayten_Crypto.*`,
  `Kayten_Mobile.*`).
- Throttle state (`init_pin_fail_count` + `last_failed_init_pin_unix`)
  MUST survive `Kayten_WipeAllKeyMaterial`.
- Write-order in `0x44`: USER_PIN → IDENTITY → RUNTIME_STATE. Never in
  different order.
- `0x67 DEV_SET_INIT_PIN_V1` MUST be `#if`-guarded, not runtime-flagged.
  A PROD firmware image that somehow receives `0x67` MUST return 0xE0
  because no handler is linked in.
- `Kayten_GetInnerResponseLen` MUST have an entry for every inner cmd
  added to `KAYTEN_IS_ALLOWED_INNER_CMD` — missing entry = silent drop.

======================================================================
WHEN DONE, RETURN
======================================================================

- Branch name + head SHA.
- Build logs for dev + prod profiles.
- Test results summary.
- Diff stats (lines added / removed per file).
- Confirmation that:
  * CLAUDE.md `rg` gates from parent §11 pass.
  * PROBE_STATE returns `SPI_CAP_MOBILE_PROFILE_V1 = 1`.
  * Host-side consumers remain source-compatible (no uHSM-Host changes
    in this PR; those land in the sibling `kMobileManager` PR).
- Risk list — anything discovered that the spec did not cover.
```
