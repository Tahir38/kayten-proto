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

// Canonical transcript encoder v1 (kayten-proto
// docs/canonical-transcript-encoder-v1.md).
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
	// Deterministic Ed25519 keypair from fixed 32-byte seed.
	seed := repeatByte(0x11, 32)
	edPriv := ed25519.NewKeyFromSeed(seed)
	edPub := edPriv.Public().(ed25519.PublicKey)

	// Deterministic P-256 keypair from fixed 32-byte scalar.
	p256 := elliptic.P256()
	scalarBytes := repeatByte(0x22, 32)
	// Reduce mod N to ensure valid scalar
	scalar := new(big.Int).Mod(new(big.Int).SetBytes(scalarBytes), p256.Params().N)
	px, py := p256.ScalarBaseMult(scalar.Bytes())
	ecdhPub := elliptic.Marshal(p256, px, py) // 65 bytes uncompressed
	_ = &ecdsa.PrivateKey{}                   // keep ecdsa import referenced

	// Domain tag — SHA-256("kayten-identity-registration-v1").
	domainTag := sha256.Sum256([]byte("kayten-identity-registration-v1"))

	userId := "user-0000000000000000000000000001"
	phoneBindingId := "pb-00000000-0000-4000-8000-000000000001"
	deviceId := "device-0000000000000000000000001"
	hsmSerial := "HSMSN-TEST-0001"
	commitState := uint32(2) // IDENTITY_COMMIT_STATE_COMMITTED
	commitEpoch := uint64(1)
	issuedAtUnixMs := int64(1745000000000) // Fixed 2025-04-18T21:13:20Z approx
	serverNonce := repeatByte(0xAA, 32)
	clientNonce := repeatByte(0x55, 32)

	transcript := encodeIdentityRegistrationProofTranscript(
		domainTag[:],
		userId, phoneBindingId, deviceId, hsmSerial,
		edPub, ecdhPub,
		commitState, commitEpoch, issuedAtUnixMs,
		serverNonce, clientNonce,
	)

	digest := sha256.Sum256(transcript)
	signature := ed25519.Sign(edPriv, digest[:])
	ok := ed25519.Verify(edPub, digest[:], signature)
	if !ok {
		fmt.Fprintln(os.Stderr, "signature self-verify failed")
		os.Exit(1)
	}

	out := map[string]any{
		"description":                           "Canonical golden vector for IdentityRegistrationProof v1 (kayten-proto docs/canonical-transcript-encoder-v1.md).",
		"encoder_version":                       "canonical-transcript-encoder-v1",
		"purpose":                               "kayten-identity-registration-v1",
		"domain_tag_hex":                        hex.EncodeToString(domainTag[:]),
		"inputs": map[string]any{
			"user_id":              userId,
			"phone_binding_id":     phoneBindingId,
			"device_id":            deviceId,
			"hsm_serial":           hsmSerial,
			"ed25519_identity_pub_hex": hex.EncodeToString(edPub),
			"ed25519_identity_priv_seed_hex_test_only": hex.EncodeToString(seed),
			"ecdh_identity_pub_hex":    hex.EncodeToString(ecdhPub),
			"ecdh_identity_priv_scalar_hex_test_only":  hex.EncodeToString(scalar.Bytes()),
			"commit_state":         commitState,
			"commit_epoch":         commitEpoch,
			"issued_at_unix_ms":    issuedAtUnixMs,
			"server_nonce_hex":     hex.EncodeToString(serverNonce),
			"client_nonce_hex":     hex.EncodeToString(clientNonce),
		},
		"transcript_hex": hex.EncodeToString(transcript),
		"transcript_len": len(transcript),
		"digest_hex":     hex.EncodeToString(digest[:]),
		"signature_hex":  hex.EncodeToString(signature),
		"notes": []string{
			"Domain tag is sha256(\"kayten-identity-registration-v1\") = 40547e31...e4d6.",
			"Every variable-length field is preceded by a 4-byte big-endian length.",
			"Integers are big-endian, unsigned unless noted (issued_at_unix_ms is two's-complement int64).",
			"The Ed25519 signature is over sha256(transcript), not over the raw transcript.",
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
