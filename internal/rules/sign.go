package rules

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Signature is a detached ed25519 signature over the pack's integrity digest
// (name, version, and per-file sha256s). It proves publisher identity beyond
// the manifest sha256 gate (which only detects corruption/tamper-in-transit).
type Signature struct {
	Alg   string `json:"alg"` // "ed25519"
	KeyID string `json:"keyId"`
	Sig   string `json:"sig"` // base64 raw 64-byte signature
}

// OfficialKeyID is the key id embedded for Interseptor-published packs.
const OfficialKeyID = "interseptor-1"

// officialPubHex is the Interseptor v1 pack-signing public key (ed25519, hex).
// The matching private seed is held in CI / maintainer secrets — never shipped
// in the binary. Catalog packs embedded in the binary are trusted as builtin
// (same trust as the app code) and do not require this signature.
const officialPubHex = "48bc36b13427f6b0e877f0120a699a6bf02c5d6225a94e436655fb69f78431b0"

// Keyring maps key id → ed25519 public key.
type Keyring map[string]ed25519.PublicKey

// DefaultKeyring returns the official key plus any ~/.interseptor/trusted-pack-keys/*.pub
// files (hex-encoded 32-byte keys; filename stem = key id).
func DefaultKeyring(globalDir string) Keyring {
	kr := Keyring{}
	if pub, err := hex.DecodeString(officialPubHex); err == nil && len(pub) == ed25519.PublicKeySize {
		kr[OfficialKeyID] = ed25519.PublicKey(pub)
	}
	if globalDir == "" {
		return kr
	}
	dir := filepath.Join(globalDir, "trusted-pack-keys")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return kr
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".pub")
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		kr[id] = ed25519.PublicKey(raw)
	}
	return kr
}

// ManifestDigest is the canonical bytes signed for a pack: name, version, and
// sorted entry hashes. File contents are already bound via Entry.SHA256.
func ManifestDigest(m Manifest) []byte {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "v1\n%s\n%s\n", m.Name, m.Version)
	ents := append([]Entry(nil), m.Entries...)
	sort.Slice(ents, func(i, j int) bool {
		if ents[i].Kind != ents[j].Kind {
			return ents[i].Kind < ents[j].Kind
		}
		return ents[i].ID < ents[j].ID
	})
	for _, e := range ents {
		_, _ = fmt.Fprintf(h, "%s\t%s\t%s\n", e.Kind, e.ID, e.SHA256)
	}
	return h.Sum(nil)
}

// SignManifest attaches an ed25519 signature to a copy of m.
func SignManifest(m Manifest, priv ed25519.PrivateKey, keyID string) (Manifest, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return m, fmt.Errorf("rules: invalid ed25519 private key")
	}
	if strings.TrimSpace(keyID) == "" {
		keyID = OfficialKeyID
	}
	sig := ed25519.Sign(priv, ManifestDigest(m))
	m.Signature = &Signature{
		Alg:   "ed25519",
		KeyID: keyID,
		Sig:   base64.StdEncoding.EncodeToString(sig),
	}
	return m, nil
}

// VerifyManifestSignature checks m.Signature against the keyring.
func VerifyManifestSignature(m Manifest, keys Keyring) error {
	if m.Signature == nil {
		return fmt.Errorf("rules: pack has no signature")
	}
	s := m.Signature
	if s.Alg != "" && s.Alg != "ed25519" {
		return fmt.Errorf("rules: unsupported signature alg %q", s.Alg)
	}
	pub, ok := keys[s.KeyID]
	if !ok {
		return fmt.Errorf("rules: unknown signing key %q (add a .pub under trusted-pack-keys/)", s.KeyID)
	}
	raw, err := base64.StdEncoding.DecodeString(s.Sig)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("rules: malformed signature")
	}
	if !ed25519.Verify(pub, ManifestDigest(m), raw) {
		return fmt.Errorf("rules: signature verification failed for key %q", s.KeyID)
	}
	return nil
}

// ParsePrivateSeed loads a 32-byte hex seed (or 64-byte hex private key) from a file or literal.
func ParsePrivateSeed(raw string) (ed25519.PrivateKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("rules: empty signing seed")
	}
	if b, err := os.ReadFile(raw); err == nil {
		raw = strings.TrimSpace(string(b))
	}
	bin, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("rules: signing seed must be hex: %w", err)
	}
	switch len(bin) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(bin), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(bin), nil
	default:
		return nil, fmt.Errorf("rules: signing seed must be 32 or 64 bytes (got %d)", len(bin))
	}
}

// WritePublicKeyFile writes pub as hex to path (0600 parent dir caller's job).
func WritePublicKeyFile(path string, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("rules: bad public key")
	}
	return os.WriteFile(path, []byte(hex.EncodeToString(pub)+"\n"), 0o644)
}

// signatureJSON is the on-disk member name inside a pack tarball.
const signatureName = "signature.json"

func encodeSignature(s *Signature) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("nil signature")
	}
	return json.MarshalIndent(s, "", "  ")
}
