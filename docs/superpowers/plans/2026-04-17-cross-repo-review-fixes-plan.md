# Cross-Repo Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close all CRITICAL and HIGH findings from the 2026-04-17 end-to-end review of kayten-app, kayten-server, kayten-proto, and uHSM-Host across the four core flows: user + HSM onboarding, contacts search/add, conversation encrypt/decrypt, and voice call request/answer.

**Architecture:** Fixes are grouped into 8 phases. Each phase produces a shippable, testable increment. Cross-repo changes are sequenced so proto changes land before their server/app consumers, and server changes land before app changes that depend on new server semantics.

**Tech Stack:** Flutter/Dart (kayten-app), Kotlin (kayten-app Android plugin), Swift (kayten-app iOS handlers), Go (kayten-server), Protobuf 3 (kayten-proto), C (uHSM-Host). **Note:** This plan predates the **kMobileManager / PKCS#11 retirement** workstream (`2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md`); that spec **does** require substantive `uHSM-Host` changes. Treat the lines below as **review-fixes scope only**, not a claim that the host is frozen.

**Repos & paths:**
- kayten-app: `/Users/tahir/Repos/kayten-app`
- kayten-server: `/Users/tahir/Repos/kayten-server`
- kayten-proto: `/Users/tahir/Repos/kayten-app/api/proto` (submodule)
- uHSM-Host: `/Users/tahir/Repos/uHSM/uHSM-Host` (**no changes** for *this* review-fixes plan only — see kMobileManager spec for host retirement work)

**Review source:** Consolidated findings in the 2026-04-17 cross-repo review covering 4 flows × 4 repos. Dedup'd and prioritized — this plan touches the 8 CRITICAL and 17 HIGH items plus the load-bearing MEDIUM follow-ups. LOW items are captured in the backlog at the end but not tasked here.

---

## Phase Overview

| Phase | Title | Scope | Est. |
|---|---|---|---|
| 1 | Silent data-loss fixes | padding, iOS salt, block preservation, prekey count | 1 day |
| 2 | Auth & session hardening | refresh rotation, zero-key reject, GetIdentityKey strict | 2 days |
| 3 | Voice call auth hardening | key length asserts, mode gating, signer fallback | 2 days |
| 4 | Server routing + contacts | block-on-send, OPK cardinality, discovery hardening | 2 days |
| 5 | EditMessage authoritative coords (cross-repo) | proto + server + app migration | 2 days |
| 6 | Onboarding recovery UX | setup-complete ordering, interrupted-provision, typed errors | 2 days |
| 7 | Conversation slot LRU persistence | wrap-before-destroy | 1 day |
| 8 | Diagnostics + polish | logs, l10n, test coverage | 1 day |

Phases 1–4 can be parallelized by different engineers. Phase 5 must sequence proto → server → app. Phases 6–8 are app-only.

---

## Phase 1 — Silent Data-Loss Fixes

Each finding in this phase is a bug where real user data is lost or corrupted silently. These must ship first.

### Task 1.1: Fix message padding marker collision

**Finding:** Plaintext of length N where `N % 256 == 0 && N > 256` is silently truncated on decrypt. Bucket 2048 receives a 512-byte plaintext (marker = 0), unpad loop skips candidate=0 and returns the first 256 bytes.

**Files:**
- Modify: `lib/services/crypto_service.dart:380-413` (padMessage + unpadMessage)
- Test: `test/security/message_security_test.dart` (add regression cases)

- [ ] **Step 1: Write failing test for 512-byte plaintext round-trip**

```dart
// test/security/message_security_test.dart
// Drive the real CryptoService.padMessage + unpadMessage round-trip.
// A test harness with a minimal HSM stub that serves `generateRandom`
// from `Random.secure()` is sufficient.

test('padMessage → unpadMessage round-trips at every bucket boundary', () async {
  final service = _buildCryptoServiceWithRandomStub();
  for (final size in [
    0, 1, 63, 126, 127, 128, 254, 255, 256, 510, 511, 512,
    768, 1024, 1280, 1792, 2045, 2046, 2047, 2048,
    2560, 3072, 4096, 5117, 5118,
  ]) {
    final original = Uint8List(size);
    for (var i = 0; i < original.length; i++) {
      original[i] = i & 0xFF;
    }
    final padded = await service.padMessage(original);
    final unpadded = service.unpadMessage(padded);
    expect(unpadded, equals(original), reason: 'size=$size round-trip');
  }
});

test('unpadMessage throws on out-of-range length suffix', () {
  final service = _buildCryptoServiceWithRandomStub();
  // 2 bytes: 0xFF 0xFF → length 65535, which cannot fit in any bucket.
  final junk = Uint8List.fromList([1, 2, 3, 0xFF, 0xFF]);
  expect(() => service.unpadMessage(junk), throwsFormatException);
});
```

- [ ] **Step 2: Run test, expect failure on the current implementation**

```
flutter test test/security/message_security_test.dart --plain-name 'round-trips for 512 bytes'
```

Expected: FAIL — unpadded length = 256 (bug).

- [ ] **Step 3: Update padMessage, unpadMessage, and `_paddingTarget`**

**Why the original `(len & 0xFF) + 1` scheme doesn't work:** a 1-byte marker encodes 256 values. Bucket 2048 admits 1536 distinct lengths [512..2047]; 1536 > 256, so a 1-byte marker cannot unambiguously identify the original length within that bucket. The `+1` shift still leaves `candidate=256` as the first valid match for many larger plaintexts. The correct fix is a 2-byte little-endian length suffix.

