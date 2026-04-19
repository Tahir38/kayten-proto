# uHSM-Host `kMobileManager` — AI prompt

- **Date:** 2026-04-17
- **Repo scope:** `/Users/tahir/Repos/uHSM/uHSM-Host`
- **Authoritative repo spec:** [`2026-04-17-uhsm-host-kmobile-manager-spec.md`](2026-04-17-uhsm-host-kmobile-manager-spec.md)
- **Parent spec:** [`2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`](2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md)

---

```text
You are Claude (Opus or Sonnet) operating inside the `uHSM-Host` repo
at `/Users/tahir/Repos/uHSM/uHSM-Host`. This is the TC33x host-side
SPI gateway that sits between the FT4222H USB bridge and the HSM. It
is TC33x AURIX C code with its own CSM / CryIf stack today.

Your task: replace mobile PKCS#11 dispatch (`kPkcs11Manager`) with a new
`kMobileManager` semantic dispatcher. Delete `Src/PKCS11/` from the
mobile build target. Forward new semantic commands (0x60..0x68) to the
HSM without translation. Enforce the hardware-production
`LOGIN_USER_V1 (0x45)` + `CHANGE_USER_PIN_V1 (0x46)` bare-outer
rejection rule. Plumb the new `SPI_MOBILE_STATUS_INIT_PIN_THROTTLED
(0xF5)` status code through.

Week 1 work: introduce `kMobileManager` behind compile-time
`KAYTEN_HOST_MOBILE_PROFILE_V2` (default OFF). Week 3 cut-over (separate
PR) flips it ON and deletes `Src/PKCS11/`.

======================================================================
MANDATORY READING (in order)
======================================================================

1. `docs/superpowers/plans/2026-04-17-uhsm-host-kmobile-manager-spec.md`
   — authoritative scope. Read every section (it is short).

2. `docs/superpowers/plans/2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`
   — parent spec. Focus on:
     §2.1 — one dispatcher, no runtime flag
     §2.2 — delete CSM / CryIf from host as well
     §3.1 + §3.1.1 — bare-outer PIN rejection rules (LOGIN + CHANGE_USER_PIN)
     §3.2 — new command surface 0x60..0x68
     §3.3 — removed PKCS#11 opcodes
     §5.8 — 0x68 / 0x65 / 0x66 wipe flow
     §7 row "uHSM-Host" — full per-repo delta
     §9b — new slot bands (host pass-through only, no range-check)
     §11 — `rg` gates on Src/Mobile/

3. Existing host layout:
   - `Src/PKCS11/Appl/kPkcs11Manager.{c,h}` — the dispatcher you replace.
   - `Src/PKCS11/Appl/kPkcs11Manager.h:122..180` — legacy `CMD_*` opcodes
     that must become unreachable on the mobile wire.
   - `Src/PKCS11/Appl/Pkcs11ObjectRegistry.{c,h}` + attribute-template
     parsers — all go away with the directory.
   - `Src/BSW/Csm/`, `Src/BSW/CryIf/` + `*_Cfg.{c,h}` — delete from mobile
     build target (parent §2.2).

======================================================================
STEP-BY-STEP TASK LIST
======================================================================

Task 1 — Branch
  Create `feat/2026-04-17-kmobile-manager` off `develop`. All new code
  gated behind `#if KAYTEN_HOST_MOBILE_PROFILE_V2 == STD_ON` except the
  status-code header additions (always-compiled constants).

Task 2 — New files
  Create `Src/Mobile/Appl/kMobileManager.c` + `.h`:
  - Top-level entrypoint `kMobileManager_DispatchCommand(const spi_frame_t*
    frame, spi_response_t* resp)` — single function, switch on `cmd_byte`.
  - Handler functions per command. Retained commands (0x40..0x5C) delegate
    to existing HSM-IPC helpers (same as today). New commands (0x60..0x68)
    are thin pass-through to HSM IPC.
  - Error path: any command NOT in the known list returns
    `SPI_STATUS_CMD_NOT_AVAILABLE (0xE0)`.

  Create `Src/Mobile/Appl/spi_status_codes.h` with constants:
    #define SPI_STATUS_CMD_NOT_AVAILABLE              0xE0
    #define SPI_MOBILE_STATUS_KEY_ID_NOT_READABLE     0xE1
    #define SPI_MOBILE_STATUS_KEY_ID_ROTATE_ONLY      0xE2
    #define SPI_MOBILE_STATUS_KEY_ID_STALE            0xE3
    #define SPI_MOBILE_STATUS_SESSION_NOT_READY       0xED
    #define SPI_MOBILE_STATUS_DEVICE_WIPED            0xEE
    #define SPI_MOBILE_STATUS_ALREADY_PROVISIONED     0xE4
    #define SPI_MOBILE_STATUS_INIT_PIN_INVALID        0xE5
    #define SPI_MOBILE_STATUS_WIPE_NONCE_INVALID      0xEF
    #define SPI_MOBILE_STATUS_INIT_PIN_THROTTLED      0xF5   // NEW

