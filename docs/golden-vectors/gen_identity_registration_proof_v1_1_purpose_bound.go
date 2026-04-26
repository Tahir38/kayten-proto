// Generator for the WP2 B1 firmware-side purpose-bound golden vector.
//
// Cross-repo plan WP7-1 (Session B 2026-04-25 review). The hardware HSM
// firmware running KAYTEN_FW_VERSION_MINOR=1 wraps the wire-supplied
// 32-byte digest as `signedDigest = SHA-256(purposeByte || digest)`
// before Ed25519 signing. The legacy bare vector (v1, in
// `identity-registration-proof-v1.json`) continues to lock the
// fw_minor=0 / SoftHSM dev contract: server dual-verifies during the
// fleet rollout window so both vectors are valid simultaneously.
//
// Inputs are byte-equal to the bare-vector inputs; only the digest and
// signature differ. This guarantees every cross-language consumer can
// reuse the same encoder + transcript bytes and switch only the final
// hash + sign step depending on whether the caller is a fw_minor=1
// hardware HSM or a fw_minor=0 SoftHSM build.
//
// Build / run:
//   go run gen_identity_registration_proof_v1_1_purpose_bound.go > identity-registration-proof-v1.1-purpose-bound.json
package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
)

// Identical to the v1 generator; copied here so the v1.1 generator is
// runnable in isolation (each generator is its own `package main`).
func encodeIdentityRegistrationProofTranscript(
	domainTag []byte,
	userId, phoneBindingId, deviceId, hsmSerial string,
	ed25519Pub, ecdhPub []byte,
	commitState uint32,
	commitEpoch uint64,
	issuedAtUnixMs int64,
	serverNonce, clientNonce []byte,
) []byte {
	if len(domainTag) != 32 {
		panic("domain_tag must be 32 bytes")
	}
	if len(ed25519Pub) != 32 {
		panic("ed25519_identity_pub must be 32 bytes")
	}
	if len(ecdhPub) != 33 && len(ecdhPub) != 65 {
		panic("ecdh_identity_pub must be 33 or 65 bytes")
	}
	if len(serverNonce) != 32 {
		panic("server_nonce must be 32 bytes")
	}
	if len(clientNonce) != 32 {
		panic("client_nonce must be 32 bytes")
	}

	out := make([]byte, 0, 512)
	out = append(out, domainTag...)

	writeLen := func(b []byte) []byte {
		var lp [4]byte
		binary.BigEndian.PutUint32(lp[:], uint32(len(b)))
		return append(lp[:], b...)
	}
	writeStr := func(s string) { out = append(out, writeLen([]byte(s))...) }
	writeBytes := func(b []byte) { out = append(out, writeLen(b)...) }

	writeStr(userId)
	writeStr(phoneBindingId)
	writeStr(deviceId)
	writeStr(hsmSerial)
	writeBytes(ed25519Pub)
	writeBytes(ecdhPub)

	var u32 [4]byte
	binary.BigEndian.PutUint32(u32[:], commitState)
	out = append(out, u32[:]...)

	var u64 [8]byte
	binary.BigEndian.PutUint64(u64[:], commitEpoch)
	out = append(out, u64[:]...)

	binary.BigEndian.PutUint64(u64[:], uint64(issuedAtUnixMs))
	out = append(out, u64[:]...)

	writeBytes(serverNonce)
	writeBytes(clientNonce)

	return out
}

