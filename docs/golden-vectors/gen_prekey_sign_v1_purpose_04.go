// Generator for the prekey-sign purpose-0x04 golden vector.
//
// Cross-repo spec: kayten-app/docs/superpowers/plans/
//   2026-04-27-cross-repo-prekey-purpose-allocation-spec.md §5 (Order 1).
//
// The hardware HSM firmware running KAYTEN_FW_VERSION_MINOR=2 extends the
// identity-sign allow-list to include purpose 0x04 (prekey-sign). For a
// prekey ECDH public key (P-256 uncompressed, 65 bytes) the signing flow is:
//
//   payload32    = SHA-256(ecdh_pub)           // 32-byte hash of raw P-256 pub
//   signed_digest = SHA-256(0x04 || payload32)  // firmware-side wrap
//   signature     = Ed25519(identity_priv, signed_digest)
//
// This covers both SPK and OPK because they authenticate the same statement
// type ("this ECDH public prekey belongs to this device identity and may be
// used for X3DH bootstrap") — spec §2.1.
//
// The seed used here (0x33 * 32) is deliberately different from the identity-
// registration vector seeds (0x11/0x22) so downstream test suites can detect
// cross-vector confusion.
//
// Build / run:
//   cd docs/golden-vectors
//   go run gen_prekey_sign_v1_purpose_04.go > prekey-sign-v1-purpose-04.json
//
// The output MUST be bit-identical to the committed vector on every run.
package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
)

func repeatByte(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func main() {
	// ── Ed25519 identity keypair (seed 0x33 * 32) ──────────────────────────
	// TEST KEY ONLY — must not appear in any non-test context.
	seed := repeatByte(0x33, 32)
	edPriv := ed25519.NewKeyFromSeed(seed)
	edPub := edPriv.Public().(ed25519.PublicKey)

	// ── P-256 prekey ECDH keypair (scalar 0x44 * 32) ───────────────────────
	// Prekeys are ECDH P-256 (uncompressed, 65 bytes). All existing verifiers
	// in kayten-server/internal/device/service.go operate on the raw P-256 pub.
	// TEST KEY ONLY.
	_ = &ecdsa.PrivateKey{} // keeps the ecdsa import live; not used directly
	p256 := elliptic.P256()
	ecdhScalar := repeatByte(0x44, 32)
	scalar := new(big.Int).Mod(new(big.Int).SetBytes(ecdhScalar), p256.Params().N)
	px, py := p256.ScalarBaseMult(scalar.Bytes())
	ecdhPub := elliptic.Marshal(p256, px, py) // 65 bytes, leading 0x04

	if len(ecdhPub) != 65 {
		fmt.Fprintf(os.Stderr, "unexpected ecdhPub length: %d (want 65)\n", len(ecdhPub))
		os.Exit(1)
	}

	// ── Purpose byte ────────────────────────────────────────────────────────
	const purposePrekeySign byte = 0x04

	// ── payload32 = SHA-256(ecdh_pub) ───────────────────────────────────────
	payload32 := sha256.Sum256(ecdhPub)

	// ── signed_digest = SHA-256(0x04 || payload32) ──────────────────────────
	hashInput := make([]byte, 0, 33)
	hashInput = append(hashInput, purposePrekeySign)
	hashInput = append(hashInput, payload32[:]...)
	signedDigest := sha256.Sum256(hashInput)

	// ── Ed25519 signature over signed_digest ────────────────────────────────
	signature := ed25519.Sign(edPriv, signedDigest[:])
	if !ed25519.Verify(edPub, signedDigest[:], signature) {
		fmt.Fprintln(os.Stderr, "signature self-verify failed")
		os.Exit(1)
	}

	out := map[string]any{
		"description": "WP1 prekey-sign purpose-0x04 golden vector. " +
			"Hardware HSM running KAYTEN_FW_VERSION_MINOR=2 signs prekey ECDH " +
			"public keys (P-256 uncompressed, 65 bytes) as " +
			"Ed25519(identity_priv, SHA-256(0x04 || SHA-256(ecdh_pub))). " +
			"Server verifies this under KAYTEN_PREKEY_SIGN_PURPOSE_BINDING=wrapped_only " +
			"(or dual_verify during rollout). See spec §2, §3, §7.",
		"version":                     "prekey-sign-v1-purpose-04",
		"firmware_purpose_binding_version": "v1.2",
		"firmware_minor_required":     2,
		"purpose":                     "prekey-sign",
		"purpose_byte":                purposePrekeySign,
		"ecdh_curve":                  "P-256-uncompressed",
		"ecdh_pub_len_bytes":          len(ecdhPub),
		"inputs": map[string]any{
			"ed25519_identity_pub_hex":                  hex.EncodeToString(edPub),
			"ed25519_identity_priv_seed_hex_test_only":  hex.EncodeToString(seed),
			"ecdh_pub_hex":                              hex.EncodeToString(ecdhPub),
			"ecdh_priv_scalar_hex_test_only":            hex.EncodeToString(scalar.Bytes()),
		},
		"payload32_hex":     hex.EncodeToString(payload32[:]),
		"signed_digest_hex": hex.EncodeToString(signedDigest[:]),
		"signature_hex":     hex.EncodeToString(signature),
		"notes": []string{
			"TEST KEY ONLY — the Ed25519 seed (0x33*32) and P-256 scalar (0x44*32) are fixed and public.",
			"These keys MUST NOT appear in any production, staging, or non-test context.",
			"ecdh_pub is P-256 uncompressed (65 bytes, leading 0x04). All kayten-server prekey verifiers",
			"  operate on the raw P-256 pub bytes per kayten-server/internal/device/service.go verifyPrekeyEd25519.",
			"payload32 = SHA-256(ecdh_pub). This is the 32-byte payload the app passes to CMD_IDENTITY_SIGN (0x48).",
			"signed_digest = SHA-256(0x04 || payload32). Firmware computes this before Ed25519 signing (fw_minor=2 path).",
			"signature = Ed25519(identity_priv, signed_digest). This is what the server verifies under purpose_04 shape.",
			"Covers BOTH SPK and OPK — they authenticate the same statement type (spec §2.1).",
			"Seed 0x33*32 is distinct from identity-registration seeds (0x11/0x22) to prevent cross-vector confusion.",
			"Run: cd docs/golden-vectors && go run gen_prekey_sign_v1_purpose_04.go > prekey-sign-v1-purpose-04.json",
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