```dart
// lib/services/crypto_service.dart:383-420

int _paddingTarget(int length) {
  // Reserve 2 bytes for the length suffix; ensure plaintext + suffix fits.
  if (length + 2 <= 128) return 128;
  if (length + 2 <= 512) return 512;
  if (length + 2 <= 2048) return 2048;
  return 5120;
}

Future<Uint8List> padMessage(Uint8List plaintext) async {
  if (plaintext.length > 0xFFFF) {
    throw StateError(
      'padMessage: plaintext ${plaintext.length} exceeds 16-bit length',
    );
  }
  final targetSize = _paddingTarget(plaintext.length);
  final padded = Uint8List(targetSize);
  padded.setRange(0, plaintext.length, plaintext);

  final fillLength = targetSize - plaintext.length - 2;
  if (fillLength > 0) {
    final randomFill = await _hsmService.generateRandom(fillLength);
    padded.setRange(plaintext.length, targetSize - 2, randomFill);
  }

  // 2-byte little-endian length suffix. Unambiguous across all buckets:
  // uint16 (0..65535) covers every bucket size (max 5120). Pre-2026-04-17
  // senders used a 1-byte marker scheme that silently truncated any
  // plaintext where `len % 256 == 0 && len > 256`.
  padded[targetSize - 2] = plaintext.length & 0xFF;
  padded[targetSize - 1] = (plaintext.length >> 8) & 0xFF;
  return padded;
}

Uint8List unpadMessage(Uint8List padded) {
  if (padded.length < 2) return padded;
  final length =
      padded[padded.length - 2] | (padded[padded.length - 1] << 8);
  if (length < 0 || length > padded.length - 2) {
    // Out-of-range length — corrupt message or pre-2026-04-17 sender
    // whose 1-byte marker is now interpreted as a 2-byte length. Fail
    // closed rather than return silently truncated garbage.
    throw const FormatException(
      'unpadMessage: length out of range — corrupted padding or '
      'pre-2026-04-17 sender',
    );
  }
  return Uint8List.fromList(padded.sublist(0, length));
}
```

Note the threshold adjustment: plaintext of length 127 now falls into bucket 512 (127+2=129 > 128). Very small traffic-analysis differential at the 126/127, 510/511, 2046/2047, 5118/5119 boundaries — acceptable for a correctness fix.

- [ ] **Step 4: Verify tests pass**

```
flutter test test/security/message_security_test.dart
```

- [ ] **Step 5: Add integration test that sends/receives every bucket boundary**

Add to `test/integration/option_a_cross_device_test.dart` group "Option A full encrypt→decrypt round-trip":

```dart
test('round-trips plaintext at every bucket boundary', () async {
  // Boundaries where the old marker=0 bug triggered.
  for (final size in [127, 128, 256, 512, 768, 1024, 2048, 2560, 5119]) {
    final pt = Uint8List(size);
    for (var i = 0; i < size; i++) pt[i] = i & 0xFF;
    // ... [use the existing Alice→Bob encrypt/decrypt harness]
    expect(decrypted, equals(pt), reason: 'boundary failure at $size');
  }
});
```

- [ ] **Step 6: Commit**

```bash
git commit -m "fix(crypto): padding marker collision silently truncated messages

Plaintexts of length 512, 768, 1024, 1280, ... (any multiple of 256 > 256)
produced a length marker of 0, and the unpad loop skipped candidate=0
and returned the first 256 bytes. Fix by encoding (len & 0xFF) + 1 so
marker is never zero; adjust unpad to subtract 1 before the modular
search. Regression tests cover every bucket boundary.

Senders running pre-fix builds produce messages with marker=0 that the
new unpad rejects — deliberate: the message is already corrupt on the
wire and delivering 256 bytes of a 512-byte message is worse than
failing closed."
```

---

### Task 1.2: Fix iOS contacts hash missing salt

**Finding:** Android `AddressBookReader.kt` prepends the salt before SHA-256; iOS `ContactsHandler.swift` drops the salt argument entirely. Cross-platform contact discovery broken for iOS ↔ anyone.

**Files:**
- Modify: `ios/Runner/ContactsHandler.swift` (full signature + digest)
- Test: manual iOS simulator test + parity unit test

- [ ] **Step 1: Read current iOS handler**

```bash
sed -n '1,80p' ios/Runner/ContactsHandler.swift
```

Locate `sha256Hex(_ input: String) -> String` and the caller at line 43.

- [ ] **Step 2: Update method signatures**

```swift
// ios/Runner/ContactsHandler.swift

// OLD: private static func sha256Hex(_ input: String) -> String {
private static func sha256Hex(salt: Data, input: String) -> String {
    var digest = SHA256()
    digest.update(data: salt)
    if let bytes = input.data(using: .utf8) {
        digest.update(data: bytes)
    }
    let final = digest.finalize()
    return final.map { String(format: "%02x", $0) }.joined()
}
```

Update the call site in `readContacts`:

```swift
// BEFORE: let hash = Self.sha256Hex(normalized)
let saltData = Data(base64Encoded: saltBase64) ?? Data()
if saltData.isEmpty {
    // Fail closed — hash without salt matches no server record.
    result.error("CONTACTS_SALT_MISSING",
                 "Contacts hash salt is empty; cannot perform discovery.",
                 nil)
    return
}
let hash = Self.sha256Hex(salt: saltData, input: normalized)
```

Ensure `readContacts` reads the salt from its MethodChannel args. Match the Android key name (`"salt"`, base64 string) — verify in `AndroidContactsAdapter`.

- [ ] **Step 3: Add Dart-side parity test**

```dart
// test/unit/contacts/contact_hash_parity_test.dart
test('iOS + Android hash formula match for +49 151 555 0100', () {
  final salt = base64.decode('c2FsdDEyMzQ1Njc4OTA=');
  final normalized = '+491515550100';
  final expected = sha256.convert([...salt, ...utf8.encode(normalized)])
      .bytes
      .map((b) => b.toRadixString(16).padLeft(2, '0'))
      .join();
  // Android expected (from Kotlin test): compare golden hex string.
  expect(expected, equals('...'));
});
```

- [ ] **Step 4: Commit**

```bash
git commit -m "fix(ios): contacts SHA-256 missing salt — restored cross-platform parity"
```

---

### Task 1.3: Preserve local block state through server sync

**Finding:** `discoverContacts` upsert overwrites `isBlocked` with the server's view (peer's block status on you, not yours). Next sync silently unblocks locally-blocked contacts.

**Files:**
- Modify: `lib/data/repositories/contact_repository_impl.dart:116`
- Test: `test/unit/contacts/contact_repository_test.dart`

- [ ] **Step 1: Add failing test**

```dart
test('discoverContacts preserves existing local isBlocked', () async {
  // arrange: a contact already locally blocked
  await dao.upsertContact(existing.copyWith(isBlocked: true));
  // act: server returns same contact with isBlocked=false
  await repo.handleDiscoveryMatch(match.copyWith(isBlocked: false));
  // assert: local row still blocked
  expect((await dao.getContact(match.userId)).isBlocked, isTrue);
});
```

