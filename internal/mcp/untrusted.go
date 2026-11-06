package mcp

import (
	"crypto/rand"
	"encoding/hex"
)

// untrustedNonce returns a fresh random marker id for one tool response.
func untrustedNonce() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b)
}

// untrustedBlock frames target-derived text with unforgeable delimiters.
func untrustedBlock(nonce, body string) string {
	return "<<untrusted:" + nonce + ">>\n" + body + "\n<</untrusted:" + nonce + ">>"
}
