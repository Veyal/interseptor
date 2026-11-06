package starx

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"go.starlark.net/starlark"
)

// Signing helpers. A request-signing API rejects every tampered body unless the
// signature, timestamp and nonce are recomputed, so a codec that only re-encodes
// the payload cannot be used to test it. These are the three primitives such a
// signature needs, without hand-rolled hex and time formatting in Starlark.

// NowMSBuiltin: now_ms() → unix milliseconds as an int. Deterministic tests pass
// a clock through the codec's own flow data instead.
func NowMSBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	return starlark.MakeInt64(time.Now().UnixMilli()), nil
}

// RandomHexBuiltin: random_hex(n) → n random bytes as 2n lowercase hex chars.
func RandomHexBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var n int
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "n", &n); err != nil {
		return nil, err
	}
	if n <= 0 || n > 256 {
		return nil, fmt.Errorf("random_hex: n must be 1..256 (got %d)", n)
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("random_hex: %w", err)
	}
	return starlark.String(hex.EncodeToString(buf)), nil
}

// HmacSHA256HexBuiltin: hmac_sha256_hex(key, data) → lowercase hex digest.
func HmacSHA256HexBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key, data string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "key", &key, "data", &data); err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return starlark.String(hex.EncodeToString(mac.Sum(nil))), nil
}

// SHA256HexBuiltin: sha256_hex(data) → lowercase hex digest.
func SHA256HexBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var data string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "data", &data); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(data))
	return starlark.String(hex.EncodeToString(sum[:])), nil
}