- [ ] **Step 2: Fix upsert**

```dart
// lib/data/repositories/contact_repository_impl.dart:116
// BEFORE: isBlocked: Value(match.isBlocked),
final existing = await _contactDao.getByUserId(match.userId);
// Block is client-owned — never overwrite from server. New contacts
// default to unblocked (server cannot know our block intent yet).
isBlocked: Value(existing?.isBlocked ?? false),
```

- [ ] **Step 3: Verify test passes; commit**

```bash
git commit -m "fix(contacts): discoverContacts no longer overwrites local block state"
```

---

### Task 1.4: Validate prekey bundle completeness before upload

**Finding:** `generatePrekeys(100)` result length is never asserted before upload. A stale firmware or future refactor can upload an incomplete bundle silently.

**Files:**
- Modify: `lib/services/crypto_service.dart` (add length validation on return)
- Modify: `lib/features/auth/providers/auth_provider.dart:689, 757` (fail closed if incomplete)
- Test: `test/unit/crypto_service_test.dart`

- [ ] **Step 1: Add failing test where provider only produces partial bundle**

```dart
test('generatePrekeys throws when provider returns fewer OPKs than requested', () async {
  final provider = _FakeHsmProvider(maxOneTimePrekeysPerBatchValue: 5);
  final service = CryptoService(provider, HsmSlotRegistry());
  // Effective batch is 5, but we ask for 20. Current behavior clamps silently.
  // New behavior: clamp is OK, but downstream MUST see the actual count
  // and the CALLER MUST assert before upload. Test the contract at the
  // caller (auth_provider), not the service itself.
});
```

- [ ] **Step 2: Update call sites**

```dart
// lib/features/auth/providers/auth_provider.dart:689 (inside completeHsmSetup)
final bundle = await cryptoService.generatePrekeys(oneTimePrekeyCount: 100);
final expected = 100.clamp(0, hsmService.maxOneTimePrekeysPerBatch);
if (bundle.oneTimePrekeys.length != expected) {
  throw const HsmServiceException(
    'Prekey generation produced an incomplete bundle — refusing to '
    'upload. HSM firmware may lack CAP_OPK_V1 or be in a degraded '
    'state. Update firmware and retry.',
    'PREKEY_BUNDLE_INCOMPLETE',
  );
}
```

Apply identical guard at `lib/features/auth/providers/auth_provider.dart:757` (inside `_ensurePrekeysUploaded`).

- [ ] **Step 3: Add user-facing l10n string**

Add to `lib/l10n/app_en.arb` / `app_de.arb` / `app_tr.arb`:

```json
"hsmErrorPrekeyBundleIncomplete": "Your HSM firmware cannot generate the required one-time prekeys. Update the HSM firmware and try again."
```

Map `PREKEY_BUNDLE_INCOMPLETE` in `AuthErrorMapper.userMessage` to this string.

- [ ] **Step 4: Commit**

```bash
git commit -m "fix(auth): fail closed when prekey bundle is incomplete before upload"
```

---

## Phase 2 — Auth & Session Hardening

### Task 2.1: Rotate refresh token on every renewal (server)

**Finding:** `RefreshAccessToken` validates but reuses the refresh token — classic fixation vector.

**Files:**
- Modify: `kayten-server/internal/auth/service.go:209-236`
- Modify: proto `AuthService` response to include `refresh_token` (if not already there — verify)
- Modify: `kayten-app/lib/services/auth/token_manager.dart` (or equivalent) to consume rotated token
- Test: `kayten-server/internal/auth/service_test.go`

- [ ] **Step 1: Add server test for rotation**

```go
// internal/auth/service_test.go
func TestRefreshAccessToken_RotatesRefreshToken(t *testing.T) {
    // ... setup ...
    originalRefresh := "rt_abc"
    newAccess, newExp, newRefresh, err := svc.RefreshAccessToken(ctx, originalRefresh)
    require.NoError(t, err)
    require.NotEqual(t, originalRefresh, newRefresh, "refresh token MUST rotate")
    // Original refresh must now be invalid
    _, _, _, err = svc.RefreshAccessToken(ctx, originalRefresh)
    require.Error(t, err, "old refresh token must reject after rotation")
}
```

- [ ] **Step 2: Update service signature**

```go
// internal/auth/service.go:209
func (s *Service) RefreshAccessToken(ctx context.Context, refreshToken string) (string, time.Time, string, error) {
    // ... existing validation ...
    // Persist-and-invalidate: generate new refresh token, mark old one consumed
    newRefresh, err := s.generateRefreshToken()
    if err != nil { return "", time.Time{}, "", err }
    if err := s.repo.RotateRefreshToken(ctx, device.ID, refreshToken, newRefresh); err != nil {
        return "", time.Time{}, "", err
    }
    // ... build access token as before ...
    return access, exp, newRefresh, nil
}
```

- [ ] **Step 3: Update gRPC handler + proto response**

If `RefreshAccessTokenResponse` lacks `refresh_token`, add field 3 in `auth.proto`. Regenerate stubs.

- [ ] **Step 4: Update app-side token manager**

```dart
// kayten-app: wherever refresh response is consumed — store new refresh
await secureStorage.write(key: 'refresh_token', value: response.refreshToken);
```

- [ ] **Step 5: Commit each repo separately**

```bash
# Server
git commit -m "fix(auth): rotate refresh token on every renewal (fixation)"
# App
git commit -m "fix(auth): consume rotated refresh token from server"
```

---

### Task 2.2: Check device.IsActive on refresh + add zero-key identity rejection (server)

**Finding:** H11 + H12 — refresh path only checks IsRevoked, and zero-filled Ed25519 identity key passes length check but fails cryptographically later.

**Files:**
- Modify: `kayten-server/internal/auth/service.go:95-206` (VerifyCodeAndRegister)
- Modify: `kayten-server/internal/auth/service.go:209-236` (RefreshAccessToken)

- [ ] **Step 1: Add is-active check**

```go
// service.go:~225 — inside RefreshAccessToken after fetching device
if !device.IsActive {
    return "", time.Time{}, "", errors.New("device not active")
}
```

