# Cross-repo spec — `kMobileManager` introduction and full PKCS#11 retirement from the mobile SPI path

- **Date:** 2026-04-17
- **Status:** design draft, awaiting approval
- **Supersedes / subsumes:**
  - `2026-04-16-cross-repo-opk-nvm-band-and-dh4-spec.md` §3.1 (`GENERATE_KEY_PAIR 0x27` trailer, PKCS#11-shaped) — the trailer lives on a command id that this spec deletes from the mobile wire; the `crypto_key_id` payload is re-homed on a semantic replacement (§3.6).
  - `2026-04-15-cross-repo-secure-messaging-and-voice-system-solution-spec.md` §7.2 host-dispatch table — that table is rewritten by §3 of this spec.
  - Legacy spec `docs/v2.1/uhsm_host_mobile_gateway_spec_2026-03-27.md` (documentation-only)
- **Non-goals:** no new `kayten-server` RPCs or services; no breaking renumber/removal of existing proto fields. Additive message fields only where required (e.g. `GetIdentityKeyResponse.revoked_at` in §9a). `DeviceService.RevokeDevice` already sends `DEVICE_REVOKED` + `KEY_CHANGE_ALERT` (`kayten-server/internal/device/service.go:330-394`); the server *does* gain small logic additions in the revoke path (mark all `prekeys` used for the device, queue cleanup, `revoked_at` surfacing) — see §6a.4 / §7 **kayten-server** row and §9a.
- **No backward compatibility** on the wire. Mobile clients older than this release refuse to bootstrap against a post-release host. Factory/repair tooling must migrate to the new semantic surface — the existing `kPkcs11Manager` source is **deleted** (not retained behind a flag — per user direction; see §2.4).

## 1. Motivation

The mobile ↔ HSM SPI surface currently mixes two incompatible programming models:

1. **PKCS#11 cargo-cult** — a 40+ command handler set (`C_InitToken`, `C_OpenSession`, `C_FindObjectsInit/FindObjects/FindObjectsFinal`, `C_GetAttributeValue`, `C_SignInit/Sign*`, `C_VerifyInit/Verify*`, `C_Encrypt*/C_Decrypt*`, `C_WrapKey`, `C_UnwrapKey`, `C_DeriveKey`, `C_GenerateKeyPair`, `C_DestroyObject`, `C_GenerateRandom`, …) with multi-call stateful lifecycles (`Init` → `Update` → `Final`), host-side object registries keyed by per-session handles, and attribute-template serialization (`CKA_EC_POINT` blobs).
2. **Kayten-native semantic commands** — single-shot commands named after the use-case (`READ_IDENTITY_PUBLIC_V1`, `IDENTITY_SIGN_V1`, `CALL_KEY_AGREE_V1`, `MSG_KEY_EXCHANGE_V1`, `GENERATE_EPOCH_KEY`, `SECURE_EXECUTE_V1`, …) that take `crypto_key_id`s directly and encode the full operation in a single request/response.

The PKCS#11 model was imported from the uHSM platform's automotive heritage; the messaging app does not need it, has never used its full generality, and has accumulated six unavoidable pain-points from it:

| # | Pain | Evidence |
|---|------|----------|
| 1 | **Deprecated 2-byte forms still shipped.** `HsmPlugin.handleGetPublicKey` for non-identity slots emits `A5 5A 02 00 33 01 <crc>` which the host rejects with `status=0xFF` (observed in latest HSM-setup log). | `android/app/src/main/kotlin/com/kayten/uhsm/models/HsmCommand.kt` `Legacy.GetPublicKey`; `/Users/tahir/Repos/uHSM/uHSM-Host/Src/PKCS11/Appl/kPkcs11Manager.h` `spi_get_attribute_cmd_t` (>=16 B required). |
| 2 | **Dart-level logical slots with no firmware mapping.** `kSlotSignedPrekey = 1` has no corresponding firmware CSM id; firmware SPK band is `[35..43]`. | `lib/core/constants/crypto_constants.dart`, `uHSM-HSM/Src/BSW/kHsm/kHsm_Kayten.c:4828-4846`. |
| 3 | **Three-step `FindObjects` flow to translate app slot → PKCS#11 handle → CSM id**, then a 4th roundtrip to read the public component. Four SPI transactions to retrieve one public key; each intermediate cache lives on the host and drifts from firmware truth. | `android/app/src/main/kotlin/com/kayten/uhsm/hsm/Pkcs11ObjectResolver.kt`, `uHSM-Host/.../kPkcs11Manager.c` `kPkcs11Manager_HandleFindObjects*`. |
| 4 | **CSM/CryIf host-side key-id rewriting.** Every PKCS#11 flow goes through `Pkcs11ObjectRegistry` → `CsmKey_*` → `CryIfKey_*`, producing three names for one concept. Inconsistencies surface as `default: assigned_cryif_key_id = CryIfKey_EcdsaPrivateKey_1` fallbacks, which silently collapse distinct key slots onto slot 1. | 6 `default:` branches in `kPkcs11Manager.c:5368..6181`. |
| 5 | **OPK pool starvation** pre-OPK-band spec — 5-slot PKCS#11 ECDSA pool shared between SPK, ephemerals, and 10 OPKs. Documented but only half-fixed by the 2026-04-16 OPK/DH4 workstream (which still carries `C_GenerateKeyPair` on the wire). | `2026-04-16-cross-repo-opk-nvm-band-and-dh4-spec.md` §1.2. |
| 6 | **Status-code translation layer duplicates every error** (`KAYTEN_STATUS_*` → `MapKaytenMsgKeyExchangeStatus` → `SPI_STATUS_*` → `SPI_MOBILE_STATUS_*`). | `uHSM-Host/.../kPkcs11Manager.c` dispatch-site status wrappers. |

The user's direction: **"wir must not use pkcs11 functions for our messaging app. … create a new `kMobileManager` on host side. `kPkcs11Manager` shall be deprecated and disabled. … no CSM, no CryIf. all keys and ids stored in HSM."** This spec frames that refactor.

## 2. Design principles

### 2.1 One dispatcher, one programming model
The mobile dispatch moves into a new `kMobileManager.{c,h}` at `uHSM-Host/Src/Mobile/Appl/`. It speaks only the semantic command surface (§3). `kPkcs11Manager.{c,h}` is **deleted** from the source tree along with every `spi_*` struct for PKCS#11 commands, the host-side `Pkcs11ObjectRegistry`, the PKCS#11 session table, and all `C_*` function implementations. No build flag, no second binary, no "tooling profile" — those flows migrate to the new semantic surface or move off the SPI wire entirely (factory tooling uses JTAG / direct NVM programming; see §2.4).

### 2.2 Eliminate `Csm_*` / `CryIf_*` from both HSM and Host — direct `crypto_key_id` end-to-end

Per the user's direction: *"wir shall not use `CsmKey_*` / `CryIfKey_*` and CSM-CryIf-Modules in HOST and HSM. working with direct `crypto_key_id`."* The AUTOSAR abstraction layers (`Csm`, `CryIf`) wrap the actual hardware drivers (`Crypto_kHW` = hardware accelerator; `Crypto_kSW` = software fallback). They give us:

- `CsmKey_*` names (configured in `Csm_Cfg.h` / `Csm_Cfg.c`)
- `CryIfKey_*` names (configured in `CryIf_Cfg.h` / `CryIf_Cfg.c`)
- a job-scheduling layer (`Csm_KeyPairGenerate`, `Csm_RandomGenerate`, `CryIf_ProcessJob`, …)

None of this buys the mobile messaging app anything. We replace the two layers with a single thin shim:

- **`uHSM-HSM/Src/BSW/kHsm/Kayten_Crypto.{c,h}`** (new): the only crypto call-site in the HSM. Exposes one function per use-case — `Kayten_Crypto_GenerateKeyPair(crypto_key_id_priv, crypto_key_id_pub, algo)`, `Kayten_Crypto_EcdhCompute(priv_id, peer_pub, out_shared)`, `Kayten_Crypto_EcdsaSign(priv_id, msg, msg_len, out_sig)`, `Kayten_Crypto_AesGcmEncrypt(key_id, iv, aad, pt, out_ct, out_tag)`, `Kayten_Crypto_AesGcmDecrypt(…)`, `Kayten_Crypto_HkdfExpand(prk_id, info, out_okm, out_key_id)`, `Kayten_Crypto_TrngBytes(out, len)`. Each function takes `crypto_key_id` (u32) directly and calls `Crypto_kHW` / `Crypto_kSW` primitives inside without ever naming a `CsmKey_*` or `CryIfKey_*` constant.
- Key material lives in a single flat NVM table owned by `Kayten_Crypto` itself: `Kayten_Crypto_KeyStore[crypto_key_id]`. No `Crypto_Keys[]`, no `Pkcs11_ElementNvmMapping`, no `CryIf_KeyList`. Firmware `kHsm_Kayten.c` resolves the `crypto_key_id` → NVM offset directly via `(id >= KAYTEN_KEY_ID_FIRST && id <= KAYTEN_KEY_ID_LAST)` range gates.
- **Host side**: the `kMobileManager` forwards the 4-byte `crypto_key_id` straight into the HSM IPC payload without any translation — it has no key registry, no CSM imports, no CryIf imports. The only host-side crypto call remaining is the SPI transport's secure-channel ECDH / AES-GCM for `0x55 SECURE_EXECUTE_V1`, which becomes a direct `Crypto_kHW` call rather than a `Csm_*` call.

`Csm_Cfg.{c,h}`, `CryIf_Cfg.{c,h}`, `Rte_Csm_Type.h`, `SchM_Csm.h`, `SchM_CryIf.h`, `uHSM-HSM/Src/BSW/Csm/Csm.c`, and the CryIf counterpart are **deleted**. Their build targets drop out of `uHSM-HSM/Src/BSW/CMakeLists.txt`. Verification: `rg "Csm_|CryIf_|CsmKey_|CryIfKey_"` over the post-refactor tree returns 0 hits outside `docs/archive/`.

This is a deep refactor — it touches every active crypto call-site in the HSM firmware — and it's a hard prerequisite for the §2.6 "no host-side key registry" claim to hold. The per-repo HSM spec (§Appendix A item 2) carries the full call-site inventory.

### 2.3 Session = use-case, not PKCS#11 session
`OPEN_SESSION` / `CLOSE_SESSION` disappear. Sessions are implicit state of one of five use-case tokens:

- **Auth session** — established by `LOGIN_USER_V1`, torn down by `LOGOUT_USER_V1`; scope: everything needs it.
- **Secure-execute session** — ephemeral ECDH transport, established as today by `SECURE_EXECUTE_V1` handshake; scope: wraps messaging + voice commands in hardware mode.
- **Messaging session** — per-peer key material living in slot 64 + SPK/OPK slots; no handle, no state to open/close. Replaced by message-addressed commands.
- **Voice-call session** — bounded by `CALL_KEY_AGREE_V1` + `CALL_TEARDOWN_V1`; already semantic.
- **Epoch window** — bounded by `GENERATE_EPOCH_KEY 0x3B`.

No SPI command takes a `session_handle` argument any more. The auth-session is tracked host-side and HSM-side as a single boolean + PIN-retry counter.

### 2.4 Clean-sheet provisioning — no PKCS#11 carry-over

Per the user's direction: *"existing pkcs11 provision/factory shall not needed. create best fitting solution for provision for new hsm fitting our use-case."* The existing `HandleMobileProvisionToken` (`kPkcs11Manager.c:13107`) carries PKCS#11 token semantics — SO-role + user-role, `token_label` string, `so_pin_label_block_t`, `user_pin_block_t`, synthetic SO-session over `session_table[]`, `kCdd_KaytenProvisionIdentity` sidecar call — none of which the mobile app uses. It's deleted entirely alongside the dispatcher (§2.1).

The replacement is a two-PIN model aligned with the user's requirement:

- **`init_pin` (Initialization PIN)** — used **once** to enter the provisioning flow on a fresh or wiped HSM. Factory-fused: each HSM ships with a unique `init_pin` burned into a write-once NVM region at manufacturing time, printed on the device label (or delivered out-of-band to the user). Survives a key wipe (§6a) so that a wiped device can be re-provisioned. Length: 8 ASCII digits.
- **`user_pin`** — the everyday PIN. Length 4–16 digits (matches current bounds). Zeroized on wipe.

Provisioning flow on a fresh HSM:

1. App reads device state via `GET_RUNTIME_STATUS_V1 (0x49)` → `provisioning_state = PROVISIONING_REQUIRED` (0x00), `last_wipe_reason` optional.
2. App sends `PROVISION_DEVICE_V1 (0x44)` with `{init_pin, user_pin}`. HSM verifies `init_pin` against the factory-fused value (constant-time), sets `user_pin`, generates the device identity key (`crypto_key_id = IDENTITY_PRIV = 22`) **inside this same handler** using `Kayten_Crypto_GenerateKeyPair`, and marks `provisioning_state = PROVISIONED`.
3. App calls `LOGIN_USER_V1 (0x45)` with the user PIN → auth session opens.
4. *(Optional self-check)* App may call `READ_PUBLIC_KEY_V1 (0x61)` with `crypto_key_id = IDENTITY_PUB (23)` to verify the live key matches the `identity_pub` returned in step 2 — **not required** for enrollment because `PROVISION_DEVICE_V1` already returned `identity_pub:32` in its response (§5.1).
5. App registers with `kayten-server` (`DeviceService.RegisterIdentityKey`), then generates + uploads SPK/OPK bundles.

The SO-role disappears. `token_label` disappears. `session_table` disappears. `kCdd_KaytenProvisionIdentity` is folded into step 2 via `Kayten_Crypto_GenerateKeyPair`. Provisioning-state is a single u8: `{0 = REQUIRED, 1 = PROVISIONED, 2 = LOCKED_WIPED}`.

Factory manufacturing flow (off-wire, not SPI-dispatched):
- HSM's NVM receives the unique `init_pin` via JTAG or a one-shot flashing stub during device provisioning on the factory line. This flow is **not** part of the SPI mobile surface and does not ship as a runtime command. Once the `init_pin` NVM block is sealed at manufacturing, no SPI command can change it.

### 2.4.1 Dev-mode init-pin handling

The factory-fused model above is production-only. Dev prototypes (shared serial numbers, no factory line, CI runners that flash 50 times a day) can't use it. Firmware exposes a compile-time switch `KAYTEN_INIT_PIN_SOURCE` with three profiles:

| Profile | Default `init_pin` | `init_pin_block` mutability | `SPI_CAP_DEV_FIRMWARE` bit | Use-case |
|---|---|---|---|---|
| `KAYTEN_INIT_PIN_FACTORY_FUSED` | burned via JTAG per device | sealed, write-once | 0 | PROD binaries. Firmware refuses to boot past `KaytenInit_CheckInitPinBlock()` if the block is blank or the seal bit is clear. |
| `KAYTEN_INIT_PIN_DEV_RESETTABLE` | `"00000000"` (override via `KAYTEN_DEV_DEFAULT_INIT_PIN` at compile time) | non-sealed; rewritable by `CMD_DEV_SET_INIT_PIN (0x67)` | 1 | Dev boards, CI, current prototypes with shared serials. On first boot, if the block is blank, firmware writes the default value and continues — no JTAG step needed. Survives `Kayten_WipeAllKeyMaterial` (wipe does not touch this block). |
| `KAYTEN_INIT_PIN_STAGING_PINNED` | team-shared pin written via `uHSM-Tools/staging_provisioner/` at internal flash time | non-sealed but rewrite gated behind JTAG (no SPI command) | 0 | Internal dogfood — looks like prod on the wire. |

Optional second compile-time flag `KAYTEN_DEV_INIT_PIN_DERIVE_FROM_UID = STD_ON` derives per-board PINs from the MCU's 96-bit unique die ID (`UID0..UID2`):
```
init_pin = base32(HMAC_SHA256("kayten-dev-initpin-v1", UID0||UID1||UID2))[0..8]
```
Firmware emits the derived PIN on the debug UART during first boot. Dev team writes it on masking tape. Useful when the team wants distinct PINs per board without a JTAG tool but knows `UID` is distinct even when the printed serial is reused.

**Dev-only SPI command** (compiled out in PROD and STAGING):

- `CMD_DEV_SET_INIT_PIN (0x67)` — payload `{new_init_pin_len:1, new_init_pin:new_init_pin_len}`. Rewrites the init_pin block. Allowed only when `provisioning_state == PROVISIONING_REQUIRED` (i.e. post-wipe or factory-fresh). A prod HSM that somehow receives `0x67` returns `SPI_STATUS_CMD_NOT_AVAILABLE (0xE0)` — the handler is `#if`-d out, not linked in.

**Dev-firmware advertising**: `PROBE_STATE_V1` capability bitmap gains `SPI_CAP_DEV_FIRMWARE = 1<<7` (post-§2.8 remap) when the dev profile is active. `kayten-app` and `kayten-app-v2` show a persistent red banner *"⚠ dev firmware — do not use with production keys"* whenever they see this bit. Makes accidental dev/prod mixing on the same bench immediately visible.

Your current prototypes → `KAYTEN_INIT_PIN_DEV_RESETTABLE`, default PIN `"00000000"`, `SPI_CAP_DEV_FIRMWARE` on, optionally with UID-derived PINs if per-board distinctness is wanted. No JTAG step; no sticker; `0x67` available over SPI for CI to sweep PINs.

### 2.5 PIN retry + wipe behaviour

Per the user: *"wrong entering 3 times of hsm pin shall lock hsm device and delete all keys in mobile and hsm device. invalidate keys in nvm storage."* This is the full lockout flow (full procedural detail lives in §6a):

- 3 failed `LOGIN_USER_V1` attempts trigger an **atomic key wipe** on the HSM: identity (22/23), SPK band (35..43), OPK band (70..109), messaging bootstrap secret (slot 64), voice-call + epoch-window state, SRK, and any other key-bearing NVM block.
- `user_pin` is cleared (`user_pin_initialized = 0`); `init_pin` is **retained** so the user can re-provision with a fresh `user_pin`.
- HSM returns `SPI_MOBILE_STATUS_DEVICE_WIPED (0xEE)` on the failing `0x45` response, with `last_wipe_reason = PIN_LOCKOUT` (0x01) now readable via `GET_RUNTIME_STATUS_V1`.
- App on receiving `DEVICE_WIPED`: follow **§6a.3** (normative): **outbox-first**
  `RevokeDevice` / durable pending queue, **then** local wipe (Keychain, DB,
  caches), **then** provisioning UX — do not use the naive ordering below as an
  implementation guide; §6a.3 fixes offline / process-kill loss of revocation.
- Server on receiving `RevokeDevice`:
  1. Marks device revoked (existing behaviour).
  2. Broadcasts `DEVICE_REVOKED` + `KEY_CHANGE_ALERT` (existing behaviour, `service.go:355-394`).
  3. **New**: mark **all** `prekeys` rows for the device `used = TRUE` (unified table — see `kayten-server/migrations/005_keys.up.sql`) so no fresh bundle is served.
  4. **New**: clears the device's undelivered inbound message queue (encrypted-at-rest, but no longer decryptable).
  5. **New**: expose revocation on identity fetch (`revoked_at` per §9a — DB column and/or join from `devices.revoked_at`).

### 2.6 No host-side key registry
`Pkcs11ObjectRegistry`, `Pkcs11_ElementNvmMapping`, `CsmKey_*`, `CryIfKey_*` disappear from the mobile-host binary. All three concepts collapse into "firmware key id in `[N..M]` band".

### 2.7 No Dart-level logical slot layer
App code references `crypto_key_id` directly. The only named constants allowed are band-boundary symbols (`KAYTEN_KEY_IDENTITY_PRIV = 22`, `KAYTEN_KEY_SPK_PRIV_FIRST = 35`, `KAYTEN_KEY_OPK_PRIV_FIRST = 70`, …) shared via proto-generated `kayten.hsm_keys.v1` (new proto package, §9). No more `kSlotIdentityEd25519`, `kSlotSignedPrekey`, `kSlotOpkStart`.

### 2.8 Firmware-alignment note (post-2026-04-16 OPK/DH4 merge)

The 2026-04-16 OPK + DH4 workstream merged into `uHSM-HSM` `develop` on 2026-04-17 (commit `b0b4c46`). Its shipped constants consume wire space this spec's original drafting assumed was free. The authoritative remap below replaces several values this spec initially assigned. **All per-repo specs, AI prompts, and downstream consumers MUST use the remapped values** — the pre-alignment numbers are preserved in git history only.

**Firmware constants locked in by 2026-04-16 merge** (from `Src/BSW/kHsm/kHsm_Kayten.h`):

- Status codes (0xE0..0xFF): `0xE6 ENVELOPE_INNER_FORBIDDEN`, `0xE7 ENVELOPE_COUNTER_REPLAY`, `0xE8 ENVELOPE_UNAVAILABLE`, `0xE9 ENVELOPE_AUTH_FAILURE`, `0xEA CALL_NOT_ACTIVE`, `0xEB PEER_EPHEMERAL_INVALID`, `0xEC SESSION_NOT_CONFIRMED`, `0xF0 ENCRYPTED_DATA_INVALID`, `0xF1 MSG_KE_OPK_INVALID`, `0xF2 MOBILE_AUTH_FAILURE`, `0xF3 MOBILE_EPOCH_MISMATCH`, `0xF4 MOBILE_SESSION_OWNERSHIP_FAILED`, `0xF6..0xF9` identity/entropy, `0xFA..0xFD` MSG_KE_*_FAILED, `0xFF MOBILE_INTERNAL_FAILURE`.
- Capability bits: `bit 0 SECURE_EXECUTE_V1`, `bit 1 CALL_KEY_AGREE`, `bit 2 MSG_KEY_EXCHANGE_V1`, `bit 3 DEDICATED_SLOTS_V1`, `bit 4 PKCWAIT_TIMEOUT`, `bit 5 OPK_V1`.
- Command ids: `0x5C GENERATE_OPK_V1` (shipped), `0x57/0x5A/0x5B` DH4-mixing (shipped). Commands `0x60..0x68` (this spec's new surface) remain free.
- OPK band: `KAYTEN_OPK_LOGICAL_ID_FIRST = 70`, `_LAST = 108` (priv, even), pubs `71..109` (odd) — matches this spec's `CryptoKeyId.CRYPTO_KEY_ID_OPK_*` band (no change).
- NvM block IDs: OPK uses 20 × 96-byte blocks at ids `113..132` via new `Src/BSW/kOpk/` module (single-block-per-pair layout, not two blocks per pair). `NVM_TOTAL_NUM_OF_BLOCK` now `135`.

**Authoritative status-code remap (this spec's mobile-profile codes → firmware-free slots):**

| Symbolic name | Original (pre-merge) | **Final (post-merge)** | Reason for move |
|---|---|---|---|
| `SPI_STATUS_CMD_NOT_AVAILABLE` | 0xE8 | **0xE0** | Firmware `ENVELOPE_UNAVAILABLE` owns 0xE8 |
| `SPI_MOBILE_STATUS_KEY_ID_NOT_READABLE` | 0xEA | **0xE1** | Firmware `CALL_NOT_ACTIVE` owns 0xEA |
| `SPI_MOBILE_STATUS_KEY_ID_ROTATE_ONLY` | 0xEB | **0xE2** | Firmware `PEER_EPHEMERAL_INVALID` owns 0xEB |
| `SPI_MOBILE_STATUS_KEY_ID_STALE` | 0xEC | **0xE3** | Firmware `SESSION_NOT_CONFIRMED` owns 0xEC |
| `SPI_MOBILE_STATUS_SESSION_NOT_READY` | 0xED | 0xED | Unchanged — 0xED is free |
| `SPI_MOBILE_STATUS_DEVICE_WIPED` | 0xEE | 0xEE | Unchanged — 0xEE is free |
| `SPI_MOBILE_STATUS_ALREADY_PROVISIONED` | 0xF0 | **0xE4** | Firmware `ENCRYPTED_DATA_INVALID` owns 0xF0 |
| `SPI_MOBILE_STATUS_INIT_PIN_INVALID` | 0xF1 | **0xE5** | Firmware `MSG_KE_OPK_INVALID` owns 0xF1 |
| `SPI_MOBILE_STATUS_WIPE_NONCE_INVALID` | 0xF2 | **0xEF** | Firmware `MOBILE_AUTH_FAILURE` owns 0xF2 |
| `SPI_MOBILE_STATUS_INIT_PIN_THROTTLED` | 0xF3 | **0xF5** | Firmware `MOBILE_EPOCH_MISMATCH` owns 0xF3 |

**Authoritative capability-bit remap:**

| Symbolic name | Original (pre-merge) | **Final (post-merge)** | Reason for move |
|---|---|---|---|
| `SPI_CAP_MOBILE_PROFILE_V1` | `1<<5` (`0x20`) | **`1<<6` (`0x40`)** | Firmware `KAYTEN_HSM_CAP_OPK_V1 = 0x20` owns bit 5 |
| `SPI_CAP_DEV_FIRMWARE` | `1<<6` (`0x40`) | **`1<<7` (`0x80`)** | Cascaded from the bit-5 → bit-6 move above |

Firmware-side naming additions (align with existing `KAYTEN_HSM_CAP_*` convention — defined in `kHsm_Kayten.h`):

```c
#define KAYTEN_HSM_CAP_MOBILE_PROFILE_V1  (0x00000040u)  /* bit 6 — kMobileManager semantic surface + 0x60..0x68 handlers */
#define KAYTEN_HSM_CAP_DEV_FIRMWARE       (0x00000080u)  /* bit 7 — KAYTEN_INIT_PIN_SOURCE == DEV_RESETTABLE + 0x67 linked */
```

**NVM block IDs for this spec's new crypto_key_id bands (110..239):** allocate starting at `NvM_Cfg` id **133** (next free after kOpk ends at 132). Do NOT overlap `113..132`. The uHSM-HSM spec §2.7 lists concrete allocations.

Any reference in any downstream spec / AI prompt / code to a pre-merge value MUST be treated as a typo and updated to the post-merge value. CI `rg` gates in §11 are extended accordingly.

## 3. Target mobile command surface

### 3.1 Retained, unchanged on the wire

| `cmd_id` | Semantic name | Notes |
|---|---|---|
| `0x43` | `PROBE_STATE_V1` | Capability bitmap; gains `SPI_CAP_MOBILE_PROFILE_V1 = 1<<6` (post-§2.8 remap — firmware bit 5 is `KAYTEN_HSM_CAP_OPK_V1`) indicating legacy-PKCS#11 dispatch is unavailable, and `SPI_CAP_DEV_FIRMWARE = 1<<7` when firmware is built with `KAYTEN_INIT_PIN_SOURCE == KAYTEN_INIT_PIN_DEV_RESETTABLE` (see §2.4.1). Apps render a dev-mode warning banner on the latter. |
| `0x44` | `PROVISION_DEVICE_V1` | **Renamed** from `PROVISION_TOKEN_V1`; semantics completely replaced per §2.4 — accepts `{init_pin, user_pin}`, generates identity inline. No SO-role, no label. Payload shrinks (see §5.1). |
| `0x45` | `LOGIN_USER_V1` | Lockout semantics upgraded per §2.5/§6a: third failed attempt wipes all key NVM blocks and returns `DEVICE_WIPED`. **Hardware production (Model C):** MUST be invoked only as the **inner** command of `SECURE_EXECUTE_V1 (0x55)` — bare outer `0x45` SHALL be rejected with `SPI_STATUS_CMD_NOT_AVAILABLE (0xE0)` once `SPI_CAP_MOBILE_PROFILE_V1` is advertised (§4). Dev/staging builds MAY keep a bare-`0x45` fast path for bench bring-up. Rationale: `0x55` handshake requires the HSM's identity key (present after `0x44` commits identity), so `0x55`-wrapping is feasible from state `PROVISIONED_LOGGED_OUT` onward — see §3.1.1. |
| `0x46` | `CHANGE_USER_PIN_V1` | Body unchanged, but now rejects when `provisioning_state != PROVISIONED`. **Hardware production (Model C):** MUST be invoked only as the **inner** command of `SECURE_EXECUTE_V1 (0x55)` — bare outer `0x46` SHALL be rejected with `SPI_STATUS_CMD_NOT_AVAILABLE (0xE0)` once `SPI_CAP_MOBILE_PROFILE_V1` is advertised (§4). Dev/staging builds MAY keep a bare-`0x46` fast path for bench bring-up. |
| `0x47` | `READ_IDENTITY_PUBLIC_V1` | Unchanged (legacy convenience; equivalent to `0x61` with `crypto_key_id = IDENTITY_PUB`). Retained because the bring-up sequence in §2.4 step 4 hits it before the auth session is fully up on legacy clients. New clients should prefer `0x61`. |
| `0x48` | `IDENTITY_SIGN_V1` | Unchanged. |
| `0x49` | `GET_RUNTIME_STATUS_V1` | Response grows `provisioning_state:1`, `last_wipe_reason:1`, `remaining_pin_retries:1` fields per §5.7. |
| `0x4A` | `LOGOUT_USER_V1` | Unchanged. |
| `0x4B` | `VOICE_ENCRYPT_FRAME_V1` | Unchanged. |
| `0x4C` | `VOICE_DECRYPT_FRAME_V1` | Unchanged. |
| `0x4D` | `RESET_SESSION_STATE_V1` | Unchanged. |
| `0x4E` | `WRAP_STORAGE_ROOT_KEY_V1` | Unchanged. |
| `0x4F` | `UNWRAP_STORAGE_ROOT_KEY_V1` | Unchanged. |
| `0x53` | `CALL_KEY_AGREE_V1` | Unchanged. |
| `0x54` | `CALL_TEARDOWN_V1` | Unchanged. |
| `0x55` | `SECURE_EXECUTE_V1` | Inner-cmd allow-list in firmware is rewritten to match §3 (§4). |
| `0x57` | `MSG_KEY_EXCHANGE_V1` | Unchanged (initiator). |
| `0x59` | `GET_HSM_DET_BUFFER_V1` | Unchanged. |
| `0x5A` | `PEER_RATCHET_STEP_V1` | Unchanged. |
| `0x5B` | `MSG_KEY_EXCHANGE_RESPONDER_V1` | Unchanged (OPK/DH4 spec lands first). |
| `0x5C` | `KAYTEN_GENERATE_OPK_V1` | Unchanged (OPK/DH4 spec lands first). |
| `0x40` | `ENCRYPT_AND_SIGN_MSG` | Unchanged (messaging). |
| `0x41` | `DECRYPT_AND_VERIFY_MSG` | Unchanged (messaging). |
| `0x42` | `FULL_RATCHET_STEP` | Unchanged (messaging). |
| `0x3A` | `HKDF_DERIVE` | Unchanged. |
| `0x3B` | `GENERATE_EPOCH_KEY` | Unchanged. |
| `0x3D` | `SET_VOICE_MODE` | Unchanged. |
| `0x3E` | `CLEAR_VOICE_MODE` | Unchanged. |

### 3.1.1 PIN-protection rules across `0x44` / `0x45` / `0x46`

| Command | Plaintext-outer allowed in hardware prod? | Why |
|---|---|---|
| `0x44 PROVISION_DEVICE_V1` | **Yes (unavoidable).** `init_pin` + `user_pin` cross plaintext. | `0x55` handshake (see `android/…/SecureChannel.kt:562-620`) is Ed25519-signed by the HSM's identity key. On a fresh HSM the identity key does not yet exist — `0x44` is the command that generates it. A pre-provision `0x55` is therefore physically impossible without weakening the handshake to unauthenticated ephemeral-only ECDH (rejected — gives no MitM protection and forks the secure-channel implementation). Accepted risk is narrow: one-shot per device lifetime, physical FT4222 bus access required. `init_pin` is already a physical possession factor. Two-phase provisioning (§13 follow-up) closes this fully. See also §10 consideration #14. |
| `0x45 LOGIN_USER_V1` | **No.** Bare outer rejected with `0xE0` once `SPI_CAP_MOBILE_PROFILE_V1` is advertised. | Identity key is present from state `PROVISIONED_LOGGED_OUT` onward; `0x55` handshake succeeds. `user_pin` never crosses plaintext on login. Same rule as `0x46`. |
| `0x46 CHANGE_USER_PIN_V1` | **No.** Bare outer rejected with `0xE0` once `SPI_CAP_MOBILE_PROFILE_V1` is advertised. | Same rationale as `0x45`. |

Dev/staging MAY keep a bare-outer fast path for each of the three so bench bring-up on fresh HSMs without a host that speaks `0x55` still works.

### 3.2 New semantic replacements introduced by this spec

| `cmd_id` | Semantic name | Replaces |
|---|---|---|
| `0x60` | `GENERATE_MESSAGING_KEY_V1` | `CMD_GENERATE_KEY_PAIR (0x27)` for the SPK use-case. Payload `{ kind: u8 }` only — `kind ∈ {SPK, EPHEMERAL_ECDH}` (see §5.2). Returns `{crypto_key_id_priv:4LE, crypto_key_id_pub:4LE, public_key:raw-X||Y:64, spk_signature:ecdsa-p256-sig:64, crc:2}` for SPK; ephemeral response uses zero `spk_signature`. The signature over `public_key` by the identity key is **atomic with generation** — app never calls a separate sign. |
| `0x61` | `READ_PUBLIC_KEY_V1` | `CMD_GET_ATTRIBUTE_VALUE (0x33)` for the "give me the public component of `crypto_key_id`" use-case. Single-shot, no template, no session handle. Payload `{crypto_key_id:4LE}`, response `{public_key_len:2LE, public_key:raw}`. |
| `0x62` | `DELETE_MESSAGING_KEY_V1` | `CMD_DESTROY_OBJECT (0x34)`. Payload `{crypto_key_id:4LE}`. Only permitted for OPK and ephemeral bands; SPK deletion goes through `ROTATE_SPK_V1`. |
| `0x63` | `ROTATE_SPK_V1` | `CMD_DESTROY_OBJECT` + `CMD_GENERATE_KEY_PAIR` dance. Atomic: generate fresh SPK, sign with identity, zeroize previous SPK slot, return `{new_crypto_key_id:4LE, public_key:64, spk_signature:64}`. |
| `0x64` | `GENERATE_RANDOM_V1` | `CMD_GENERATE_RANDOM (0x21)`. Payload `{length:2LE}`, response `{length:2LE, random:raw}`. Bounded at 512 B. |
| `0x65` | `USER_INITIATED_WIPE_V1` | **NEW**. User-triggered "wipe my HSM" flow (settings → security → reset device). Requires `user_pin` re-entry **and** a fresh HSM-issued `wipe_nonce` from `REQUEST_WIPE_CHALLENGE_V1 (0x68)` (§5.8). On match performs the same zeroization as the PIN-lockout path (§6a). Retains `init_pin`. Returns `SPI_MOBILE_STATUS_OK` or `AUTH_FAILED` / `SPI_MOBILE_STATUS_WIPE_NONCE_INVALID (0xEF)`. Not reachable without a logged-in session. |
| `0x66` | `GET_WIPE_STATUS_V1` | **NEW**. Returns `{last_wipe_reason:1, last_wipe_unix:8LE}` where `last_wipe_reason ∈ {0=NONE, 1=PIN_LOCKOUT, 2=USER_REQUESTED, 3=ATTESTATION_FAILED}`. Pre-auth callable so the app can render the provisioning screen with an accurate "your HSM was wiped because …" message. |
| `0x67` | `DEV_SET_INIT_PIN_V1` | **NEW — DEV-only**. Compiled out of PROD and STAGING binaries via `#if KAYTEN_INIT_PIN_SOURCE == KAYTEN_INIT_PIN_DEV_RESETTABLE` (§2.4.1). Payload `{new_init_pin_len:1, new_init_pin:new_init_pin_len}`. Allowed only in `PROVISIONING_REQUIRED` state. Lets CI rewrite the init_pin without JTAG. A PROD HSM accidentally targeted with `0x67` returns `SPI_STATUS_CMD_NOT_AVAILABLE (0xE0)`. |
| `0x68` | `REQUEST_WIPE_CHALLENGE_V1` | **NEW**. Auth-session required (`PROVISIONED_LOGGED_IN`). Returns `{wipe_nonce:32, valid_until_unix:8LE}`. Firmware stores the nonce in RAM (single-shot, TTL default **120 s**, config-const), cleared on successful `0x65`, logout, or superseding challenge. Plaintext outer SPI is acceptable (nonce is not secret); rate-limit optional. |

No further new ids beyond `0x68` for this workstream. All existing *messaging runtime* commands (`0x40`, `0x41`, `0x42`, `0x5A`, `0x57`, `0x5B`, `0x5C`) already fit the 1:1 model.

### 3.3 Removed from mobile dispatch

Every `CMD_*` opcode in `uHSM-Host/Src/PKCS11/Appl/kPkcs11Manager.h` lines `122..180` except `0x21` (superseded by `0x64`) and `0x27` (superseded by `0x60`/`0x63`) is dead on the mobile wire after this spec:

`0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08–0x2C` (all `C_Encrypt*`, `C_Decrypt*`, `C_Digest*`, `C_Sign*`, `C_Verify*`, `C_GenerateKey`, `C_WrapKey`, `C_UnwrapKey`, `C_DeriveKey`), `0x2F`, `0x30–0x37`.

Mobile host returns `SPI_STATUS_CMD_NOT_AVAILABLE (0xE0)` for any of them; no handler is linked in (the `kPkcs11Manager` translation unit is deleted — it cannot even be reached at runtime).

### 3.4 PKCS#11 constants removed from app code

| Constant | Current location | Fate |
|---|---|---|
| `CKA_EC_POINT`, `CKA_CLASS`, `CKA_KEY_TYPE`, `CKO_PRIVATE_KEY`, `CKO_PUBLIC_KEY`, `CKK_EC` | `HsmConstants.kt` | Delete. |
| `CK_SESSION_HANDLE`, `CK_OBJECT_HANDLE` typedefs | `HsmCommand.kt` | Delete. |
| `kSlotIdentityEd25519`, `kSlotIdentityEd25519Public`, `kSlotSignedPrekey`, `kSlotOpkStart`, `kSlotOpkEnd`, `kSlotKeyExchangeCurve25519Private`, … | `lib/core/constants/crypto_constants.dart` | Replace with `kayten.hsm_keys.v1` proto-generated `CryptoKeyId` constants. |
| `HsmCommand.Legacy` sealed subclass tree (all variants) | `HsmCommand.kt` | Delete entire `Legacy` branch. |
| `Pkcs11ObjectResolver`, `Pkcs11ObjectRegistry` | `android/app/src/main/kotlin/com/kayten/uhsm/hsm/` | Delete. |

## 4. `SECURE_EXECUTE_V1 (0x55)` inner-command allow-list (HSM-side)

Firmware `KAYTEN_IS_ALLOWED_INNER_CMD` (`uHSM-HSM/Src/BSW/kHsm/kHsm_Kayten.h:344`) becomes the new allow-list after §3:

```
KAYTEN_CMD_ENCRYPT_AND_SIGN,
KAYTEN_CMD_DECRYPT_AND_VERIFY,
KAYTEN_CMD_FULL_RATCHET_STEP,
KAYTEN_CMD_VOICE_ENCRYPT_FRAME,
KAYTEN_CMD_VOICE_DECRYPT_FRAME,
KAYTEN_CMD_CALL_KEY_AGREE,
KAYTEN_CMD_CALL_TEARDOWN,
KAYTEN_CMD_LOGIN_USER_V1,               -- outer semantic 0x45 in hardware prod MUST use this inner path only (§3.1 / §3.1.1)
KAYTEN_CMD_CHANGE_USER_PIN_V1,          -- outer semantic 0x46 in hardware prod MUST use this inner path only (§3.1 / §3.1.1)
KAYTEN_CMD_IDENTITY_SIGN,
KAYTEN_CMD_MSG_KEY_EXCHANGE,
KAYTEN_CMD_MSG_KEY_EXCHANGE_RESPONDER,
KAYTEN_CMD_PEER_RATCHET_STEP,
KAYTEN_CMD_GENERATE_OPK,                -- OPK/DH4 spec, confirmed
KAYTEN_CMD_GENERATE_MESSAGING_KEY,      -- NEW §3.2 0x60
KAYTEN_CMD_READ_PUBLIC_KEY,             -- NEW §3.2 0x61
KAYTEN_CMD_DELETE_MESSAGING_KEY,        -- NEW §3.2 0x62
KAYTEN_CMD_ROTATE_SPK,                  -- NEW §3.2 0x63
KAYTEN_CMD_GENERATE_RANDOM,             -- NEW §3.2 0x64
KAYTEN_CMD_USER_INITIATED_WIPE,         -- NEW §3.2 0x65 (security: must be SECURE_EXECUTE-wrapped)
KAYTEN_CMD_GENERATE_EPOCH_KEY,          -- existing, newly allowed inside 0x55
KAYTEN_CMD_HKDF_DERIVE                  -- existing, newly allowed inside 0x55
```

`WRAP_DEVICE_SECRET` / `UNWRAP_DEVICE_SECRET` are retained on the inner allow-list — they cover the same ground as `WRAP_STORAGE_ROOT_KEY_V1` / `UNWRAP_STORAGE_ROOT_KEY_V1`.

Recursive `0x55` remains forbidden. `GENERATE_MESSAGING_KEY_V1` is required inside `0x55` in hardware production mode per the existing Model C rule.

## 5. Wire contracts for new commands

### 5.1 `PROVISION_DEVICE_V1 (0x44)`

```
[header:2 = A5 5A][length:2LE][cmd:1 = 0x44]
[init_pin_len:1 = 8][init_pin:8]        -- exactly 8 ASCII digits
[user_pin_len:1][user_pin:user_pin_len] -- 4..16 ASCII digits
[crc:2LE]
```

Response:
```
[header:2 = A5 5A][length:2LE][status:1][cmd_id:1 = 0x44]
[provisioning_state:1]                  -- 0x01 = PROVISIONED on success
[identity_pub:32]                       -- the freshly-generated Ed25519 identity pub
[crc:2LE]
```

Firmware behaviour (§6a companion):
1. Verify `provisioning_state == PROVISIONING_REQUIRED` (state 0). Otherwise `SPI_MOBILE_STATUS_ALREADY_PROVISIONED (0xE4)`.
2. **Throttle check (new, §5.1.1):** if `now - last_failed_init_pin_unix < throttle_delay(init_pin_fail_count)`, return `SPI_MOBILE_STATUS_INIT_PIN_THROTTLED (0xF5)` without attempting compare.
3. Constant-time compare `init_pin` against the factory-fused NVM value. Mismatch: `init_pin_fail_count++`; `last_failed_init_pin_unix = now`; persist both; return `SPI_MOBILE_STATUS_INIT_PIN_INVALID (0xE5)`.
4. On match: reset `init_pin_fail_count = 0`; persist.
5. **Write NVM blocks in this exact order — each atomic at the NvM layer, no journal needed (§5.1.2):**
   1. `KAYTEN_NVM_USER_PIN_BLOCK` ← `{user_pin, initialized=1, retries=3}`
   2. `KAYTEN_NVM_IDENTITY_KEY_PAIR_BLOCK` ← generated `(IDENTITY_PRIV=22, IDENTITY_PUB=23)` via `Kayten_Crypto_GenerateKeyPair(..., ALGO_ED25519)`
   3. `KAYTEN_NVM_RUNTIME_STATE_BLOCK` ← `{provisioning_state = PROVISIONED}` **[commit marker]**
6. Emit response with the new `identity_pub`.

**Atomicity contract (§5.1.2, normative):** any crash mid-sequence leaves `provisioning_state = PROVISIONING_REQUIRED`; a retry of `0x44` overwrites partial state (step 5.1 / 5.2) idempotently before re-committing step 5.3. No separate journal is required as long as the NvM layer guarantees per-block atomicity (it does on TC33x; verify equivalent on any future MCU). The §6a.2 recovery heuristic covers the inverse failure mode (state=PROVISIONED with pin_initialized=0).

**Init-PIN throttle (§5.1.1, normative):** persistent NVM `init_pin_fail_count:u8` (default 0, resets to 0 on successful `0x44`) and `last_failed_init_pin_unix:u64`. Backoff table:

| `init_pin_fail_count` | Delay before next `0x44` accepted |
|---|---|
| 0..2 | 0 s (free typo allowance) |
| 3 | 10 s |
| 4 | 60 s |
| 5 | 600 s |
| 6 | 3600 s |
| ≥7 | 3600 s (capped) |

Reduces 10⁸-PIN-space worst-case brute-force from ~115 days at 100 ms/attempt to >19 years. No legitimate user hits the throttle past attempt 3 (typos fit inside the free allowance). A prod HSM that encounters `init_pin_fail_count ≥ 7` continues throttling indefinitely; a factory reset (out-of-band JTAG) is the only way to clear the counter on a stuck device. Status code `SPI_MOBILE_STATUS_INIT_PIN_THROTTLED (0xF5)`.

### 5.7 `GET_RUNTIME_STATUS_V1 (0x49)` extended

Response (new fields appended; no CRC break because length-prefixed):
```
[header:2][length:2LE][status:1][cmd_id:1 = 0x49]
... existing fields ...
[provisioning_state:1]              -- 0 = REQUIRED, 1 = PROVISIONED, 2 = LOCKED_WIPED
[last_wipe_reason:1]                -- 0 = NONE, 1 = PIN_LOCKOUT, 2 = USER_REQUESTED, 3 = ATTESTATION_FAILED
[remaining_pin_retries:1]           -- 0..3 (initial 3)
[crc:2LE]
```

Callable **pre-auth** — the app uses this for initial routing between "fresh HSM → provision", "normal → login", "wiped → provision-with-recovery-message".

### 5.8 `REQUEST_WIPE_CHALLENGE_V1 (0x68)`, `USER_INITIATED_WIPE_V1 (0x65)`, and `GET_WIPE_STATUS_V1 (0x66)`

**`0x68 REQUEST_WIPE_CHALLENGE_V1` Request** (auth session required — state **2** only; plaintext outer SPI is acceptable):
```
[header:2][length:2LE][cmd:1 = 0x68][crc:2LE]
```

Response:
```
[header:2][length:2LE][status:1][cmd_id:1 = 0x68]
[wipe_nonce:32]
[valid_until_unix:8LE]
[crc:2LE]
```

Firmware generates `wipe_nonce` with TRNG, stores it in RAM with `valid_until_unix = now + 120` (default TTL, `#define` tunable), **single-shot**: issuing a second `0x68` supersedes the prior nonce. Successful `0x65`, `LOGOUT_USER_V1`, or power-loss clears the pending challenge. Errors: `SESSION_NOT_READY` if not logged in; optional rate-limit returns `AUTH_FAILED` if abused.

**`0x65` Request** (must be `SECURE_EXECUTE_V1`-wrapped in hardware production mode):
```
[header:2][length:2LE][cmd:1 = 0x65]
[user_pin_len:1][user_pin:user_pin_len]
[wipe_nonce:32]                     -- MUST match the RAM-stored nonce from the most recent successful 0x68, and MUST satisfy now <= valid_until_unix
[crc:2LE]
```

Response:
```
[header:2][length:2LE][status:1][cmd_id:1 = 0x65][crc:2LE]
```

Handler performs the full zeroization flow (§6a.2) on success, **burns** the pending nonce before mutating NVM, and invalidates the current session. Requires `user_pin` re-entry as a second-factor against a lost/unlocked device. Wrong / expired / missing nonce → `SPI_MOBILE_STATUS_WIPE_NONCE_INVALID (0xEF)` (distinct from `AUTH_FAILED` for bad PIN so the UI can prompt "tap wipe again to refresh challenge"). **Rationale:** A deterministic client-only "confirmation" derived from public `identity_pub` is not anti-replay; freshness MUST be rooted in HSM-issued entropy.

**`0x66` Request** (pre-auth, plaintext):
```
[header:2][length:2LE][cmd:1 = 0x66][crc:2LE]
```

Response:
```
[header:2][length:2LE][status:1][cmd_id:1 = 0x66]
[last_wipe_reason:1][last_wipe_unix:8LE][crc:2LE]
```

Duplicates two of the fields from `0x49` but is callable on a freshly-wiped device *before* provisioning / login, when `0x49` may still return `SESSION_NOT_READY` or carry fields (`remaining_pin_retries`) that are not yet meaningful for UX copy.

### 5.2 `GENERATE_MESSAGING_KEY_V1 (0x60)`

Request:
```
[A5 5A][length:2LE][cmd:1 = 0x60]
[kind:1]        -- 0x01 = SPK, 0x02 = EPHEMERAL_ECDH
[crc:2LE]
```

Response (kind = SPK):
```
[A5 5A][length:2LE][status:1][cmd_id:1 = 0x60]
[crypto_key_id_priv:4LE]            -- odd-valued SPK band id, e.g. 35
[crypto_key_id_pub:4LE]             -- even neighbour
[public_key:64]                     -- raw X||Y
[spk_signature:64]                  -- ECDSA over public_key by KAYTEN_KEY_IDENTITY_PRIV
[crc:2LE]
```

Response (kind = EPHEMERAL_ECDH): same shape, no `spk_signature` (all-zero 64 B), public = raw X||Y.

### 5.3 `READ_PUBLIC_KEY_V1 (0x61)`

Request: `[A5 5A][length:2LE][cmd:1 = 0x61][crypto_key_id:4LE][crc:2LE]`.

Response: `[A5 5A][length:2LE][status:1][cmd_id:1 = 0x61][public_key_len:2LE][public_key:raw][crc:2LE]`.

Valid `crypto_key_id` bands: identity pub (`23`), SPK pub (`36/38/40/42/44`), OPK pub (`71,73,…,109`), ephemeral pub (firmware-assigned). Any other id returns `SPI_MOBILE_STATUS_KEY_ID_NOT_READABLE (0xE1)`.

### 5.4 `DELETE_MESSAGING_KEY_V1 (0x62)`

Request: `[A5 5A][length:2LE][cmd:1 = 0x62][crypto_key_id:4LE][crc:2LE]`.

Permitted bands: OPK private (`70,72,…,108`) and ephemeral private. SPK private is refused with `SPI_MOBILE_STATUS_KEY_ID_ROTATE_ONLY (0xE2)` — apps must use `0x63` for SPK.

Response: `{status, cmd_id = 0x62, crc}`.

### 5.5 `ROTATE_SPK_V1 (0x63)`

Request: `[A5 5A][length:2LE][cmd:1 = 0x63][old_spk_crypto_key_id:4LE][crc:2LE]`.

Response: `{status, cmd_id = 0x63, new_crypto_key_id_priv:4LE, new_crypto_key_id_pub:4LE, public_key:64, spk_signature:64, crc:2LE}`.

**SPK retention model (normative):** The NVM band holds **five** SPK key-pair slots (`35/36` … `43/44`). Firmware tracks `current_spk_priv_id` (odd, in-range) and optionally `previous_spk_priv_id` (the immediately prior current, or `0` if none). `ROTATE_SPK_V1` **atomically**: (1) verifies `old_spk_crypto_key_id == current_spk_priv_id`; (2) generates the successor key in the **next free ring slot** (or overwrites the oldest slot **only** when the ring is full — implement as `(write_index++) % 5` with metadata tracking which slot is `current`); (3) demotes old `current` → `previous`; (4) **zeroizes** the private half of any SPK slot older than `previous` (at most one slot per rotate) so **at most two** SPK private keys are non-zero at any instant (`current` + `previous`). `READ_PUBLIC_KEY_V1` / server prekey bundles MAY still publish the `previous` public key until the app completes `UploadPrekeys` for the new SPK and the server marks the old bundle retired — partners already in an X3DH-style flow with a fetched SPK pub therefore have a bounded handshake window. Firmware rejects `old_spk_crypto_key_id` outside `[35..43]` or not equal to `current_spk_priv_id` with `SPI_MOBILE_STATUS_KEY_ID_STALE (0xE3)`.

### 5.6 `GENERATE_RANDOM_V1 (0x64)`

Request: `[A5 5A][length:2LE][cmd:1 = 0x64][length_requested:2LE][crc:2LE]`.

Response: `[A5 5A][length:2LE][status:1][cmd_id:1 = 0x64][length:2LE][random:length][crc:2LE]`.

`length_requested` ≤ 512. Implementation source: `Kayten_Crypto_TrngBytes` (target tree); legacy reference was `Csm_RandomGenerate` — CSM is deleted per §2.2. Gated on an authenticated session.

## 6. Session & PIN state machine

Four states, one transition-table:

| State | Dispatch allowed | Description |
|---|---|---|
| **0 — PROVISIONING_REQUIRED** (fresh or wiped) | `0x43 PROBE_STATE_V1`, `0x44 PROVISION_DEVICE_V1`, `0x49 GET_RUNTIME_STATUS_V1`, `0x66 GET_WIPE_STATUS_V1` | Factory-default or post-wipe. No keys. Only provisioning + status probes allowed. |
| **1 — PROVISIONED_LOGGED_OUT** | Same dispatch surface as state **0** **plus** `0x45 LOGIN_USER_V1` | User-PIN set; identity + SRK present; other keys may or may not be present. No runtime crypto allowed. **`0x44`** is still parseable on the wire here but the handler **MUST** return `SPI_MOBILE_STATUS_ALREADY_PROVISIONED (0xE4)` (§5.1 step 1) — not a usable re-provision until a wipe returns state **0**. |
| **2 — PROVISIONED_LOGGED_IN** (auth session live) | all §3.1 + §3.2 commands (includes `0x68 REQUEST_WIPE_CHALLENGE_V1`) | Normal operation. `0x55 SECURE_EXECUTE_V1` required in hardware-production mode per the existing Model C rule. |
| **3 — LOCKED_WIPED** (observable for 1 response only) | (transient) | Set by the handler on the third bad PIN + returned to the caller. On the next boot / next `0x49`, the device reports state 0 with `last_wipe_reason = PIN_LOCKOUT`. |

No SPI command takes a `session_handle`. The auth-session is tracked as a single `g_kayten_state.user_logged_in` boolean + `g_kayten_state.remaining_pin_retries` (u8, initial 3) + `g_kayten_state.provisioning_state` (u8).

## 6a. Lockout and wipe procedure

### 6a.1 Trigger: PIN lockout on `0x45 LOGIN_USER_V1`

```
on LOGIN_USER_V1(user_pin):
    if provisioning_state != PROVISIONED: return SESSION_NOT_READY
    if remaining_pin_retries == 0:        return DEVICE_WIPED  (carry-over from earlier lockout)
    if constant_time_eq(user_pin, stored_user_pin):
        remaining_pin_retries = 3
        WriteNvm(USER_PIN_BLOCK)
        user_logged_in = TRUE
        return OK
    else:
        remaining_pin_retries -= 1
        WriteNvm(USER_PIN_BLOCK)
        if remaining_pin_retries == 0:
            Kayten_WipeAllKeyMaterial(reason = PIN_LOCKOUT)
            return DEVICE_WIPED
        return AUTH_FAILED { remaining_pin_retries }
```

### 6a.2 `Kayten_WipeAllKeyMaterial(reason)` — atomic HSM-side zeroization

Single firmware function in `Kayten_Crypto.c`. Steps run in order; **must tolerate mid-step power loss** by repeating idempotently on boot when `provisioning_state` is seen as inconsistent.

1. Zeroize RAM-resident key schedule caches (voice-mode state, epoch key context, secure-execute transport keys, bootstrap secret slot 64 RAM copy).
2. Walk `Kayten_Crypto_KeyStore[]` and zeroize every active entry:
   - Identity pair (22, 23)
   - SPK band (35..43), public neighbours (36..44)
   - OPK band private (70, 72, …, 108), public neighbours (71, 73, …, 109)
   - Messaging bootstrap secret (64)
   - Any ephemeral / call-scoped slots
   - SRK (storage root key)
3. For each zeroized entry, overwrite the corresponding NVM block with `0x00` bytes (`NvM_WriteBlock`) and wait for `NvM_MultiBlockJob_Callout(NVM_WRITE_BLOCK)` completion. Block ids: `NVM_IDENTITY_KEY_PAIR_BLOCK_ID`, `NVM_SPK_BLOCK_ID_FIRST..LAST`, `NVM_OPK_KEY_PAIR_n_PRIVATE/PUBLIC_BLOCK_ID` (n ∈ 0..19), `NVM_MSG_BOOTSTRAP_SECRET_BLOCK_ID`, `NVM_SRK_BLOCK_ID`.
4. Clear `user_pin_block`: zero the PIN bytes, `user_pin_initialized = 0`, `remaining_pin_retries = 3` (reset for next provisioning cycle). Persist.
5. **Retain** `init_pin_block` (factory NVM, write-once during manufacture).
6. Write `last_wipe_reason = reason`, `last_wipe_unix = kCurrentUnix()`, `provisioning_state = PROVISIONING_REQUIRED` to `KAYTEN_NVM_RUNTIME_STATE_BLOCK`. Persist.
7. Emit `KAYTEN_DET_WIPE_COMPLETED` diag event.

If a power loss interrupts step 2-3 mid-walk, the boot path sees some zeroized entries, others intact. The recovery heuristic: **if `provisioning_state == PROVISIONED` but `user_pin_initialized == 0`** (an impossible live combination), run `Kayten_WipeAllKeyMaterial(reason = PIN_LOCKOUT_RECOVERY)` again before accepting any command. Guarantees eventual full wipe.

### 6a.3 App-side response to `DEVICE_WIPED`

All app platforms (kayten-app, kayten-app-v2):

**Step 0 — Silent-wipe detection (new, normative; runs on every app cold start *before* any HSM crypto operation beyond `0x43` / `0x49` / `0x66`).**
```
runtime = GET_RUNTIME_STATUS_V1()
if runtime.provisioning_state == PROVISIONING_REQUIRED
   AND local_state.registered_device_id != null:
   // DEVICE_WIPED response to the failing 0x45 was lost, OR power-loss
   // completed a wipe the app never observed. Treat as if step 1 below
   // just fired, using runtime.last_wipe_reason as reason.
   fall through to step 1
```
Detects the case where the third-bad-PIN response was dropped in transit or the device powered off mid-wipe.

1. **Server notification via durable capability (outbox-first, MUST run before local wipe touches Keychain/Keystore/SQLDelight):**
   - **Pre-requisite at enrollment + on every successful login:** app calls `DeviceService.IssueSelfRevokeCapability` (authenticated gRPC) → receives `capability_jwt` (server-signed, `{sub=device_id, aud="self-revoke", jti=<uuid>, exp=now+30d}`). Persist this JWT into the **native outbox companion storage** (see below) immediately on receipt, replacing any prior value. **Never** store the `capability_jwt` inside Keychain / Keystore / wrapped-key DB — it MUST survive the local wipe.
   - On wipe trigger, write outbox entry `{device_id, capability_jwt, reason, silent:bool, enqueued_at}` to the durable file:
     - **Android:** `kayten_wipe_revocation_outbox` (`EncryptedSharedPreferences` in `no_backup` app-private storage — app sandbox only, no Keystore-derived keys).
     - **iOS:** `Application Support/Kayten/WipeRevocationOutbox.plist` (App Sandbox, **`NSFileProtectionCompleteUnlessOpen`** — NOT `CompleteUntilFirstUserAuthentication` — so the `BGAppRefreshTask` drainer can read the file even after reboot before first unlock; excluded from iCloud backup via `isExcludedFromBackupKey = true`). See §10 #17 for the security tradeoff.
     - **Dart-only state:** enqueue via platform channel to the native outbox above; do NOT keep sole copies in in-memory `Provider` state.
   - **Happy path (still-online, still-authed):** attempt `DeviceService.RevokeDevice(reason)` over the live JWT with exponential backoff up to 60 seconds (NOT 5 minutes — that delays the user's re-provisioning flow unnecessarily). On success, clear the outbox entry and continue to step 2.
   - **Post-wipe drain path (outbox still has an entry after step 2):** `WorkManager` (Android) / `BGAppRefreshTask` (iOS) drains by calling `DeviceService.RevokeDeviceByCapability(capability_jwt, reason)` — an **unauthenticated** RPC (no Bearer). The server validates the JWT signature + expiry + single-use `jti` and runs the standard revocation flow. This path works after Keychain/Keystore are wiped because the capability is stored outside them. Drain retries on any `jti`-not-yet-used response; gives up on `jti-used` / `expired` / `signature-invalid`.
2. **Local wipe** — atomic, must not bail on first error:
   - Delete all Keychain (iOS) / Keystore (Android) entries in the `com.kayten.*` namespace.
   - Drop the wrapped-key database (schema `v2` per §7 kayten-app row).
   - Clear the conversation session cache (ratchet state, peer SPK cache, OPK inventory).
   - Dump the undelivered-message queue.
   - Clear the cached device identity + SRK.
   - **DO NOT delete** the wipe-revocation outbox file — it's intentionally outside the wipe scope so the drain can complete post-wipe.
3. **UX** — navigate to the provisioning screen with a non-dismissable banner keyed off `runtime.last_wipe_reason`:
   - `PIN_LOCKOUT` → *"Your HSM was wiped due to 3 failed PIN attempts. Re-enter your init PIN (printed on the device label) and choose a new user PIN."*
   - `USER_REQUEST` → *"Your HSM has been wiped. Re-enter your init PIN to set up the device."*
   - `PIN_LOCKOUT_RECOVERY` → *"Your HSM completed a wipe after a power interruption. Re-enter your init PIN to re-provision."*
   No option to skip or bypass. Conversations and message history are gone.

### 6a.3.1 Capability-token lifecycle (normative)

| Event | Client action | Server action |
|---|---|---|
| Enrollment success | Call `IssueSelfRevokeCapability` (auth) | Issue JWT `{sub, aud="self-revoke", jti, exp=now+30d}`; record `jti` as pending in `revocation_capability_jti` table. |
| Each successful `LOGIN_USER_V1` + server auth | Call `IssueSelfRevokeCapability` (auth); overwrite outbox-stored JWT | Issue fresh JWT with new `jti`; mark prior `jti` superseded. |
| `RevokeDeviceByCapability(token, reason)` drain | Unauth call from outbox drain | Verify sig + expiry + `jti` not used; mark `jti=used`; run standard `RevokeDevice` path (prekeys used, inbound queue cleared, fan-out, `identity_keys.revoked_at` populated). |
| Same `jti` submitted twice | — | Second call returns `FAILED_PRECONDITION` ("already consumed"); app drain treats this as success (idempotent; clear outbox). |
| Expired JWT | Drain gives up with local log; user must re-provision to re-establish server state | Returns `UNAUTHENTICATED`. |
| New device provisioning | — | Old JWT's `jti` remains marked used; doesn't overlap new device (new `device_id`). |

Attacker model: a leaked `capability_jwt` lets the attacker revoke the legitimate device once. This is a denial-of-service, not a key-material compromise — the revocation itself triggers `KEY_CHANGE_ALERT` fan-out and partner re-verification, so the attacker gains nothing beyond forcing the user to re-provision. Single-use + 30-day expiry bounds blast radius.

### 6a.4 Server-side response to `RevokeDevice(pin_lockout_wipe)`

Existing `service.go:331-394` flow runs unchanged for the `DEVICE_REVOKED` + `KEY_CHANGE_ALERT` fan-out. New additions (see §9a):

- `devices.revoked_at` timestamp populated (existing column).
- **`prekeys` (unified table, `migrations/005_keys.up.sql`):** set `used = TRUE` (and `used_at = NOW()` where appropriate) for **all** rows for `device_id` — this retires both SPK-like and OPK-like material in one step; there is no separate `signed_prekey` / `one_time_prekeys` / `is_active` schema in this repo.
- Device's inbound undelivered message queue: soft-delete rows where `recipient_device_id = <wiped>`.
- **`identity_keys`:** populate `revoked_at` on the response path — either add `identity_keys.revoked_at TIMESTAMPTZ` via migration and set `NOW()`, or return `devices.revoked_at` joined in `GetIdentityKey` until the column exists (§9a field is the contract either way).
- Partner re-verification: implement using **actual** trust / enrollment tables in this codebase (e.g. `hsm_enrollment_records`, attestation); do **not** reference `contacts.is_verified` unless a migration introduces it.

### 6a.5 Post-wipe re-provisioning

User re-enters `init_pin` + picks a new `user_pin` → `PROVISION_DEVICE_V1` succeeds → new identity pub is generated → app calls `DeviceService.RegisterIdentityKey` with the new pub. Server creates a **fresh** device row (new `device_id`, not a revival of the old revoked one). Old `device_id` stays revoked in history for audit.

## 7. Per-repo ownership

| Repo | Scope |
|---|---|
| **uHSM-HSM** | (1) Introduce `Kayten_Crypto.{c,h}` as the sole crypto call-site (§2.2). (2) **Delete** `Src/BSW/Csm/`, `Src/GenData/Csm_Cfg.{c,h}`, `Src/GenData/CryIf_Cfg.{c,h}`, `Src/BSW/SchM/SchM_Csm.h`, `Src/BSW/SchM/SchM_CryIf.h`, `Rte_Csm_Type.h`. Remove all `CsmKey_*` / `CryIfKey_*` references from `kHsm_Kayten.c` (~6 `default:` fall-through branches at lines 5368..6181 today). (3) Add handlers for `0x60..0x68` in a new `Src/BSW/kHsm/Kayten_Mobile.c` (`0x67` dev-only per §2.4.1). (4) Replace `HandleMobileProvisionToken` with the §5.1 clean-sheet handler — including the **NVM write-ordering contract (§5.1.2)** and **init_pin throttle (§5.1.1)** (§2.4). (5) Implement `Kayten_WipeAllKeyMaterial` per §6a.2. (6) Extend `KAYTEN_IS_ALLOWED_INNER_CMD` per §4 — **now includes `KAYTEN_CMD_LOGIN_USER_V1`**. (7) Seal the factory-fused `init_pin` NVM block (write-once, JTAG-provisioned). (8) Boot-path recovery heuristic per §6a.2 final paragraph. (9) Remove every `Kayten_Process*` function that implements a PKCS#11 semantic if any exist in firmware. (10) **New NVM slot bands** per §9b — allocate `110..117` (secure channel), `118..129` (voice ephemeral), `130..191` (conversation), `192..239` (general ephemeral) in the firmware NVM layout; reserve but do not yet populate. |
| **uHSM-Host** | (1) Create `uHSM-Host/Src/Mobile/Appl/kMobileManager.{c,h}` as the sole SPI dispatcher — only the §3.1 + §3.2 command ids. (2) **Delete** `uHSM-Host/Src/PKCS11/` entirely (directory goes away). (3) **Delete** any `Csm_*` / `CryIf_*` imports on the host; the mobile host has no reason to call AUTOSAR crypto abstractions — SPI-transport secure-channel ECDH for `0x55` becomes a direct `Crypto_kHW` call. (4) New `spi_status_codes.h` containing the additions: `SPI_STATUS_CMD_NOT_AVAILABLE (0xE0)`, `SPI_MOBILE_STATUS_KEY_ID_NOT_READABLE (0xE1)`, `SPI_MOBILE_STATUS_KEY_ID_ROTATE_ONLY (0xE2)`, `SPI_MOBILE_STATUS_KEY_ID_STALE (0xE3)`, `SPI_MOBILE_STATUS_SESSION_NOT_READY (0xED)`, `SPI_MOBILE_STATUS_DEVICE_WIPED (0xEE)`, `SPI_MOBILE_STATUS_ALREADY_PROVISIONED (0xE4)`, `SPI_MOBILE_STATUS_INIT_PIN_INVALID (0xE5)`, `SPI_MOBILE_STATUS_WIPE_NONCE_INVALID (0xEF)`, **`SPI_MOBILE_STATUS_INIT_PIN_THROTTLED (0xF5)`** (§5.1.1). (5) `Pkcs11ObjectRegistry`, `CsmKey_*` / `CryIfKey_*` aliasing tables, `spi_get_attribute_cmd_t` et al. all go away with the directory. (6) Top-level SPI entry point renamed `kMobileManager_DispatchCommand` — the old name `kPkcs11Manager_HandleSpiDispatch` disappears. (7) **Bare-outer `0x45` rejection** when `SPI_CAP_MOBILE_PROFILE_V1` is advertised (same rule as `0x46` — §3.1 / §3.1.1). |
| **kayten-proto** | Add `kayten/hsm_keys/v1/hsm_keys.proto` defining `CryptoKeyId` (band boundaries: identity, SPK, OPK, **plus new bands secure-channel `110..117`, voice-ephemeral `118..129`, conversation `130..191`, ephemeral `192..239`** — see §9 §9b) + `MobileCommandId` (through `MOBILE_COMMAND_ID_REQUEST_WIPE_CHALLENGE_V1 = 104` / wire `0x68`) + `ProvisioningState` + `WipeReason` enums. **Also add two RPCs on `DeviceService`** (§9c): `IssueSelfRevokeCapability(auth)` + `RevokeDeviceByCapability(unauth)` with matching request/response messages. Regenerate Dart + Rust + Kotlin + Swift + Go. **Additive** edits to existing messages **only** for §9a (`GetIdentityKeyResponse.revoked_at`); all other wire deltas stay in new package / new messages. Ship **wire↔enum translator reference impls** for ALL FIVE consumer languages — Dart + Kotlin + Go + Rust + Swift — in `references/translators/` (§9d). `kayten-app-v2` regenerates Rust / Swift bindings in its own workstream and needs the reference files pinned at the same proto SHA it bumps to. |
| **kayten-app** (v1) | (1) Delete `HsmCommand.Legacy` subtree. (2) Delete `Pkcs11ObjectResolver`, `Pkcs11ObjectRegistry`. (3) Delete `kSlot*` logical slot constants; replace with proto-generated `CryptoKeyId`. **Renumber internal slot ranges** to the new bands: secure-channel `9..12` → `110..117`, voice `42..43` → `118..119` (inside `118..129`), messaging `13..30` → `130..159` (inside `130..191`). Wire format for `0x40/0x41/0x42` 1-byte `convKey*` fields unchanged (new numeric values still fit in 0–255). (4) Rewrite `HsmService`, `CryptoOperations`, `KeyManagement`, `SecureChannel`, `VoiceModeCrypto`, `MessageKeyWrapper` to use §3.2 semantic commands + `crypto_key_id` directly. (5) Schema-v2 wrapped-key store with `crypto_key_id: Int` columns; **atomic replacement + forced re-enrollment** on first launch after upgrade. (6) New UI flow: provisioning screen (init-pin + user-pin entry), PIN-retry countdown, lockout-wipe handler (§6a.3: **capability-token outbox + silent-wipe step 0 + ordering**), post-wipe re-provisioning screen (§6a.5), user-initiated wipe: **`0x68` then SECURE_EXECUTE `0x65`** (§5.8). (7) **Both `LOGIN_USER_V1 (0x45)` and `CHANGE_USER_PIN_V1 (0x46)`** on hardware prod: inner `0x55` path only (§3.1 / §3.1.1 / §4). (8) New error-state handling for `DEVICE_WIPED` → navigate to provisioning. (9) **Capability-token lifecycle (§6a.3.1):** call `DeviceService.IssueSelfRevokeCapability` immediately after enrollment *and* on every successful login; persist returned `capability_jwt` to the durable native outbox companion file (`kayten_wipe_revocation_outbox` / `WipeRevocationOutbox.plist`), **never** in Keychain/Keystore. WorkManager / BGAppRefreshTask drains via `DeviceService.RevokeDeviceByCapability` post-wipe. (10) **Silent-wipe probe (§6a.3 step 0):** call `GET_RUNTIME_STATUS_V1` on every cold start before any other HSM op; trigger wipe-response handler if `PROVISIONING_REQUIRED` but app still holds a registered `device_id`. |
| **kayten-app-v2** | **Full Rust-core refactor authorized — NO backwards compat.** Per product direction, v2 has no obligation to preserve its prior schema, wrapped-key layout, or transport interfaces, and no obligation to speak the legacy PKCS#11 wire under any flag. Clean-sheet `rust-core/kayten-keymanager`: delete every `pkcs11` / `session_handle` / `object_handle` / `CKA_*` / `CKO_*` / `CKK_*` identifier (types AND call sites — no deprecated wrappers); restructure the module tree around semantic commands; target ≤ 500 LOC for the HSM-transport layer. Add the four new slot bands (110..239) as Rust constants mirroring proto `CryptoKeyId`. Update `NativeHsmTransport.kt` + `ExternalHsmClient.swift` as thin pass-throughs to the Rust core. `SimulatedTransport` / SoftHSM implements the full semantic command matrix incl. `0x68`/`0x65`/`0x66` + init-pin throttle + NVM-style write-order contract with crash-injection test hooks. Capability-token outbox lifecycle (§6a.3 / §6a.3.1) + silent-wipe probe on cold start + `0x45` via `0x55` inner in hw prod. **If the host does not advertise `SPI_CAP_MOBILE_PROFILE_V1`, v2 refuses to bootstrap with a "firmware / host too old" error — no fallback path.** On first launch post-upgrade, drop any existing wrapped-key DB / Keychain / Keystore entries and treat the device as fresh. Ship `translator.rs` + `translator.swift` alongside the Kotlin/Dart mirrors — MANDATORY in this workstream, not follow-up. Archive `docs/v2.1/uhsm_host_mobile_gateway_spec_2026-03-27.md` and siblings under `docs/archive/2026-04-17-pkcs11-retirement/`. |
| **kayten-server** | **Mandatory work.** (1) `DeviceService.RevokeDevice` gains §6a.4 semantics: mark **all** `prekeys` rows `used = TRUE` for the device, clear undelivered inbound queue, expose revocation timestamp on identity fetch (`revoked_at` per §9a — column on `identity_keys` or join from `devices`). (2) `GetPrekey` / bundle paths must refuse revoked devices (`NOT_FOUND` or equivalent); verify against current `prekeys` + `devices.revoked_at` logic. (3) `GetIdentityKeyResponse` gains `revoked_at` (proto §9a). (4) Migration / indexes: tune bulk revoke on **`prekeys(device_id)`** (partial index on `NOT used` already exists — extend or add `WHERE device_id = $1` batch update plan as needed). (5) Optional: `kayten_device_wiped_total{reason}`. (6) **NEW — capability-token plumbing (§6a.3.1 / §9c):** add two RPC handlers — `IssueSelfRevokeCapability` (auth-gated) and `RevokeDeviceByCapability` (unauth, signature-gated). Add `revocation_signing_key` (HMAC-SHA256 secret loaded from existing config secret-store, rotatable). Add migration `014_revocation_capability_jti.up.sql` with table `revocation_capability_jti(jti UUID PK, device_id TEXT NOT NULL, issued_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL, used_at TIMESTAMPTZ NULL, superseded_at TIMESTAMPTZ NULL)` + unique partial index `WHERE used_at IS NULL`. Rate-limit `RevokeDeviceByCapability` to ≤ 3 requests / device_id / minute (counter keyed by JWT `sub`). On success, run same fan-out as authed `RevokeDevice`. (7) **Status code expose:** plumb `SPI_MOBILE_STATUS_INIT_PIN_THROTTLED (0xF5)` as a distinct gRPC error code for server-side logging of throttled devices (no direct server action needed — the HSM enforces the throttle; the server just labels telemetry). Proto deltas for consumers: `GetIdentityKeyResponse.revoked_at`, `IssueSelfRevokeCapabilityResponse`, `RevokeDeviceByCapabilityRequest/Response`. |

## 8. Sequencing

Per user direction: *"opk/dh4 are separate, we already defined new commands for our use case."* The OPK/DH4 workstream (`2026-04-16-cross-repo-opk-nvm-band-and-dh4-spec.md`) lands first on its current timeline. This retirement workstream *then* fans out:

```
Week 0 (already in flight):   OPK/DH4 workstream merges
                              - kayten-proto           (no changes in 04-16 review)
                              - uHSM-HSM               (5C + 5B + band 70..109)
                              - uHSM-Host              (thin forwarder updates)
                              - kayten-server          (idempotent receive-path)
                              - kayten-app / -v2       (OPK-bundle consumption)

Week 1 (this workstream starts):
  Branch A (parallel, independent of B/C/D):
    kayten-proto - hsm_keys.v1 constants package (no wire changes except
                   GetIdentityKeyResponse.revoked_at for §7 server row)
  Branch B:
    uHSM-HSM - Kayten_Crypto.{c,h} introduction; delete Csm/CryIf; new §3.2
               handlers (0x60..0x68; 0x67 dev-only); clean-sheet PROVISION_DEVICE_V1;
               Kayten_WipeAllKeyMaterial; boot-path recovery heuristic.
               All behind KAYTEN_HSM_MOBILE_PROFILE_V2, default STD_OFF.
  Branch C:
    uHSM-Host - add `kMobileManager.{c,h}` and route new commands through it
               behind compile-time `KAYTEN_HOST_MOBILE_PROFILE_V2` (default OFF
               for bisect). **Do not** describe a production “runtime flag” that
               keeps PKCS#11 on the SPI path — that contradicts §2.1. Optional:
               a **separate lab firmware target** may still link `Src/PKCS11/` for
               A/B bench images only; the app-shipped mobile binary deletes PKCS#11
               in Week 3. Advertise `SPI_CAP_MOBILE_PROFILE_V1` when the semantic
               path exists; apps must not require it until Week 3 cut-over.
  Branch E (parallel):
    kayten-server - DeviceService.RevokeDevice extended (`prekeys.used`,
                    queue cleanup, `GetIdentityKeyResponse.revoked_at`); any DB
                    migrations + index review for bulk prekey revoke; optional
                    metric kayten_device_wiped_total

Week 2:
  Branch D (app work, large):
    kayten-app    - purge HsmCommand.Legacy, Pkcs11ObjectResolver, kSlot*;
                    repipe all call sites through §3 commands; schema-v2
                    migration with forced re-enrollment; provisioning UI;
                    PIN-retry + lockout-wipe flow; post-wipe re-provisioning
                    screen; user-initiated-wipe setting
    kayten-app-v2 - smaller surface, same concept

Week 3:
  Cut-over (all-or-nothing merge boundary):
    - uHSM-HSM flips KAYTEN_HSM_MOBILE_PROFILE_V2 = STD_ON, deletes every
      PKCS#11 handler + all Csm/CryIf modules
    - uHSM-Host mobile build deletes Src/PKCS11/ directory
    - Apps require SPI_CAP_MOBILE_PROFILE_V1 in PROBE_STATE; refuse older hosts
    - kayten-server: ship any pending migrations (`GetIdentityKeyResponse.revoked_at`,
      optional `identity_keys.revoked_at`, index tuning for bulk `prekeys` revoke)
```

Each week is a merge boundary; no partial mixes. Full-mode production binaries ship at end of week 3. An old-firmware field device connected to a new app refuses to bootstrap with an explicit `"HSM firmware too old — please update via <OTA link>"` error; an old-app device connected to a new HSM refuses with `SPI_STATUS_CMD_NOT_AVAILABLE` on its first legacy command.

## 9. Proto changes

**Exactly one new file:** `kayten/hsm_keys/v1/hsm_keys.proto` in `kayten-proto`, containing:

```proto
syntax = "proto3";
package kayten.hsm_keys.v1;

enum CryptoKeyId {
  CRYPTO_KEY_ID_UNSPECIFIED = 0;
  CRYPTO_KEY_ID_IDENTITY_PRIV = 22;
  CRYPTO_KEY_ID_IDENTITY_PUB  = 23;
  CRYPTO_KEY_ID_SPK_PRIV_FIRST = 35;
  CRYPTO_KEY_ID_SPK_PUB_FIRST  = 36;
  CRYPTO_KEY_ID_SPK_PRIV_LAST  = 43;
  CRYPTO_KEY_ID_SPK_PUB_LAST   = 44;
  CRYPTO_KEY_ID_MSG_BOOTSTRAP_SECRET = 64;
  CRYPTO_KEY_ID_OPK_PRIV_FIRST = 70;
  CRYPTO_KEY_ID_OPK_PUB_FIRST  = 71;
  CRYPTO_KEY_ID_OPK_PRIV_LAST  = 108;
  CRYPTO_KEY_ID_OPK_PUB_LAST   = 109;
  // NEW bands (§9b): closes namespace collision where today's kayten-app
  // slots 9..12 (secure-channel), 13..30 (messaging LRU), 42..43 (voice)
  // overlap identity/SPK bands. Renumbering into these bands keeps 1-byte
  // convKey wire fields on 0x40/0x41/0x42 unchanged (values fit in 0..255).
  CRYPTO_KEY_ID_SECURE_CHANNEL_FIRST   = 110;  // 8 slots, Doppel-AES session keys
  CRYPTO_KEY_ID_SECURE_CHANNEL_LAST    = 117;
  CRYPTO_KEY_ID_VOICE_EPHEMERAL_FIRST  = 118;  // 12 slots, per-call ECDH + epoch
  CRYPTO_KEY_ID_VOICE_EPHEMERAL_LAST   = 129;
  CRYPTO_KEY_ID_CONVERSATION_FIRST     = 130;  // 62 slots = 31 convos × 2 (LRU)
  CRYPTO_KEY_ID_CONVERSATION_LAST      = 191;
  CRYPTO_KEY_ID_EPHEMERAL_FIRST        = 192;  // 48 slots, returned by
                                               //   GENERATE_MESSAGING_KEY_V1
                                               //   { kind=EPHEMERAL_ECDH }
  CRYPTO_KEY_ID_EPHEMERAL_LAST         = 239;
  // 240..255 reserved
}

enum MobileCommandId {
  MOBILE_COMMAND_ID_UNSPECIFIED = 0;
  PROBE_STATE_V1                  = 0x43;
  PROVISION_DEVICE_V1             = 0x44;        // renamed from PROVISION_TOKEN_V1
  LOGIN_USER_V1                   = 0x45;
  CHANGE_USER_PIN_V1              = 0x46;
  READ_IDENTITY_PUBLIC_V1         = 0x47;
  IDENTITY_SIGN_V1                = 0x48;
  GET_RUNTIME_STATUS_V1           = 0x49;
  LOGOUT_USER_V1                  = 0x4A;
  VOICE_ENCRYPT_FRAME_V1          = 0x4B;
  VOICE_DECRYPT_FRAME_V1          = 0x4C;
  RESET_SESSION_STATE_V1          = 0x4D;
  WRAP_STORAGE_ROOT_KEY_V1        = 0x4E;
  UNWRAP_STORAGE_ROOT_KEY_V1      = 0x4F;
  CALL_KEY_AGREE_V1               = 0x53;
  CALL_TEARDOWN_V1                = 0x54;
  SECURE_EXECUTE_V1               = 0x55;
  MSG_KEY_EXCHANGE_V1             = 0x57;
  GET_HSM_DET_BUFFER_V1           = 0x59;
  PEER_RATCHET_STEP_V1            = 0x5A;
  MSG_KEY_EXCHANGE_RESPONDER_V1   = 0x5B;
  GENERATE_OPK_V1                 = 0x5C;
  GENERATE_MESSAGING_KEY_V1       = 0x60;
  READ_PUBLIC_KEY_V1              = 0x61;
  DELETE_MESSAGING_KEY_V1         = 0x62;
  ROTATE_SPK_V1                   = 0x63;
  GENERATE_RANDOM_V1              = 0x64;
  USER_INITIATED_WIPE_V1          = 0x65;        // §3.2 NEW
  GET_WIPE_STATUS_V1              = 0x66;        // §3.2 NEW
  DEV_SET_INIT_PIN_V1             = 0x67;        // §3.2 DEV-only
  REQUEST_WIPE_CHALLENGE_V1       = 0x68;        // §5.8 — checked-in proto SHOULD use `= 104` (decimal); see per-repo spec §3.0.1
}

enum ProvisioningState {
  PROVISIONING_STATE_UNSPECIFIED = 0;
  PROVISIONING_REQUIRED          = 1;   // wire value 0x00
  PROVISIONED                    = 2;   // wire value 0x01
  LOCKED_WIPED                   = 3;   // transient, wire value 0x02
}

enum WipeReason {
  WIPE_REASON_UNSPECIFIED  = 0;
  WIPE_REASON_NONE         = 1;   // wire 0x00
  WIPE_REASON_PIN_LOCKOUT  = 2;   // wire 0x01
  WIPE_REASON_USER_REQUEST = 3;   // wire 0x02
  WIPE_REASON_ATTESTATION  = 4;   // wire 0x03
}
```

> **Canonical enum / message text:** The block above is a **shape** reference.
> Normative member names (prefixed enums, full `WIPE_REASON_PIN_LOCKOUT_RECOVERY`,
> package paths) live in `2026-04-17-kayten-proto-hsm-keys-constants-spec.md` §3.
> **`CryptoKeyId` typing:** treat the enum as **named band boundaries + sentinel
> only**; arbitrary ids inside a band (e.g. SPK priv `37`) appear as numeric
> `uint32` on the wire — see per-repo spec §3 for the `uint32` vs enum pattern
> for app-generated code.
>
> **⚠ Two different wire↔enum-value conventions in the block above — read carefully:**
>
> 1. **`MobileCommandId`, `CryptoKeyId`** — enum value **equals** wire byte. No `_UNSPECIFIED = 0` entry because every valid wire byte is non-zero (SPI command bytes start at 0x40; crypto_key_id starts at 22). Checked-in proto still sets `*_UNSPECIFIED = 0` per proto3 rules, but all other values are `enum_value == wire_value` with no offset.
>
> 2. **`ProvisioningState`, `WipeReason`** — enum value **= wire + 1**, because wire 0x00 is a valid state/reason (`PROVISIONING_REQUIRED`, `WIPE_REASON_NONE`) but proto3 reserves enum `0` for `_UNSPECIFIED`. Consumers MUST translate via the `references/translators/translator.{dart,kt,go,rs,swift}` helpers (per-repo spec §5.1) — do NOT cast directly.
>
> All five translator impls must preserve this split: a `MobileCommandId` translator is a no-op identity (numeric cast) whereas a `ProvisioningState` / `WipeReason` translator is the mechanical switch-mapping shown in per-repo spec §5.1.

### 9a. `kayten-server` proto delta

The only cross-repo proto change is an additive field on an existing response:

```proto
// kayten/v1/device.proto
message GetIdentityKeyResponse {
  bytes ed25519_pub = 1;
  bytes ecdh_pub    = 2;
  bytes signature   = 3;
  google.protobuf.Timestamp revoked_at = 4;  // NEW — null/unset means active
}
```

Partners use `revoked_at != null` to short-circuit prekey fetches and mark the conversation state as "key_change_pending_reverify".

### 9b. New slot bands — rationale + migration

Today's kayten-app uses logical slot numbers that pre-date the unified `crypto_key_id` scheme: secure-channel 9..12 (`kSlotSecureChannelStart..End`), messaging LRU 13..30 (`kSlotMessagingStart..End`), voice ephemeral 42..43 (`kSlotVoiceEpoch*`). After this spec renumbers the mobile command surface, slot 22/23 become identity (PRIV/PUB) and 35..44 become the SPK band — creating direct collisions with the historical layout.

Four new bands close the collision without touching the existing 1-byte `convKeyInner` / `convKeyOuter` wire format on `0x40` / `0x41` / `0x42`:

| Band | Range | Slots | Replaces | Uses |
|---|---|---|---|---|
| Secure-channel | 110..117 | 8 | `kSlotSecureChannelStart..End` (9..12) | Doppel-AES inner/outer keys for `0x55` session(s). 4 sessions × 2 keys. |
| Voice ephemeral | 118..129 | 12 | `kSlotVoiceEpoch*` (42..43) | Per-call ECDH + epoch keys; 3 concurrent calls × 4 keys. |
| Conversation | 130..191 | 62 | `kSlotMessagingStart..End` (13..30) | LRU-managed per-peer messaging keys; 31 conversations × 2 keys (`convKeyInner`, `convKeyOuter`). |
| Ephemeral | 192..239 | 48 | — (new) | Firmware-assigned ids returned by `GENERATE_MESSAGING_KEY_V1 { kind = EPHEMERAL_ECDH }` and readable via `READ_PUBLIC_KEY_V1`. |

Wire format impact: zero. All new values fit in 0..255, so 1-byte `convKey*` fields on existing messaging commands carry the new numeric values directly. Firmware range-checks become one-liners per band (§10 consideration #4 preserved).

Schema-v2 forced re-enrollment (§10.4) cleanly brings apps into the new numbering — no incremental migration needed.

### 9c. New RPCs on `DeviceService` — capability-based self-revoke

Closes the post-wipe authentication gap (§6a.3 step 1, §6a.3.1). Additive on existing `kayten.v1.device.proto`:

```proto
service DeviceService {
  // ... existing RPCs ...

  // Auth-gated (Bearer JWT). Called at enrollment success and on every
  // successful LOGIN. Server issues a single-use signed capability JWT
  // scoped to "revoke the caller's device". App stores the returned JWT
  // in its native outbox companion file (NOT in Keychain/Keystore) so
  // it survives the local wipe that accompanies a DEVICE_WIPED event.
  rpc IssueSelfRevokeCapability(IssueSelfRevokeCapabilityRequest)
      returns (IssueSelfRevokeCapabilityResponse);

  // UNAUTHENTICATED. Called from the app's wipe-revocation-outbox drain
  // (WorkManager / BGAppRefreshTask) after the local wipe has deleted
  // the JWT refresh token. Validates the capability_jwt signature, exp,
  // and single-use jti; runs the standard RevokeDevice path on success.
  rpc RevokeDeviceByCapability(RevokeDeviceByCapabilityRequest)
      returns (RevokeDeviceByCapabilityResponse);
}

message IssueSelfRevokeCapabilityRequest {
  // Empty — device_id derived from auth context.
}
message IssueSelfRevokeCapabilityResponse {
  // Compact JWT: sub=device_id, aud="self-revoke", jti=uuid, exp=now+30d.
  // Signed with kayten-server-side revocation_signing_key (HS256, rotatable).
  string capability_jwt                  = 1;
  google.protobuf.Timestamp expires_at   = 2;
}

message RevokeDeviceByCapabilityRequest {
  string capability_jwt = 1;
  string reason         = 2;  // e.g. "pin_lockout_wipe", "user_requested", "silent_wipe_detected"
}
message RevokeDeviceByCapabilityResponse {
  // Empty. gRPC status carries outcome; FAILED_PRECONDITION for already-consumed jti.
}
```

Server state:
- HS256 `revocation_signing_key` loaded from existing config secret-store. Rotatable.
- Table `revocation_capability_jti(jti UUID PK, device_id TEXT, issued_at TIMESTAMPTZ, expires_at TIMESTAMPTZ, used_at TIMESTAMPTZ NULL, superseded_at TIMESTAMPTZ NULL)`.
- Unique partial index on `(device_id) WHERE used_at IS NULL` to cheaply expire prior tokens on `IssueSelfRevokeCapability` (mark prior `used_at`/`superseded_at`).
- Migration: `014_revocation_capability_jti.up.sql` / `014_revocation_capability_jti.down.sql`.

Rate-limit `RevokeDeviceByCapability` at ≤ 3 requests / `sub` / minute (keyed by JWT `sub`). IP-based rate-limit layered on top.

Attacker model recap (from §6a.3.1): a leaked `capability_jwt` lets attacker revoke the legit device once — DoS, not key compromise; revocation triggers `KEY_CHANGE_ALERT` fan-out and partner re-verification, so attacker gains nothing beyond forcing user to re-provision.

### 9d. Wire↔enum translator reference implementations

`proto3` forces `_UNSPECIFIED = 0`, so our `ProvisioningState` and `WipeReason` enums carry `(wire + 1)` numeric values. Five languages consume this enum (Dart, Kotlin, Rust, Swift, Go) — drift is the realistic failure mode. This spec requires **checked-in reference translators** in `kayten-proto` under `references/translators/` — **all five MANDATORY** in the Week-1 proto PR (kayten-app-v2 regenerates Rust + Swift bindings in its own workstream and the reference files must be available at the same pinned SHA):

- `translator.dart` — used by `kayten-app` and `kayten-app-v2` Dart surface
- `translator.kt` — used by both apps' Kotlin `HsmPlugin`
- `translator.go` — used by `kayten-server`
- `translator.rs` — used by `kayten-app-v2` rust-core
- `translator.swift` — used by `kayten-app-v2` iOS bridge

Each file ≤ 40 lines, contains `provisioningStateFromWire`, `wipeReasonFromWire`, and `toWire` inverses. Single canonical source per language; consumer repos copy-paste or `include`/`import` during their proto-regen step.

## 10. Security considerations

1. **Removing PKCS#11 removes attack surface.** Every `GetAttributeValue` template-parse, every `FindObjects` registry walk, every `C_Derive` key-wrap dance is dead code on the mobile host + HSM.
2. **Removing CSM / CryIf removes an entire abstraction layer with its own bug surface.** `Kayten_Crypto.c` is ~500 lines calling hardware primitives directly; it replaces ~4-6 kLoC of AUTOSAR-style glue whose only purpose was to satisfy SWS contracts the mobile product never used. Fewer indirections = fewer places for a `CsmKey_*` / `CryIfKey_*` mapping to be misconfigured and leak a cross-privilege key reference.
3. **`crypto_key_id`-only wire makes privilege checks trivially auditable.** Firmware's `KAYTEN_KEY_OPK_PRIV_FIRST <= id && id <= KAYTEN_KEY_OPK_PRIV_LAST && (id & 1) == 0` is a one-liner per command; the host cannot tamper with the reference because there's no registry mapping to manipulate.
4. **Forced re-enrollment on migration.** No version-spanning schema with both `object_handle` and `crypto_key_id` columns — guarantees no stale handle ever escapes into a production key reference.
5. **Atomic SPK generate-and-sign (`0x60`) + atomic rotate (`0x63`)** close the window where an old SPK could be used for signing after the identity key has authorized a new one. Current PKCS#11 path leaves that window open across three SPI roundtrips.
6. **PIN-based session only.** No dual-role (SO/user) model — only init-pin (factory-fused, recovery) + user-pin (everyday). SO operations collapse into factory provisioning off the SPI surface.
7. **PIN-lockout wipe is tamper-resistant** — atomic across HSM NVM, mobile key store, and server-side prekey inventory. An attacker who physically captures a locked device cannot extract keys because the third bad-PIN attempt zeroizes them before returning. See §6a.
8. **`init_pin` is a possession factor, not a secret** — since it's printed on the device label and survives wipes. It protects *replay of a wipe + re-provision* by requiring physical device possession at re-setup time. Loss of the label equals a bricked device (acceptable trade-off per the user direction — "3 failed PIN means locked device").
9. **Post-wipe, partner devices see `KEY_CHANGE_ALERT` + `GetIdentityKeyResponse.revoked_at`** (sourced from `identity_keys` and/or `devices` per §6a.4) — they cannot silently accept a new identity as the old one. The wiped device's new identity pub is a fresh row in `identity_keys`; partner UX must force re-verification per the product's trust model (not a literal `contacts.is_verified` column unless the DB adds one).
10. **`REQUEST_WIPE_CHALLENGE_V1 (0x68)` + `USER_INITIATED_WIPE_V1 (0x65)`** — `0x68` issues a TRNG `wipe_nonce` with bounded TTL in HSM RAM (single-shot). `0x65` (SECURE_EXECUTE-wrapped in hardware prod) must present the matching nonce **and** `user_pin`. Replay of an old `0x65` transcript fails after the nonce is burned or expires (`SPI_MOBILE_STATUS_WIPE_NONCE_INVALID (0xEF)`). A deterministic client-only token derived from public `identity_pub` would **not** provide freshness and is explicitly rejected by this design.
11. **`CHANGE_USER_PIN_V1 (0x46)` in hardware production** — MUST use the `0x55` inner-command path (`KAYTEN_CMD_CHANGE_USER_PIN_V1` in §4); bare outer `0x46` is rejected once `SPI_CAP_MOBILE_PROFILE_V1` is advertised so `user_pin` bytes never cross the SPI in plaintext in prod.
12. **`init_pin` has no server-side throttle** — `PROVISION_DEVICE_V1` returns `INIT_PIN_INVALID` on mismatch with **no** per-device retry counter (§5.1). An attacker with prolonged physical access could brute the 8-digit space; security relies on **possession of the label**, rate limits at the UI, and **partner re-verification** after any successful re-provision post-wipe (`KEY_CHANGE_ALERT`, `revoked_at`) so a recovered device cannot impersonate the old identity.
13. **Dev firmware is deliberately weakened but is loud about it** (§2.4.1) — `"00000000"` default PIN, `CMD_DEV_SET_INIT_PIN (0x67)` over the wire, `SPI_CAP_DEV_FIRMWARE` bit always on. PROD binaries omit the `0x67` handler via `#if` (not a runtime flag), so a compromised PROD firmware image can't be flipped into dev by setting a config bit. The app's red-banner on `SPI_CAP_DEV_FIRMWARE` surfaces mis-mixed builds to end-users within one probe cycle. `KAYTEN_INIT_PIN_STAGING_PINNED` looks like PROD on the wire (no dev bit) to keep internal-beta dogfood from drifting.
14. **`PROVISION_DEVICE_V1 (0x44)` carries PINs in plaintext outer SPI** — **once per provisioning event (initial + any post-wipe re-provision)**, NOT strictly "once per device lifetime." A device hitting `PIN_LOCKOUT` wipe (3 bad PINs → §6a.2) returns to `PROVISIONING_REQUIRED` and re-executes `0x44` on re-provisioning, exposing `user_pin` on the wire each time. Similarly for `USER_INITIATED_WIPE_V1 (0x65)` flows. The `0x55` secure channel (`android/…/SecureChannel.kt:562-620`) is Ed25519-signed by the HSM's *identity* key, which `0x44` is the command that generates. A pre-provision `0x55` is therefore physically impossible without downgrading the handshake to unauthenticated ephemeral-only ECDH (rejected — would give no MitM protection and fork the secure-channel implementation). Accepted risk: narrow attack surface (physical FT4222 bus access at re-provisioning moments); `init_pin` is already a physical possession factor printed on the device label; per-provisioning-event `user_pin` exposure is bounded to those discrete moments. `0x45 LOGIN_USER_V1` and `0x46 CHANGE_USER_PIN_V1` — which carry `user_pin` on every login / change outside the provisioning window — are fully protected via mandatory `0x55`-wrap in hardware prod (§3.1 / §3.1.1 / §4). Deployments with high wipe-frequency (enterprise/fleet, factory-line, high-assurance verticals) should promote two-phase provisioning (§13 follow-ups) earlier than its default Q2/Q3 2026 window — see trigger conditions in `2026-04-18-two-phase-provisioning-cross-repo-spec.md` §5.
15. **Self-revoke capability token (§6a.3 / §6a.3.1 / §9c)** — post-wipe `RevokeDevice` auth gap closed via a pre-issued single-use JWT stored in the native outbox companion file (`kayten_wipe_revocation_outbox` / `WipeRevocationOutbox.plist`), durably outside the Keychain/Keystore wipe scope. Leak model: a compromised `capability_jwt` enables **one** revocation — DoS, not key material compromise; the revocation itself triggers `KEY_CHANGE_ALERT` fan-out and partner re-verification, giving the attacker nothing beyond forcing the legitimate user to re-provision. Server rate-limits `RevokeDeviceByCapability` to 3 req/sub/min. 30-day expiry + single-use `jti` + rotation-on-login bounds blast radius.
16. **`init_pin` NVM-side throttle (§5.1.1)** — persistent exponential backoff (10 s → 60 s → 600 s → 3600 s after 3 free typos) reduces 10⁸-PIN-space brute-force from ~115 days at 100 ms/attempt to >19 years. Counter is NVM-resident and survives power cycles; only a factory reset (JTAG) clears it. No legitimate user hits the throttle past attempt 3.
17. **iOS capability-token outbox — `NSFileProtectionCompleteUnlessOpen` is deliberate** (§6a.3 step 1, §6a.3.1). The stricter `CompleteUntilFirstUserAuthentication` would make the outbox unreadable after a reboot before first unlock, and `BGAppRefreshTask` can fire in that window — the drain would silently fail right when we most need it (immediately post-wipe, possibly while the user is still setting up the device). Tradeoff: at-rest exposure of the encrypted plist between wipe and first unlock. Mitigation: the `capability_jwt` is **single-use** and **30-day-expiring** (§9c), so a leaked JWT lets an attacker revoke the legitimate device exactly once (DoS, not key-material compromise). The attack surface is narrow (physical device possession + file-system access during the wipe-to-unlock window) and strictly weaker than the already-accepted threat model for the `init_pin` on `0x44` (parent §10 #14).

## 11. Verification

`rg` checks that must pass on a post-week-3 tree:

```
rg "HsmCommand\.Legacy" kayten-app/ kayten-app-v2/                        # 0 hits
rg "Pkcs11ObjectResolver|Pkcs11ObjectRegistry" kayten-app/ kayten-app-v2/ # 0 hits
rg "kSlotIdentityEd25519|kSlotSignedPrekey|kSlotOpkStart" kayten-app/     # 0 hits
rg "CKA_|CKO_|CKK_" kayten-app/ kayten-app-v2/                            # 0 hits
rg "^\s*case\s+CMD_SIGN_INIT|^\s*case\s+CMD_VERIFY_INIT|^\s*case\s+CMD_WRAP_KEY|^\s*case\s+CMD_UNWRAP_KEY|^\s*case\s+CMD_DERIVE_KEY|^\s*case\s+CMD_GENERATE_KEY_PAIR|^\s*case\s+CMD_FIND_OBJECTS_INIT|^\s*case\s+CMD_GET_ATTRIBUTE_VALUE|^\s*case\s+CMD_CREATE_OBJECT|^\s*case\s+CMD_DESTROY_OBJECT" uHSM/uHSM-Host/Src/Mobile/    # 0 hits
rg "kPkcs11Manager"    uHSM/uHSM-Host/Src/Mobile/                          # 0 hits
rg "Csm_|CsmKey_|CryIf_|CryIfKey_" uHSM/uHSM-Host/Src/Mobile/ uHSM/uHSM-HSM/Src/   # 0 hits outside docs/archive/
test -d uHSM/uHSM-Host/Src/PKCS11                                          # non-existent (directory removed)
test -d uHSM/uHSM-HSM/Src/BSW/Csm                                          # non-existent (directory removed)

# PROD firmware must not link the dev-only 0x67 handler:
nm build/prod/uHSM-HSM.elf | rg "HandleDevSetInitPin|Kayten_ProcessDevSetInitPin"    # 0 hits
# Conversely the DEV firmware must link it:
nm build/dev/uHSM-HSM.elf  | rg "HandleDevSetInitPin|Kayten_ProcessDevSetInitPin"    # 1+ hits
```

End-to-end tests:

- **Fresh HSM bring-up**: `PROVISION_DEVICE_V1 { init_pin, user_pin }` → `LOGIN_USER_V1` → `READ_PUBLIC_KEY_V1 (IDENTITY_PUB)` → non-empty 32 B pub returned, no `0x33`, no `0x30/0x31/0x32` ever on the wire.
- **Re-enrollment on upgrade**: app first launch post-upgrade detects schema-v1, wipes, runs full enrollment end-to-end. No user intervention beyond PIN entry.
- **Messaging bootstrap**: initiator + responder complete over `SECURE_EXECUTE_V1`-wrapped `0x57`/`0x5B` + `0x5C` calls only. Wire capture contains zero `0x01..0x3A` commands.
- **Voice call**: `CALL_KEY_AGREE_V1` → `GENERATE_EPOCH_KEY` → `VOICE_ENCRYPT_FRAME_V1` / `VOICE_DECRYPT_FRAME_V1`. Same wire cleanliness.
- **SPK rotation**: `ROTATE_SPK_V1` → server `UploadPrekeys` with the new SPK + signature. Zero `CMD_DESTROY_OBJECT` on wire.
- **PIN lockout wipe** (new): submit 3 bad PINs → assert `DEVICE_WIPED` response → assert `GET_WIPE_STATUS_V1` returns `PIN_LOCKOUT` → assert `0x49 GET_RUNTIME_STATUS_V1` returns `PROVISIONING_REQUIRED` → assert `NvM_ReadBlock(NVM_IDENTITY_KEY_PAIR_BLOCK_ID)` yields all-zero → assert app has called `DeviceService.RevokeDevice` with `reason = "pin_lockout_wipe"` → assert server DB has `devices.revoked_at != NULL`, **all** `prekeys` rows for that device have `used = TRUE`, and `GetIdentityKey` exposes non-null `revoked_at` (or equivalent join) → assert partner devices received `KEY_CHANGE_ALERT` via WebSocket → assert re-provisioning with `init_pin` + new `user_pin` yields a fresh `device_id` on the server.
- **Power-loss during wipe** (new): interrupt wipe mid-walk at each NVM block boundary; verify next boot completes the wipe via the recovery heuristic (§6a.2 final paragraph) before accepting any command.
- **User-initiated wipe** (new): `0x68 REQUEST_WIPE_CHALLENGE_V1` → receive `wipe_nonce` → `0x65 USER_INITIATED_WIPE_V1` (SECURE_EXECUTE-wrapped in prod) with correct `user_pin` + matching nonce → assert same outcomes as PIN-lockout except `WipeReason = USER_REQUEST`. Wrong PIN → `AUTH_FAILED`; wrong/expired nonce → `0xEF WIPE_NONCE_INVALID`; replay same `0x65` after success → `0xEF`.
- **Dev-firmware bring-up** (new, CI-only): fresh `DEV_RESETTABLE` board → boot → `0x43 PROBE_STATE_V1` returns `SPI_CAP_DEV_FIRMWARE = 1` → `0x44 PROVISION_DEVICE_V1 { init_pin = "00000000", user_pin = <test> }` succeeds → app UI shows the red dev banner. Follow-up: `0x67 DEV_SET_INIT_PIN_V1 { "99999999" }` after a wipe → next provisioning requires `"99999999"`. Same sequence on a PROD-firmware board must yield `SPI_STATUS_CMD_NOT_AVAILABLE` for `0x67`.

## 12. Risks & open items

0. **Single binary, no split** — per user direction, there is no `kayten-host-provisioning` alongside `kayten-host-mobile`. Old concerns about CI coverage of a second host binary go away; the mobile binary is the only one.
1. **CSM / CryIf removal is the single largest refactor.** Every crypto call-site in HSM firmware must be rewritten against `Kayten_Crypto.*`. Risk is hidden `Csm_*` references in RTE-generated glue code that are not obvious in source grep. Mitigation: week-1 HSM inventory scan must produce a signed-off call-site list before any deletion.
2. **Factory line provisioning tool needs rework.** The old `kPkcs11Manager`-based `CMD_INIT_TOKEN` flow is gone. Factory line must switch to a JTAG / NVM-bootloader flow that writes `KAYTEN_NVM_INIT_PIN_BLOCK` + seals it. Tool binary lives outside this workstream (`uHSM-Tools/factory_line_provisioner/`).
3. **Firmware test suite.** HSM unit tests that drove PKCS#11 paths need retirement or migration to the semantic-command equivalent. No back-compat test profile.
4. **Support-tooling scripts.** Any debug tools under `tools/` that speak PKCS#11 must migrate. To audit in Week 1.
5. **Docs archive.** `docs/v2.1/uhsm_host_mobile_gateway_spec_2026-03-27.md`, `docs/external_hsm_integration_specification.md`, and sibling docs move under `docs/archive/2026-04-17-pkcs11-retirement/`.
6. **Field HSM hardware.** Currently-deployed HSMs accept both command surfaces. Once Week 3 cuts over, a host upgrade pushes STD_ON to existing HSMs; firmware must ship `0x60..0x68` handlers first (`0x67` dev-only). Week 1 → Week 3 ordering ensures this.
7. **Server-side DB / proto rollback**: `GetIdentityKeyResponse.revoked_at` is an additive proto3 field (safe on downgrade of *clients*), but partner devices that were already told "your contact's device is revoked" cannot be un-told. Treat revocation fan-out as one-way from a product perspective.
8. **Init-PIN printed on device label** is a physical-security surface. A found/stolen-and-label-readable device is bricked by the first bad PIN sequence but can be reset by the finder. Mitigation recommended (out of scope for this spec): ship the `init_pin` out-of-band to the buyer (email/postcard) and print a checksum on the label, not the PIN itself.

## 13. Follow-ups (out of scope)

- Retire `WRAP_STORAGE_ROOT_KEY_V1 (0x4E)` / `UNWRAP_STORAGE_ROOT_KEY_V1 (0x4F)` in favor of unified `WRAP_KEY_V1` / `UNWRAP_KEY_V1` if SRK semantics change. Not required for PKCS#11 retirement.
- Consolidate `READ_IDENTITY_PUBLIC_V1 (0x47)` into `READ_PUBLIC_KEY_V1 (0x61)` by passing `IDENTITY_PUB (23)`. Kept separate for now to preserve the clean fresh-bring-up path.
- Introduce `BATCH_COMMAND_V1` for multi-op flows (e.g. generate-SPK + sign + publish) if SPI round-trip latency is an issue on field devices.
- `kayten-proto` could host the per-command wire struct definitions (TLV schema) as `.proto`s for language-agnostic parsers. Deferred: host and firmware today speak a hand-rolled packed C layout that matches the proto numeric enums closely enough.
- **Two-phase provisioning (closes §10 #14).** Split `PROVISION_DEVICE_V1 (0x44)` into:
  - **Phase A — `INIT_PROVISION_V1 (0x69)` `{ init_pin }`** → throttle-check → verify init_pin → generate identity → state = `IDENTITY_ESTABLISHED` (new intermediate state between `REQUIRED` and `PROVISIONED`). Plaintext outer SPI (but only exposes `init_pin`, a physical possession factor).
  - **Phase B — `SECURE_EXECUTE_V1`-wrapped `CHANGE_USER_PIN_V1 (0x46)` `{ current = "", new = user_pin }`** → state transitions to `PROVISIONED`. `user_pin` never crosses plaintext.
  Splits the single "provision" action into two atomic HSM commits separated by a `0x55` handshake. **Scope stub written:** [`2026-04-18-two-phase-provisioning-cross-repo-spec.md`](2026-04-18-two-phase-provisioning-cross-repo-spec.md). Not on the critical path for PKCS#11 retirement itself — `0x44` as specified here is secure enough for launch given the narrow attack surface of the one-shot provisioning event. Promote to a full cross-repo workstream if any of the trigger conditions in the stub §5 fire.

---

## Appendix A — Per-repo spec + AI-prompt rollout

Once this master spec is approved, per-repo artefacts will be produced in this order:

1. `2026-04-17-kayten-proto-hsm-keys-constants-spec.md` + AI prompt (the `hsm_keys.v1` package + `MobileCommandId` through `0x68` + `ProvisioningState` + `WipeReason` enums + `GetIdentityKeyResponse.revoked_at` field addition).
2. `2026-04-17-uhsm-hsm-mobile-profile-v2-spec.md` + AI prompt (Kayten_Crypto introduction; **delete Csm / CryIf / PKCS#11**; new `0x60..0x68` handlers (`0x67` dev-only); clean-sheet `PROVISION_DEVICE_V1`; `Kayten_WipeAllKeyMaterial`; RAM wipe-challenge state for `0x68`/`0x65`; factory-fused init-pin NVM block; allow-list update incl. `CHANGE_USER_PIN` inner path; profile flag; boot-path recovery heuristic; tolerance tests for mid-wipe power loss).
3. `2026-04-17-uhsm-host-kmobile-manager-spec.md` + AI prompt (delete `Src/PKCS11/` directory, delete Csm/CryIf host imports, new `kMobileManager` dispatch, all new status codes — §2.8 authoritative remap: `CMD_NOT_AVAILABLE 0xE0`, `DEVICE_WIPED 0xEE`, `INIT_PIN_INVALID 0xE5`, `ALREADY_PROVISIONED 0xE4`, `WIPE_NONCE_INVALID 0xEF`, `INIT_PIN_THROTTLED 0xF5`).
4. `2026-04-17-kayten-app-pkcs11-retirement-spec.md` + AI prompt — the large Android/Dart work. Includes: schema-v2 wrapped-key store + forced re-enrollment; provisioning UI (init-pin + user-pin); PIN-retry countdown; lockout-wipe handler (**outbox-first** `RevokeDevice` per §6a.3 → local wipe → post-wipe re-provisioning); user-initiated wipe: `0x68` + SECURE_EXECUTE `0x65`; hardware-prod `CHANGE_USER_PIN` via inner `0x55` only; purge `HsmCommand.Legacy`, `Pkcs11ObjectResolver`, `kSlot*`.
5. `2026-04-17-kayten-app-v2-pkcs11-retirement-spec.md` + AI prompt (smaller mirror of #4 — Rust-core already key-id-centric; main work is `NativeHsmTransport.kt` + `ExternalHsmClient.swift` + `SimulatedTransport.kt` / SoftHSM adding `0x68`/`0x65`/`0x66` + outbox paths).
6. `2026-04-17-kayten-server-device-wipe-integration-spec.md` + AI prompt (mandatory server work: `DeviceService.RevokeDevice` marks all `prekeys` used, clears inbound queue, surfaces `GetIdentityKeyResponse.revoked_at`; migrations/indexes as needed against **actual** `005_keys.up.sql` schema; optional `kayten_device_wiped_total`; `DeviceRevoked` / `KeyChangeAlert` already in `service.go`; plus NEW `IssueSelfRevokeCapability` + `RevokeDeviceByCapability` handlers + `revocation_capability_jti` migration — §9c).

## Appendix C — Parked follow-up artefacts (not active workstreams)

- [`2026-04-18-two-phase-provisioning-cross-repo-spec.md`](2026-04-18-two-phase-provisioning-cross-repo-spec.md) — **scope stub** closing §10 #14 residual (plaintext `user_pin` on `0x44`). Not scheduled. Promote to full spec + per-repo specs + AI prompts if any trigger condition in its §5 fires.

Each active per-repo spec in Appendix A references this master spec as §0.