func repeatByte(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func main() {
	// Same fixed seed as v1 so cross-language consumers can reuse the
	// keypair derivation.
	seed := repeatByte(0x11, 32)
	edPriv := ed25519.NewKeyFromSeed(seed)
	edPub := edPriv.Public().(ed25519.PublicKey)

	p256 := elliptic.P256()
	scalarBytes := repeatByte(0x22, 32)
	scalar := new(big.Int).Mod(new(big.Int).SetBytes(scalarBytes), p256.Params().N)
	px, py := p256.ScalarBaseMult(scalar.Bytes())
	ecdhPub := elliptic.Marshal(p256, px, py)
	_ = &ecdsa.PrivateKey{}

	domainTag := sha256.Sum256([]byte("kayten-identity-registration-v1"))

	userId := "user-0000000000000000000000000001"
	phoneBindingId := "pb-00000000-0000-4000-8000-000000000001"
	deviceId := "device-0000000000000000000000001"
	hsmSerial := "HSMSN-TEST-0001"
	commitState := uint32(2)
	commitEpoch := uint64(1)
	issuedAtUnixMs := int64(1745000000000)
	serverNonce := repeatByte(0xAA, 32)
	clientNonce := repeatByte(0x55, 32)

	// Identity-registration purpose byte (0x01) per
	// kayten-server/internal/device/proof_verifier.go and the
	// uHSM-HSM Kayten_ProcessIdentitySign handler under fw_minor=1.
	const purposeIdentityRegistration byte = 0x01

	transcript := encodeIdentityRegistrationProofTranscript(
		domainTag[:],
		userId, phoneBindingId, deviceId, hsmSerial,
		edPub, ecdhPub,
		commitState, commitEpoch, issuedAtUnixMs,
		serverNonce, clientNonce,
	)

	// Wire digest — what the host/SDK hands to the HSM.
	wireDigest := sha256.Sum256(transcript)

	// Firmware-bound signed digest (fw_minor=1 path).
	hashInput := make([]byte, 0, 33)
	hashInput = append(hashInput, purposeIdentityRegistration)
	hashInput = append(hashInput, wireDigest[:]...)
	signedDigest := sha256.Sum256(hashInput)

	signature := ed25519.Sign(edPriv, signedDigest[:])
	if !ed25519.Verify(edPub, signedDigest[:], signature) {
		fmt.Fprintln(os.Stderr, "signature self-verify failed")
		os.Exit(1)
	}

	out := map[string]any{
		"description":     "WP2 B1 firmware-side purpose-bound golden vector for IdentityRegistrationProof. Hardware HSM running KAYTEN_FW_VERSION_MINOR=1 produces a signature over SHA-256(purposeByte || SHA-256(transcript)) — the SoftHSM / fw_minor=0 path is in the bare vector at identity-registration-proof-v1.json. Server dual-verifies during fleet rollout window.",
		"encoder_version": "canonical-transcript-encoder-v1",
		"firmware_purpose_binding_version": "v1.1",
		"firmware_minor_required":          1,
		"purpose":         "kayten-identity-registration-v1",
		"purpose_byte":    purposeIdentityRegistration,
		"domain_tag_hex":  hex.EncodeToString(domainTag[:]),
		"inputs": map[string]any{
			"user_id":                                  userId,
			"phone_binding_id":                         phoneBindingId,
			"device_id":                                deviceId,
			"hsm_serial":                               hsmSerial,
			"ed25519_identity_pub_hex":                 hex.EncodeToString(edPub),
			"ed25519_identity_priv_seed_hex_test_only": hex.EncodeToString(seed),
			"ecdh_identity_pub_hex":                    hex.EncodeToString(ecdhPub),
			"ecdh_identity_priv_scalar_hex_test_only":  hex.EncodeToString(scalar.Bytes()),
			"commit_state":                             commitState,
			"commit_epoch":                             commitEpoch,
			"issued_at_unix_ms":                        issuedAtUnixMs,
			"server_nonce_hex":                         hex.EncodeToString(serverNonce),
			"client_nonce_hex":                         hex.EncodeToString(clientNonce),
		},
		"transcript_hex":         hex.EncodeToString(transcript),
		"transcript_len":         len(transcript),
		"wire_digest_hex":        hex.EncodeToString(wireDigest[:]),
		"signed_digest_hex":      hex.EncodeToString(signedDigest[:]),
		"signature_hex":          hex.EncodeToString(signature),
		"notes": []string{
			"Domain tag is sha256(\"kayten-identity-registration-v1\") = 40547e31...e4d6 — same as the bare vector.",
			"Encoder transcript is byte-identical to identity-registration-proof-v1.json — only the final hash + sign differ.",
			"wire_digest is what the host/SDK hands to the HSM (SHA-256 of transcript).",
			"signed_digest is the firmware-side wrapped value: SHA-256(purpose_byte || wire_digest).",
			"signature is Ed25519_Sign(slot22_priv, signed_digest) and matches what a fw_minor=1 hardware HSM emits.",
			"Verifiers MUST replicate the wrap: parse purpose_byte (=0x01 for identity-registration-v1), compute wrapped = SHA-256(purpose_byte || wire_digest), then ed25519.Verify(pub, wrapped, signature).",
			"During the fleet rollout window the server SHOULD dual-verify: try the wrapped form first, fall back to the bare wire_digest if the bare verifier accepts. The unwrapped fallback is safe to drop after the fleet flips to fw_minor>=1 (observable via the 0x49 GET_RUNTIME_STATUS_V1 trailer once KAYTEN_HSM_CAP_RUNTIME_STATUS_V2 is advertised).",
			"Private-key fields in this vector are TEST-ONLY and are intentionally shipped so downstream repos can reproduce the signature. They MUST NOT be reused for production identity.",
		},
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Stdout.Write(b)
	os.Stdout.Write([]byte("\n"))
}