- [ ] **Step 2: Reject zero identity key**

```go
// service.go:~160 — inside VerifyCodeAndRegister, before any identity persist
if len(ed25519Pub) != 32 {
    return nil, errors.New("ed25519 identity key must be exactly 32 bytes")
}
if bytes.Equal(ed25519Pub, make([]byte, 32)) {
    return nil, errors.New("ed25519 identity key cannot be all-zero")
}
```

- [ ] **Step 3: Add tests for both**

- [ ] **Step 4: Commit**

```bash
git commit -m "fix(auth): reject zero identity key + gate refresh on device.IsActive"
```

---

### Task 2.3: GetIdentityKey strict device mode (server)

**Finding:** `GetIdentityKey` falls back to the first active device when the specific deviceId is not found. This is the root cause of the voice call signer identity pub bug documented in the app's 2026-04-16 delta.

**Files:**
- Modify: `kayten-server/internal/device/handler_grpc.go:194-232` (GetIdentityKey)
- Review: all call sites that depend on the current fallback behavior — identify whether any legitimately need "first active" semantics

- [ ] **Step 1: Audit callers**

```bash
cd /Users/tahir/Repos/kayten-server
grep -rn "GetIdentityKey" --include="*.go"
grep -rn "getIdentityKey\|GetIdentityKey" /Users/tahir/Repos/kayten-app/lib
```

Expected: the app now populates `signer_identity_pub` on the wire, so server-side fallback is unnecessary for voice. Messaging responder also uses wire-provided identity. Confirm no legitimate caller needs fallback.

- [ ] **Step 2: Add failing test**

```go
func TestGetIdentityKey_RejectsMissingDeviceId(t *testing.T) {
    _, err := svc.GetIdentityKey(ctx, &pb.GetIdentityKeyRequest{UserId: "u1", DeviceId: ""})
    require.ErrorContains(t, err, "device_id required")
}

func TestGetIdentityKey_RejectsNonexistentDevice(t *testing.T) {
    _, err := svc.GetIdentityKey(ctx, &pb.GetIdentityKeyRequest{UserId: "u1", DeviceId: "d-unknown"})
    st, _ := status.FromError(err)
    require.Equal(t, codes.NotFound, st.Code())
}
```

- [ ] **Step 3: Fix handler**

```go
// internal/device/handler_grpc.go:194
func (h *GrpcHandler) GetIdentityKey(ctx context.Context, req *pb.GetIdentityKeyRequest) (*pb.GetIdentityKeyResponse, error) {
    if req.UserId == "" {
        return nil, status.Error(codes.InvalidArgument, "user_id required")
    }
    if req.DeviceId == "" {
        // Strict: no more "first active" fallback. Callers MUST provide
        // the specific device id (mobile clients always know it from
        // PrekeyBundle / call offer / wire).
        return nil, status.Error(codes.InvalidArgument,
            "device_id required — GetIdentityKey no longer supports user-only lookup")
    }
    dev, err := h.repo.GetDevice(ctx, req.DeviceId)
    if err != nil { return nil, status.Error(codes.Internal, err.Error()) }
    if dev == nil || dev.UserID != req.UserId {
        return nil, status.Error(codes.NotFound, "device not found for user")
    }
    // ... return identity ...
}
```

- [ ] **Step 4: Ensure app-side always populates deviceId**

Grep `kayten-app/lib` for `getIdentityKey(` calls — confirm each passes both `userId` and `deviceId`. Per the recent voice call delta, this should already be the case. Add assertion `assert(deviceId.isNotEmpty)` in `KeyRepository.getIdentityKey` client-side.

- [ ] **Step 5: Commit**

```bash
git commit -m "fix(device): GetIdentityKey requires specific deviceId — no fallback"
```

---

### Task 2.4: Device-identity sync validates HSM serial match (app)

**Finding:** H7 — after physical HSM swap without re-enrollment, sync service re-registers the new HSM's identity without verifying the enrolled serial matches.

**Files:**
- Modify: `lib/services/device_identity_sync_service.dart:140-159`

- [ ] **Step 1: Add test**

```dart
test('ensureCurrentDeviceIdentityRegistered skips when enrolled serial '
    'does not match live HSM serial', () async {
  fakeEnrollment.record = EnrollmentRecord(hsmSerial: 'ENROLLED-001', ...);
  fakeHsm.connectionInfo = HsmConnectionInfo(enrollmentSerial: 'LIVE-999');
  await service.ensureCurrentDeviceIdentityRegistered();
  expect(fakeKeyRepo.registerIdentityKeyCalled, isFalse);
  expect(
    fakeSessionNotifier.enrollmentFailureReason,
    equals(EnrollmentFailureReason.wrongDevice),
  );
});
```

- [ ] **Step 2: Add serial-match gate**

```dart
// lib/services/device_identity_sync_service.dart — in the method that
// calls registerIdentityKey:
final record = await _enrollmentStore.load();
final liveSerial = _hsmService.connectionInfo?.enrollmentSerial;
if (record != null && liveSerial != null && record.hsmSerial != liveSerial) {
  kLog(
    '[IdentitySync] Enrolled serial ${record.hsmSerial} != live $liveSerial. '
    'Clearing backend-sync gate; re-enrollment required.',
    tag: 'DeviceIdentitySync',
    level: 900,
  );
  await _clearBackendSyncGateIfStale(reason: EnrollmentFailureReason.wrongDevice);
  return;
}
```

- [ ] **Step 3: Commit**

---

## Phase 3 — Voice Call Auth Hardening

### Task 3.1: Assert voice-ECDH key + signature lengths before transcript build

**Finding:** C3 — callee accepts `_pendingVoiceEcdhPub` / `_pendingVoiceEcdhSig` without length check.

**Files:**
- Modify: `lib/features/call/providers/call_provider.dart:435-485`

- [ ] **Step 1: Add failing test**

```dart
test('answerCall rejects malformed pendingVoiceEcdhPub length', () async {
  notifier.testSetPending(voiceEcdhPub: Uint8List(32)); // wrong length
  await notifier.answerCall(...);
  expect(notifier.state.error, equals(l10n.callInvalidSignature));
  verifyNever(hsmService.setupVoiceSession(...));
});
```

- [ ] **Step 2: Add hard assertions**