Task 3 — Route mobile SPI entrypoint through the new dispatcher
  Find the current mobile SPI top-level handler (likely
  `Src/Mobile/Appl/mobile_spi_task.c` or similar — grep for
  `kPkcs11Manager_HandleSpiDispatch`). Under `#if
  KAYTEN_HOST_MOBILE_PROFILE_V2 == STD_ON`, route to
  `kMobileManager_DispatchCommand` instead.

Task 4 — Command surface forwarding
  In `kMobileManager_DispatchCommand`, dispatch:
    0x40, 0x41, 0x42         — existing messaging runtime (delegate)
    0x43                     — PROBE_STATE_V1 (set capability bits, Task 6)
    0x44                     — PROVISION_DEVICE_V1 (pass-through to HSM)
    0x45, 0x46               — LOGIN / CHANGE_USER_PIN (Task 5 rejection)
    0x47, 0x48               — retained (delegate)
    0x49                     — GET_RUNTIME_STATUS_V1 (pass-through)
    0x4A..0x4F, 0x53..0x5C   — retained (delegate)
    0x55                     — SECURE_EXECUTE_V1 (existing handler)
    0x60..0x68               — NEW pass-through to HSM IPC. Host does NOT
                               range-check crypto_key_id — firmware does.
                               Include `0x67` only when `#if
                               KAYTEN_HOST_DEV_FIRMWARE == STD_ON`.
    default                  — return 0xE0

Task 5 — Bare-outer PIN-command rejection (parent §3.1 / §3.1.1)
  Under `#if KAYTEN_HOST_MODEL_C_HARDWARE_PROD == STD_ON` — the
  hardware-production profile — AND when `SPI_CAP_MOBILE_PROFILE_V1`
  has been set in the last `0x43` response (local cache), reject:
    - Bare outer `0x45 LOGIN_USER_V1`  → 0xE0 (NEW — parent §3.1.1)
    - Bare outer `0x46 CHANGE_USER_PIN_V1` → 0xE0 (unchanged)
  Accept:
    - Bare outer `0x44 PROVISION_DEVICE_V1` → forward normally
      (parent §10 #14 — pre-provision `0x55` is impossible because `0x44`
       is the command that generates the identity key used to sign
       `0x55` handshake transcripts).

  Inner-dispatch (when these commands arrive via `0x55 SECURE_EXECUTE_V1`
  wrapping) continues to work unchanged — the rejection is ONLY on the
  bare outer wire.

  Dev/staging builds (where `KAYTEN_HOST_MODEL_C_HARDWARE_PROD == STD_OFF`)
  MUST keep the bare-outer fast path so bench bring-up on fresh HSMs
  without a pre-provisioned secure channel still works.

Task 6 — PROBE_STATE_V1 capability bits (parent §2.8 remap applied)
  In the 0x43 handler:
    cap_bitmap |= (1 << 5);  // KAYTEN_HSM_CAP_OPK_V1 — existing (firmware-merged
                             //  via OPK/DH4 workstream b0b4c46)
    cap_bitmap |= (1 << 6);  // SPI_CAP_MOBILE_PROFILE_V1 — NEW: advertise
                             //  when kMobileManager is the active dispatcher
                             //  (guard on KAYTEN_HOST_MOBILE_PROFILE_V2 STD_ON).
                             //  Parent §2.8: was bit 5 pre-merge, moved to 6
                             //  because firmware owns bit 5.
    cap_bitmap |= (1 << 7);  // SPI_CAP_DEV_FIRMWARE — set when the host
                             //  is built for dev firmware pairing.
                             //  Parent §2.8: was bit 6 pre-merge, moved to 7.

  Existing pre-merge bit assignments (do not touch): bit 0 SECURE_EXECUTE_V1,
  bit 1 CALL_KEY_AGREE, bit 2 MSG_KEY_EXCHANGE_V1, bit 3 DEDICATED_SLOTS_V1,
  bit 4 PKCWAIT_TIMEOUT, bit 5 OPK_V1 (OPK/DH4 shipped).

