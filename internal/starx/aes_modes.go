package starx

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"

	"go.starlark.net/starlark"
)

// AES-CBC and AES-CTR helpers. These are the two shapes a mobile envelope
// usually takes — `hex(IV || CBC(payload))` and `nonce || CTR(payload)` — and
// emulating CBC on top of the ECB helper costs ~10M Starlark steps because
// Starlark strings expose no raw bytes. A native builtin makes the codec ten
// lines and keeps it under the step budget.

// decodeDataArg accepts raw bytes, standard base64, or — when data_format is
// "hex" — hex, mirroring the ECB helpers' ciphertext handling.
func decodeDataArg(s, format string) ([]byte, error) {
	switch format {
	case "raw":
		return []byte(s), nil
	case "hex":
		b, err := decodeHexLoose(s)
		if err != nil {
			return nil, fmt.Errorf("data_format=hex: %w", err)
		}
		return b, nil
	case "", "auto":
		if b, err := base64.StdEncoding.DecodeString(s); err == nil {
			return b, nil
		}
		if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
			return b, nil
		}
		return []byte(s), nil
	default:
		return nil, fmt.Errorf("data_format must be \"auto\", \"raw\" or \"hex\" (got %q)", format)
	}
}

// decodeIVArg interprets an IV argument. A 32-char hex string is the common
// "IV||CBC" shape, and base64 of 16 bytes is the other, so "auto" prefers hex
// when it decodes to exactly one block and otherwise falls through to
// decodeDataArg.
func decodeIVArg(s, format string) ([]byte, error) {
	switch format {
	case "", "auto":
		// 32 hex chars is one block: the common "IV||CBC" shape.
		if len(s) == 2*aes.BlockSize {
			if b, err := decodeHexLoose(s); err == nil {
				return b, nil
			}
		}
		// A literal 16-character string is a raw IV, even when it also parses as
		// hex of half a block (which base64 would then mangle).
		if len(s) == aes.BlockSize {
			return []byte(s), nil
		}
	}
	return decodeDataArg(s, format)
}

// AESCBCDecrypt decrypts CBC with PKCS7 unpadding.
func AESCBCDecrypt(key, ciphertext, iv []byte) ([]byte, error) {
	k, err := aesKey(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("IV must be 16 bytes (got %d)", len(iv))
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext length must be a multiple of 16 (got %d)", len(ciphertext))
	}
	if len(ciphertext) > aesMaxBytes {
		return nil, fmt.Errorf("ciphertext too large (max %d bytes)", aesMaxBytes)
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ciphertext)
	return pkcs7Unpad(out, aes.BlockSize)
}

// AESCBCEncrypt encrypts CBC with PKCS7 padding.
func AESCBCEncrypt(key, plaintext, iv []byte) ([]byte, error) {
	k, err := aesKey(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("IV must be 16 bytes (got %d)", len(iv))
	}
	if len(plaintext) > aesMaxBytes {
		return nil, fmt.Errorf("plaintext too large (max %d bytes)", aesMaxBytes)
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return out, nil
}

// AESCTR XORs with the keystream; CTR is unpadded, so plaintext is the same
// length as the ciphertext.
func AESCTR(key, data, iv []byte) ([]byte, error) {
	k, err := aesKey(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("IV must be 16 bytes (got %d)", len(iv))
	}
	if len(data) > aesMaxBytes {
		return nil, fmt.Errorf("data too large (max %d bytes)", aesMaxBytes)
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(data))
	cipher.NewCTR(block, iv).XORKeyStream(out, data)
	return out, nil
}

// AESCBCDecryptBuiltin: aes_cbc_decrypt(key, ciphertext, iv, key_format?, data_format?, iv_format?)
// → plaintext. The IV may be given separately or as the first 16 bytes of the
// ciphertext by the codec; the builtin takes it explicitly.
func AESCBCDecryptBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key, ciphertext, iv, keyFormat, dataFormat, ivFormat string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"key", &key, "ciphertext", &ciphertext, "iv", &iv,
		"key_format?", &keyFormat, "data_format?", &dataFormat, "iv_format?", &ivFormat); err != nil {
		return nil, err
	}
	kb, err := decodeKeyArg(key, keyFormat)
	if err != nil {
		return nil, fmt.Errorf("aes_cbc_decrypt: %w", err)
	}
	ct, err := decodeDataArg(ciphertext, dataFormat)
	if err != nil {
		return nil, fmt.Errorf("aes_cbc_decrypt: %w", err)
	}
	ivb, err := decodeIVArg(iv, ivFormat)
	if err != nil {
		return nil, fmt.Errorf("aes_cbc_decrypt: %w", err)
	}
	pt, err := AESCBCDecrypt(kb, ct, ivb)
	if err != nil {
		return nil, fmt.Errorf("aes_cbc_decrypt: %w", err)
	}
	return starlark.String(string(pt)), nil
}

// AESCBCEncryptBuiltin: aes_cbc_encrypt(key, plaintext, iv, ...) → base64 ciphertext.
func AESCBCEncryptBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key, plaintext, iv, keyFormat, ivFormat string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"key", &key, "plaintext", &plaintext, "iv", &iv,
		"key_format?", &keyFormat, "iv_format?", &ivFormat); err != nil {
		return nil, err
	}
	kb, err := decodeKeyArg(key, keyFormat)
	if err != nil {
		return nil, fmt.Errorf("aes_cbc_encrypt: %w", err)
	}
	ivb, err := decodeIVArg(iv, ivFormat)
	if err != nil {
		return nil, fmt.Errorf("aes_cbc_encrypt: %w", err)
	}
	ct, err := AESCBCEncrypt(kb, []byte(plaintext), ivb)
	if err != nil {
		return nil, fmt.Errorf("aes_cbc_encrypt: %w", err)
	}
	return starlark.String(base64.StdEncoding.EncodeToString(ct)), nil
}

// AESCTRBuiltin: aes_ctr(key, data, iv, ...) → base64 keystream output. CTR is
// symmetric, so the same call encrypts and decrypts.
func AESCTRBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key, data, iv, keyFormat, dataFormat, ivFormat string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"key", &key, "data", &data, "iv", &iv,
		"key_format?", &keyFormat, "data_format?", &dataFormat, "iv_format?", &ivFormat); err != nil {
		return nil, err
	}
	kb, err := decodeKeyArg(key, keyFormat)
	if err != nil {
		return nil, fmt.Errorf("aes_ctr: %w", err)
	}
	db, err := decodeDataArg(data, dataFormat)
	if err != nil {
		return nil, fmt.Errorf("aes_ctr: %w", err)
	}
	ivb, err := decodeIVArg(iv, ivFormat)
	if err != nil {
		return nil, fmt.Errorf("aes_ctr: %w", err)
	}
	out, err := AESCTR(kb, db, ivb)
	if err != nil {
		return nil, fmt.Errorf("aes_ctr: %w", err)
	}
	return starlark.String(base64.StdEncoding.EncodeToString(out)), nil
}