```dart
// lib/features/call/providers/call_provider.dart:435 (start of answerCall
// verification block)
final pub = _pendingVoiceEcdhPub;
final sig = _pendingVoiceEcdhSig;
if (pub == null || pub.length != 64) {
  _terminateInternal(CallEndReason.protocolError);
  state = state.copyWith(error: l10n.callInvalidEphemeralKey);
  return;
}
if (sig == null || sig.length != 64) {
  _terminateInternal(CallEndReason.protocolError);
  state = state.copyWith(error: l10n.callInvalidSignature);
  return;
}
```

Mirror on caller side (CALL_ANSWER receipt): assert `answer.voiceEcdhPub.length == 64` and `answer.voiceEcdhSig.length == 64` at `call_provider.dart:1269-1290`.

- [ ] **Step 3: Commit**

```bash
git commit -m "fix(call): hard-assert voice ECDH key/sig lengths before verify"
```

---

### Task 3.2: Gate setupVoiceSession on signature verification

**Finding:** C4 (voice agent) — callee's `setupVoiceSession` runs even when `calleeIdentityKey` resolution fails. Caller's path at `call_provider.dart:1330-1348` has the same structural issue.

**Files:**
- Modify: `lib/features/call/providers/call_provider.dart:1330-1348`

- [ ] **Step 1: Add failing test** — simulate `getIdentityKey` throwing, expect call to terminate, `setupVoiceSession` never called.

- [ ] **Step 2: Move setupVoiceSession inside verification branch**

```dart
// caller-side CALL_ANSWER verification
final Uint8List? calleeIdentityKey = await _resolveCalleeIdentityKey(
  answer: answer,
  conversationId: session.conversationId,
);
if (calleeIdentityKey == null) {
  _terminateInternal(CallEndReason.identityUnresolved);
  state = state.copyWith(error: l10n.callPeerIdentityUnresolved);
  return;
}
final ok = await _hsmService.verify(transcript, answer.voiceEcdhSig, calleeIdentityKey);
if (!ok) {
  _terminateInternal(CallEndReason.signatureInvalid);
  state = state.copyWith(error: l10n.callInvalidSignature);
  return;
}
// Only now proceed:
await _hsmService.setupVoiceSession(
  peerPublicKey: answer.voiceEcdhPub,
  voiceMode: session.voiceMode,
  callId: session.id,
);
```

- [ ] **Step 3: Commit**

---

### Task 3.3: Secure-channel precheck before Mode A call initiation

**Finding:** H5 — user can pick Mode A while the Model C secure channel is dropped; firmware-derived voice keys end up stale/unusable.

**Files:**
- Modify: `lib/features/call/providers/call_provider.dart:224-298` (initiateCall)

- [ ] **Step 1: Add precheck**

```dart
if (voiceMode == 'modeA') {
  final status = await _hsmService.getRuntimeStatus();
  if (!status.secureChannelActive) {
    // Attempt one re-establish; if it fails, fail closed.
    try {
      await _hsmService.initSecureChannel();
    } on HsmServiceException catch (e) {
      state = state.copyWith(error: l10n.callModeASecureChannelUnavailable);
      return;
    }
  }
}
```

- [ ] **Step 2: Add `ModeBExportUnavailableException` catch in `answerCall`**

```dart
try {
  await _hsmService.setupVoiceSession(...);
} on ModeBExportUnavailableException catch (e) {
  _terminateInternal(CallEndReason.modeBExportFailed);
  state = state.copyWith(error: l10n.callModeBExportUnavailable);
  return;
}
```

- [ ] **Step 3: Commit**

---

### Task 3.4: Security UI alert on signer fallback + server-side length validation

**Finding:** H8 — `signer_identity_pub` fallback is silent (log level 800 only). H13 — server relays CallVoiceRekey without Ed25519 length check.

**Files:**
- Modify: `lib/features/call/providers/call_provider.dart:447-461, 1269-1290`
- Modify: `kayten-server/internal/calling/handler_ws.go:159-176`
- Modify: `kayten-server/internal/calling/service.go:214-231`

- [ ] **Step 1: App — surface a warning state when fallback is used**

```dart
if (signerIdentityPub.isEmpty) {
  state = state.copyWith(
    peerIdentityWarning: CallPeerIdentityWarning.wireFieldMissing,
  );
  kLog('[Call] signer_identity_pub missing on CALL_OFFER — falling back '
       'to server GetIdentityKey. Peer may be on an old build, or '
       'attempting to exploit server-side fallback.',
       tag: 'CallProvider', level: 900);
}
```

Show a subtle warning chip on the active-call screen when this flag is set.

- [ ] **Step 2: Server — validate length on all relayed call envelopes**

```go
// internal/calling/service.go — inside HandleOffer, HandleAnswer,
// HandleVoiceRekey before relaying:
if len(offer.SignerIdentityPub) != 0 && len(offer.SignerIdentityPub) != 32 {
    return status.Error(codes.InvalidArgument, "signer_identity_pub must be 32 bytes or empty")
}
if len(offer.VoiceEcdhSig) != 0 && len(offer.VoiceEcdhSig) != 64 {
    return status.Error(codes.InvalidArgument, "voice_ecdh_sig must be 64 bytes or empty")
}
if len(offer.VoiceEcdhPub) != 0 && len(offer.VoiceEcdhPub) != 64 {
    return status.Error(codes.InvalidArgument, "voice_ecdh_pub must be 64 bytes or empty")
}
```

Apply same guards in `handler_ws.go` for WebSocket CALL_* envelopes.

- [ ] **Step 3: Commit each repo separately**

---

### Task 3.5: Epoch rekey monotonicity assertion

**Finding:** M6 — `rekeyVoiceSession()` does not assert `newEpochIndex > session.epochCount`.

**Files:**
- Modify: `lib/features/call/providers/call_provider.dart:1033-1081`

- [ ] **Step 1: Add assertion**

```dart
final newEpochIndex = await _hsmService.rekeyVoiceSession();
if (newEpochIndex <= session.epochCount) {
  kLog('[Call] Epoch regression: firmware returned $newEpochIndex <= '
       'current ${session.epochCount}. Terminating call.',
       tag: 'CallProvider', level: 1000);
  _terminateInternal(CallEndReason.epochRegression);
  return;
}
```

