package msgcodec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// hmacHex mirrors hmac_sha256_hex for assertions.
func hmacHex(key, data string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}