Task 7 — Status-code pass-through
  When HSM responses carry `0xF5 INIT_PIN_THROTTLED`, `0xEF WIPE_NONCE_INVALID`,
  etc., host forwards unchanged. Do NOT remap into the legacy
  `SPI_MOBILE_STATUS_AUTH_FAILED` bucket.

Task 8 — Delete legacy modules (Week 3 readiness)
  Under `#if KAYTEN_HOST_MOBILE_PROFILE_V2 == STD_ON`, ensure these
  `rg` gates pass (parent §11):
    rg "kPkcs11Manager" Src/Mobile/                       # 0 hits
    rg "GET_ATTRIBUTE_VALUE|FIND_OBJECTS|SESSION_HANDLE" Src/Mobile/  # 0 hits
    rg "Csm_|CsmKey_|CryIf_|CryIfKey_" Src/Mobile/        # 0 hits

  For Week 1 STD_OFF: the `Src/PKCS11/` directory MAY remain linked in
  for the lab A/B target, but must be unreachable from
  `Src/Mobile/` when the gate is on. The app-shipped binary deletes
  `Src/PKCS11/` entirely in Week 3.

Task 9 — Tests
  Update / add tests:
  - Command dispatch coverage (0x43, 0x44, 0x45, 0x46, 0x49, 0x55, 0x60..0x68).
  - Bare-outer `0x45` in hardware-prod profile → 0xE0; in dev profile → forwarded.
  - Legacy PKCS#11 opcode (e.g. 0x33) → 0xE0 in STD_ON build.
  - Status-code pass-through: inject HSM 0xF5 response; assert app-side
    receives 0xF5 unchanged.
  - `0x68` / `0x65` / `0x66` wipe integration (already in prior spec).

======================================================================
SELF-VERIFICATION BEFORE PR
======================================================================

  # Builds.
  ./build/scripts/build_host_prod.sh && ./build/scripts/build_host_dev.sh
  # => both succeed

  # New dispatcher compiled in.
  nm build/host_prod/uHSM-Host.elf | grep kMobileManager_DispatchCommand
  # => 1 hit

  # No kPkcs11Manager on mobile path when STD_ON.
  rg "kPkcs11Manager" Src/Mobile/
  # => 0 hits

  # New status code present in header.
  rg "SPI_MOBILE_STATUS_INIT_PIN_THROTTLED\s+0xF5" Src/Mobile/Appl/spi_status_codes.h
  # => 1 hit

  # Bare-outer LOGIN_USER_V1 rejected under hw-prod profile.
  ./build/scripts/run_mobile_tests.sh --filter=login_bare_outer_rejection
  # => PASS

======================================================================
GUARDRAILS
======================================================================

- `kMobileManager` MUST NOT import any `Csm_*` / `CryIf_*` / `Pkcs11*`
  symbols. Host-side secure-channel ECDH for `0x55` becomes a direct
  `Crypto_kHW` call.
- Host MUST NOT range-check `crypto_key_id` on forward paths — firmware
  owns the truth. Host just passes the 4-byte little-endian value through.
- Bare-outer `0x44` MUST remain accepted in hw prod (parent §10 #14 /
  §3.1.1). Rejecting it would brick fresh HSMs.
- Rejection of bare-outer `0x45`/`0x46` MUST be gated on advertisement
  of `SPI_CAP_MOBILE_PROFILE_V1` — otherwise dev hosts with older apps
  break.

======================================================================
WHEN DONE, RETURN
======================================================================

- Branch name + head SHA.
- Build logs for prod + dev host profiles.
- Before/after command dispatch table (what routes to which handler).
- `rg` output for parent §11 gates (0 hits confirmation).
- Test summary.
- Risk list.
```