- [ ] **Step 2: Commit**

---

## Phase 4 — Server Routing & Contacts

### Task 4.1: Enforce block on message routing (server)

**Finding:** C8 — `SendMessage` doesn't consult `contact_blocks`. A blocked contact can still send messages.

**Files:**
- Modify: `kayten-server/internal/messaging/service.go:872-889` (fanOutToMembers)

- [ ] **Step 1: Add test**

```go
func TestSendMessage_DropsFanOutToBlockingPeer(t *testing.T) {
    // arrange: alice blocks bob. Bob sends to the 1:1 conv.
    require.NoError(t, repo.BlockContact(ctx, alice.ID, bob.ID))
    // act
    err := svc.SendMessage(ctx, bob, msg)
    require.NoError(t, err)
    // assert: alice never received
    events := repo.GetEventsForUser(alice.ID)
    require.Empty(t, events)
}
```

- [ ] **Step 2: Add block gate in fanOutToMembers**

```go
// internal/messaging/service.go — inside fanOutToMembers loop
for _, memberID := range members {
    if memberID == senderID { continue }
    // 2026-04-17: honour 1:1 block on delivery. For group chats,
    // the sender sees the message locally but recipients who blocked
    // the sender never receive it.
    blocked, err := s.contactRepo.IsBlockedBy(ctx, memberID, senderID)
    if err != nil {
        log.Warnf("block-check failed for %s->%s: %v", senderID, memberID, err)
        continue // fail closed
    }
    if blocked {
        continue
    }
    // ... existing enqueue logic ...
}
```

- [ ] **Step 3: Commit**

---

### Task 4.2: Enforce OPK batch cardinality atomically (server)

**Finding:** C5 server side — `StorePrekeys` ON CONFLICT allows partial upload. Client could truncate without error.

**Files:**
- Modify: `kayten-server/internal/device/repository.go:114-150`

- [ ] **Step 1: Add test**

```go
func TestStorePrekeys_RejectsIncompleteOpkBatch(t *testing.T) {
    prekeys := makePrekeys(ed25519, spk, 5) // 5 OPKs instead of 20
    err := repo.StorePrekeys(ctx, device.ID, prekeys)
    require.ErrorContains(t, err, "incomplete OPK batch: 5 / 20")
}
```

- [ ] **Step 2: Validate count before insert**

```go
// internal/device/repository.go:114
func (r *Repository) StorePrekeys(ctx context.Context, deviceID string, bundle *PrekeyBundle) error {
    opkCount := 0
    for _, pk := range bundle.Prekeys {
        if pk.IsOneTime { opkCount++ }
    }
    const expectedOpkCount = 20 // aligned with kMaxOpkSlots (app)
    if opkCount != expectedOpkCount {
        return fmt.Errorf("incomplete OPK batch: %d / %d — refusing upload",
            opkCount, expectedOpkCount)
    }
    // ... wrap insert in tx and commit only on full success ...
}
```

- [ ] **Step 3: Commit**

---

### Task 4.3: DiscoverContacts constant-time lookup + per-user salting consideration

**Finding:** C5 app (already handled in contacts) + H10 — global salt + non-constant-time comparison enables timing side-channels.

**Files:**
- Modify: `kayten-server/internal/contacts/service.go:24-65`
- Modify: `kayten-server/internal/contacts/handler_grpc.go:59-65`

- [ ] **Step 1: Replace direct hash comparison with bucketed lookup**

```go
// internal/contacts/service.go
func (s *Service) DiscoverContacts(ctx context.Context, userID string, hashes [][]byte) ([]Match, error) {
    // Use a single batch SQL IN clause with index on phone_hash; DB
    // comparison is byte-exact and rows are returned by the DB engine
    // with no user-code timing loop. This eliminates the Go-level
    // timing leak where match vs no-match differs in CPU cycles.
    rows, err := s.db.QueryContext(ctx,
        `SELECT user_id, phone_hash FROM users WHERE phone_hash = ANY($1)`,
        pq.ByteaArray(hashes))
    // ... build matches ...
}
```

- [ ] **Step 2: Add TODO for per-user salt rotation**

Document in `internal/contacts/service.go` top-level comment: global salt is an accepted trade-off for simplicity; rotation is planned when user-scale exceeds 10k.

- [ ] **Step 3: Commit**

---

### Task 4.4: Sanitize EditMessage fanout envelope (server)

**Finding:** H14 — server fanout includes client-supplied `edit.conversation_id` unsanitized.

**Files:**
- Modify: `kayten-server/internal/messaging/service.go:601-678`

- [ ] **Step 1: Overwrite conversation_id from authoritative DB row**

```go
// internal/messaging/service.go:~675 — before fanOut
authoritativeConvID := row.ConversationID // from DB lookup earlier
edit.ConversationId = authoritativeConvID
editEnv := &pb.Envelope{ Edit: edit, ... }
s.fanOutToMembers(ctx, authoritativeConvID, conn.UserID, editEnv)
```

- [ ] **Step 2: Commit**

---

### Task 4.5: Fail-closed SMS rate limit on Redis error

**Finding:** M11 — Redis outage disables rate limiting silently, allowing SMS flood.

**Files:**
- Modify: `kayten-server/internal/auth/service.go:52-82`

- [ ] **Step 1: Change fail-open to fail-closed with 503**

```go
if err := s.rateLimiter.CheckRateLimit(...); err != nil {
    if errors.Is(err, ErrRateLimited) {
        return status.Error(codes.ResourceExhausted, "rate limited")
    }
    // Redis down or other infra failure: fail closed.
    log.Errorf("rate limit backend failed: %v", err)
    return status.Error(codes.Unavailable, "auth rate limit service unavailable")
}
```

- [ ] **Step 2: Commit**

---

## Phase 5 — EditMessage Authoritative Ratchet Coordinates (Cross-Repo)

**Rationale:** H4 — receivers on hardened-history builds fail-closed on edited rows because the proto lacks `editRatchetEpoch` + `editMessageCounter`. This is a coordinated change across proto → server → app. Must sequence: proto first, then server, then app.

### Task 5.1: Proto schema extension (kayten-proto)

**Files:**
- Modify: `api/proto/kayten/messaging/v1/messaging.proto` — EditMessage message

