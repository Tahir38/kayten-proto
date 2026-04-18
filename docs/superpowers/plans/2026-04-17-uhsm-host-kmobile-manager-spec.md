# uHSM-Host — kMobileManager spec

- Date: 2026-04-17
- Scope repo: `/Users/tahir/Repos/uHSM/uHSM-Host`
- Parent: `2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`

## 1) Objective

Replace mobile PKCS#11 dispatch with `kMobileManager` semantic dispatch and delete `Src/PKCS11/` from the mobile path.

## 2) Required architecture

- Add `Src/Mobile/Appl/kMobileManager.c`
- Add `Src/Mobile/Appl/kMobileManager.h`
- Main mobile SPI dispatch entrypoint delegates to `kMobileManager_DispatchCommand`.
- Legacy `kPkcs11Manager_HandleSpiDispatch` removed from mobile profile.

## 2.1) Sequencing vs parent §8

- **Week 1 Branch C:** introduce `kMobileManager` behind compile-time `KAYTEN_HOST_MOBILE_PROFILE_V2` (default OFF). Optional **lab-only** second build target may retain `Src/PKCS11/` for bench A/B — not the production app-shipped binary. **No** production runtime toggle that keeps PKCS#11 on the SPI path (contradicts parent §2.1).
- **Week 3:** mobile build **deletes** `Src/PKCS11/` entirely; `rg kPkcs11Manager` on `Src/Mobile/` must be clean per parent §11.

## 3) Remove legacy modules from mobile profile

- Remove usage of `Src/PKCS11/` for mobile dispatch (directory absent after cut-over).
- Remove host-side key registry and PKCS#11 object-handle translation for mobile.
- Remove host-side CSM/CryIf key-id rewriting from mobile command path (parent §7 **uHSM-Host** row).

## 4) Semantic command support

Support retained commands + add:
- `0x60..0x64` key management/random
- `0x65` user-initiated wipe (pass-through to HSM; outer framing per Model C)
- `0x66` wipe status
- `0x67` dev-only init-pin set (pass-through; firmware-gated)
- `0x68` request wipe challenge (pass-through; auth-gated on HSM)

## 5) Status codes

Define and expose:
- `SPI_STATUS_CMD_NOT_AVAILABLE (0xE0)`
- `SPI_MOBILE_STATUS_KEY_ID_NOT_READABLE (0xE1)`
- `SPI_MOBILE_STATUS_KEY_ID_ROTATE_ONLY (0xE2)`
- `SPI_MOBILE_STATUS_KEY_ID_STALE (0xE3)`
- `SPI_MOBILE_STATUS_SESSION_NOT_READY (0xED)`
- `SPI_MOBILE_STATUS_DEVICE_WIPED (0xEE)`
- `SPI_MOBILE_STATUS_ALREADY_PROVISIONED (0xE4)`
- `SPI_MOBILE_STATUS_INIT_PIN_INVALID (0xE5)`
- `SPI_MOBILE_STATUS_WIPE_NONCE_INVALID (0xEF)`
- **`SPI_MOBILE_STATUS_INIT_PIN_THROTTLED (0xF5)`** — parent §5.1.1 new status; host passes through HSM-generated 0xF5 to the app without translation.

## 6) Capability bits behavior

`PROBE_STATE_V1 (0x43)` capability bitmap must surface:
- `SPI_CAP_MOBILE_PROFILE_V1 (1<<6)` — advertises semantic mobile profile (enables app-side bare-outer `0x45` / `0x46` rejection rules, parent §3.1 / §3.1.1). **Parent §2.8 remap: was bit 5 pre-merge, moved to bit 6 because firmware bit 5 is `KAYTEN_HSM_CAP_OPK_V1`.**
- `SPI_CAP_DEV_FIRMWARE (1<<7)` — when applicable (dev-resettable build). **Parent §2.8 remap: was bit 6 pre-merge, moved to bit 7.**

## 7) Bare-outer PIN-command rejection (hardware prod)

Once `SPI_CAP_MOBILE_PROFILE_V1` is advertised AND the host is built with `KAYTEN_HOST_MODEL_C_HARDWARE_PROD = STD_ON`:

- Bare outer **`0x45 LOGIN_USER_V1`** → host rejects with `SPI_STATUS_CMD_NOT_AVAILABLE (0xE0)` **before** forwarding to HSM. Must use the inner-dispatch path of `0x55 SECURE_EXECUTE_V1` (parent §3.1 / §3.1.1 / §4).
- Bare outer **`0x46 CHANGE_USER_PIN_V1`** → same rule (unchanged from prior spec).
- Bare outer **`0x44 PROVISION_DEVICE_V1`** → **accepted** (parent §10 #14 / §3.1.1: `0x55` requires an identity key that `0x44` is the command that generates — pre-provision `0x55` is physically impossible).

Dev/staging builds keep bare-outer fast paths for all three so fresh HSM bring-up works without a pre-provisioned secure channel.

## 8) Verification

- No mobile calls route through `kPkcs11Manager`.
- Wire rejects legacy PKCS#11 mobile commands with `SPI_STATUS_CMD_NOT_AVAILABLE`.
- **`CHANGE_USER_PIN_V1 (0x46)`:** when `SPI_CAP_MOBILE_PROFILE_V1` is advertised,
  bare outer `0x46` (not inner to `0x55`) returns `SPI_STATUS_CMD_NOT_AVAILABLE
  (0xE0)` per parent §3.1 — host must not forward plaintext PIN on SPI in prod.
- **`LOGIN_USER_V1 (0x45)` (NEW):** same rule as `0x46`; bare outer rejected in hardware prod; inner-dispatch accepted (parent §3.1 / §3.1.1 / §4).
- `0x33 GET_ATTRIBUTE_VALUE` not used by app path.
- `0x61 READ_PUBLIC_KEY_V1` replaces legacy public-key read behavior.
- **`0x61` accepts new bands** (parent §9b): secure-channel `110..117`, voice-ephemeral `118..129`, conversation `130..191`, ephemeral `192..239` — host does not range-check; just forwards `crypto_key_id` to HSM.
- `0x68` / `0x65` / `0xEF` paths covered by integration tests (parent §5.8, §11).
- **`0xF5 INIT_PIN_THROTTLED`** passed through from HSM unchanged; host-side integration test asserts the app receives it and surfaces a "try again in N seconds" UX hint (parent §5.1.1).

## 9) Parent alignment checklist

| Topic | Parent section |
|---|---|
| Command surface `0x60..0x68`, status `0xEF` + new `0xF5` | §3.2, §5.1.1, §5.8, §7 **uHSM-Host** |
| Bare-outer `0x45` + `0x46` rejection | §3.1 / §3.1.1 / §4 |
| New slot bands (pass-through only) | §9b |
| Week 1 vs Week 3 PKCS#11 directory | §8 Branch C |
| `rg` / directory removal gates | §11 |