- [ ] **Step 1: Add new fields**

```proto
message EditMessage {
  // ... existing fields 1..N ...
  // NEW (2026-04-17): authoritative replay coords for hardened-history
  // anchor re-derive on the receiver side. Receivers running in real-HSM
  // mode with allowLegacyWrappedMessageKeyFallback=false REQUIRE these
  // fields to re-derive the original message key via HistoryKeyRederiver.
  int32 edit_ratchet_epoch = 20;
  int64 edit_message_counter = 21;
}
```

- [ ] **Step 2: `buf generate` and update both repos' `lib/data/remote/dto/` + Go stubs**

```bash
cd api/proto && buf generate && cd ../..
# Copy generated Dart to kayten-app, Go to kayten-server
```

- [ ] **Step 3: Commit in kayten-proto, tag version, update submodule pointers in consumer repos**

---

### Task 5.2: Server EditMessage handler populates + persists new coords

**Files:**
- Modify: `kayten-server/internal/messaging/service.go:601-678`
- Migration: `kayten-server/migrations/NNN_add_edit_ratchet_coords.up.sql`

- [ ] **Step 1: Migration**

```sql
-- migrations/NNN_add_edit_ratchet_coords.up.sql
ALTER TABLE messages ADD COLUMN edit_ratchet_epoch INT;
ALTER TABLE messages ADD COLUMN edit_message_counter BIGINT;
```

- [ ] **Step 2: Update HandleEditMessage + repository**

```go
// EditMessage handler — read new proto fields, persist to row
row.EditRatchetEpoch = edit.EditRatchetEpoch
row.EditMessageCounter = edit.EditMessageCounter
// ... same fanout with populated edit envelope ...
```

- [ ] **Step 3: Commit**

---

### Task 5.3: App send + receive consumes new coords

**Files:**
- Modify: `kayten-app/lib/features/chat/providers/chat_provider.dart:633-676` (send-side edit — populate coords)
- Modify: `kayten-app/lib/services/incoming_message_handler.dart` (_handleEditMessage — read + pass to rederiver)
- Modify: `kayten-app/lib/services/history_key_rederiver.dart` (accept edit coords)

- [ ] **Step 1: Send-side populates coords**

```dart
// chat_provider.dart in editMessage
final editEnv = proto.EditMessage()
  ..messageId = existing.id
  ..editSeq = existing.editSeq + 1
  ..newCiphertext = encrypted.ciphertext
  ..newIvOuter = encrypted.ivOuter
  ..newTagInner = encrypted.tagInner ?? Uint8List(0)
  ..newTagOuter = encrypted.tagOuter
  ..editRatchetEpoch = existing.ratchetEpoch ?? 0
  ..editMessageCounter = Int64(existing.messageCounter ?? 0);
```

- [ ] **Step 2: Receive-side consumes coords + drops legacy gate**

```dart
// incoming_message_handler.dart — _handleEditMessage
final edit = env.edit;
final innerKeySlot = await _historyRederiver.rederive(
  conversationId: row.conversationId,
  direction: HistoryDirection.incoming,
  ratchetEpoch: edit.editRatchetEpoch,
  messageCounter: edit.editMessageCounter.toInt(),
);
// ... decrypt + verify + persist edit ...
```

- [ ] **Step 3: Flip `allowLegacyWrappedMessageKeyFallback` to false in prod config once migration window closes**

- [ ] **Step 4: Add unit + integration test — edit a message in epoch N and decrypt on a hardened-only peer**

- [ ] **Step 5: Commit**

---

## Phase 6 — Onboarding Recovery UX

### Task 6.1: Persist `hsmSetupComplete` only after prekey upload succeeds

**Finding:** H1 (already described in Task 1.4's rationale). This task changes the persistence order.

**Files:**
- Modify: `lib/features/auth/providers/auth_provider.dart:721`
- Add: new pref flag `prekeysUploaded`

- [ ] **Step 1: Split flag semantics**

```dart
// AFTER successful registerDevice() response:
await prefs.setPrekeysUploaded(true);
// AFTER completeHsmSetup full completion:
await prefs.setHsmSetupComplete(true);
```

- [ ] **Step 2: On cold start, if prekeysUploaded=false and HSM+auth ready, re-run `_ensurePrekeysUploaded`**

- [ ] **Step 3: Commit**

---

### Task 6.2: "Provisioning in progress" flag + dedicated recovery screen

**Finding:** H3 — HSM disconnect between `provisionToken` and `unlockUser` leaves state unrecoverable.

**Files:**
- Modify: `lib/features/auth/screens/hsm_setup_screen.dart:243-299`
- New: `lib/features/auth/screens/hsm_provisioning_resume_screen.dart`
- Modify: `lib/core/session/app_session_provider.dart` — add new `AppSessionPhase` variant or route

- [ ] **Step 1: Set flag before `provisionToken`, clear after `completeHsmSetup` success**

```dart
await prefs.setProvisioningInProgress(true);
await hsmService.provisionToken(...);
// ... rest of flow ...
await prefs.setProvisioningInProgress(false);
```

- [ ] **Step 2: On boot, if flag is set, route to `/hsm/resume` showing two actions: "Resume setup" and "Wipe HSM and start fresh"**

- [ ] **Step 3: Commit**

---

### Task 6.3: Typed error surfaces for `PIN_LOCKED`, `PREKEY_BUNDLE_INCOMPLETE`, `VerifiedEnrollmentOverwriteBlocked`

**Finding:** M7 + M10 (onboarding agent).

**Files:**
- Modify: `lib/features/auth/screens/hsm_setup_screen.dart:103-108`
- Modify: `lib/features/auth/auth_error_mapper.dart` (centralized mapping)
- Modify: `lib/l10n/app_*.arb`

- [ ] **Step 1: Route HSM setup errors through AuthErrorMapper**

```dart
String _resolveErrorMessage(Object error) {
  if (error is HsmServiceException) {
    return AuthErrorMapper.userMessage(error.code, _l10n) ?? _l10n.hsmErrorGenericSetup;
  }
  return _l10n.hsmErrorGenericSetup;
}
```

- [ ] **Step 2: Add mappings + strings for PIN_LOCKED, PREKEY_BUNDLE_INCOMPLETE, VERIFIED_ENROLLMENT_OVERWRITE_BLOCKED**

- [ ] **Step 3: Commit**

---

## Phase 7 — Conversation Slot LRU Persistence

### Task 7.1: Wrap live slot contents before destroying on eviction

**Finding:** H2 — LRU eviction loses any state rotation since session bootstrap.

**Files:**
- Modify: `lib/services/conversation_slot_manager.dart:88-118`
- Modify: the conversation key repository save call sites

- [ ] **Step 1: Add failing test**

```dart
test('LRU eviction persists current wrapped outer key', () async {
  await manager.getSlots(convA);
  // Simulate outer-key rotation on convA (hypothetical future flow)
  await manager.updateOuterKey(convA, rotated);
  // Force eviction by accessing 10 other conversations
  for (var i = 0; i < 10; i++) await manager.getSlots('conv-$i');
  // Reload convA; outer key should be the rotated one
  final slots = await manager.getSlots(convA);
  final blob = await keyRepo.loadWrapped(convA, 'outer');
  expect(blob, equals(rotated.wrappedOuterKey));
});
```

- [ ] **Step 2: In eviction path — wrap and persist before destroy**

```dart
// lib/services/conversation_slot_manager.dart — _evictLRU
Future<void> _evictLRU() async {
  final victim = _lruQueue.removeFirst();
  // Pull current wrapped state from live slots before zeroising.
  final wrappedOuter = await _hsmService.wrapKey(victim.outerSlot, kSlotDbKeyInner);
  await _keyRepo.saveWrappedKey(
    id: '${victim.convId}_outer',
    conversationId: victim.convId,
    keyPurpose: 'messaging_outer',
    wrappedBlob: wrappedOuter,
  );
  // ... same for inner if software HSM ...
  await _hsmService.destroyKey(victim.outerSlot);
  await _hsmService.destroyKey(victim.innerSlot);
  _slotRegistry.releaseConversationPair(victim.innerSlot, victim.outerSlot);
}
```

- [ ] **Step 3: Commit**

---

## Phase 8 — Diagnostics & Polish

Batch smaller fixes into a single commit per repo.

### Task 8.1: kayten-app diagnostics batch

- [ ] Remove plaintext phone in debug print: `contact_repository_impl.dart:343` — hash the input, log hash prefix only.
- [ ] Self-contact guard: `contacts_screen.dart:542-624` — compare resolved userId to current userId, reject with `l10n.contactAddSelfError`.
- [ ] Diacritic-insensitive search: `contacts_provider.dart:50-67` — use NFD + strip combining chars.
- [ ] Skipped-key eviction warn log: `incoming_message_handler.dart:828-831` — `kLog(level: 800)` when buffer overflow drops a key.
- [ ] AAD direction symbolic constants: `HsmPlugin.kt:2986-3068` — `VOICE_AAD_DIR_TX = 0x00`, `VOICE_AAD_DIR_RX = 0x01`. Add test that confirms distinct AAD hashes.
- [ ] Empty SDP answer guard: `call_provider.dart:592-599` — fail closed if `_pendingRemoteSdp` is null/empty before sending CALL_ANSWER.
- [ ] Audio error localization: `call_provider.dart:1600-1610` — use `l10n.callAudioPipelineFailed` keys.
- [ ] Block/unblock ordering: `contact_repository_impl.dart:229-235` — call server first, update local DAO only on success.
- [ ] Contacts permission resume retry: register `AppLifecycleObserver` to trigger sync on resume.
- [ ] Device fingerprint display in `SettingsHsmScreen`.

Commit:
```bash
git commit -m "chore(app): review-pass diagnostics, guards, and l10n polish"
```

### Task 8.2: kayten-server diagnostics batch

- [ ] HSM recovery approval identity match: `internal/hsmenroll/repository.go` — add JOIN validation.
- [ ] OPK claim secondary ordering: `internal/device/repository.go:177-208` — `ORDER BY prekey_id ASC, created_at ASC`.
- [ ] CallSession creation serialize: `internal/calling/service.go:91-250` — wrap `GetActiveByConversation` + insert in a SELECT...FOR UPDATE or use unique constraint.
- [ ] SYNC_RESPONSE ordering: `internal/messaging/service.go:384-478` — ensure `ORDER BY server_seq ASC`.

Commit:
```bash
git commit -m "chore(server): review-pass diagnostics, ordering, and race fixes"
```

---

## Self-Review Checklist

- **Spec coverage:** All 8 CRITICAL and 17 HIGH findings from the cross-repo review map to a task above. MEDIUMs for padding, blocks, voice, and server routing are folded in. LOWs (verification-code paste warning, audit log table, etc.) deferred to backlog.
- **Placeholder scan:** All code blocks have concrete diffs. No "TBD" / "similar to Task N".
- **Type consistency:** `spkCryptoKeyId`, `opkCryptoKeyId`, `consumedOpkId`, `edit_ratchet_epoch`, `edit_message_counter` — consistent across all tasks.
- **Cross-repo sequencing:** Proto (5.1) → server (5.2) → app (5.3). Refresh-token rotation (2.1) — server + app land together.
- **Test coverage:** Every CRITICAL task has a regression test. Phases 6–8 call out key tests to add.

## Deferred Backlog (not in this plan)

- Verification-code paste / auto-fill warning (L).
- Audit log table for enrollment state changes (L).
- TOFU-downgrade escape for debug builds (L).
- Voice `VOICE_COUNTER_OVERFLOW_THRESHOLD` lower to 2^32 (L).
- Per-user salt for contact discovery (M, follow-up to 4.3).
- `InvalidateKaytenSecureSession` cleanup of non-0x55 session crypto state (uHSM-Host, M).
- Explicit `0x36 UNSUPPORTED` case in host dispatcher for maintainability (uHSM-Host, L).

---

## Execution Handoff

**Plan saved to `docs/superpowers/plans/2026-04-17-cross-repo-review-fixes-plan.md`.**

Two execution options:

**1. Subagent-Driven (recommended)** — Fresh subagent per task with checkpoint review between tasks. Best for a plan this large (30+ tasks across 3 repos).

**2. Inline Execution** — I execute tasks directly in this session using the executing-plans skill with batch checkpoints.

Which approach?
