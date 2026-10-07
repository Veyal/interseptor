package pmsandbox

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math/rand/v2"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Veyal/interseptor/internal/jsrt"
	"github.com/Veyal/interseptor/internal/scriptctx"
)

// host is the Go side of the sandbox: the only functions a script can reach
// are the ones in funcs(), and they are captured by the prelude closure and
// then removed from the global object.
type host struct {
	ctx    context.Context
	in     *Input
	rnd    func() float64
	mu     sync.Mutex
	con    []scriptctx.ConsoleLine
	conLen int
	conCut bool
	sends  []scriptctx.SendRecord
	nsend  int
}

func newHost(ctx context.Context, in *Input) *host {
	h := &host{ctx: ctx, in: in, rnd: in.Rand}
	if h.rnd == nil {
		h.rnd = rand.Float64
	}
	return h
}

func (h *host) funcs() map[string]any {
	return map[string]any{
		"conv":    jsrt.Func(h.fnConv),
		"hash":    jsrt.Func(h.fnHash),
		"hmac":    jsrt.Func(h.fnHMAC),
		"aes":     jsrt.Func(h.fnAES),
		"gcm":     jsrt.Func(h.fnGCM),
		"pbkdf2":  jsrt.Func(h.fnPBKDF2),
		"randhex": jsrt.Func(h.fnRandHex),
		"uuid":    jsrt.Func(h.fnUUID),
		"console": jsrt.Func(h.fnConsole),
		"send":    jsrt.Func(h.fnSend),
		"scope":   jsrt.Func(h.fnScope),
	}
}

func argStr(a []any, i int) string {
	if i >= len(a) {
		return ""
	}
	if s, ok := a[i].(string); ok {
		return s
	}
	return fmt.Sprint(a[i])
}

func argInt(a []any, i int) int {
	if i >= len(a) {
		return 0
	}
	switch v := a[i].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func (h *host) maxBytes() int { return h.in.Limits.withDefaults(h.in.Caps).MaxAlloc }

func decodeHex(s string, max int) ([]byte, error) {
	if len(s)/2 > max {
		return nil, errors.New("data exceeds sandbox limit")
	}
	return hex.DecodeString(s)
}

// codec converts between the string encodings the prelude uses.
func decodeText(format, s string) ([]byte, error) {
	switch format {
	case "utf8":
		return []byte(s), nil
	case "hex":
		return hex.DecodeString(s)
	case "base64", "base64url":
		t := strings.NewReplacer("-", "+", "_", "/", "=", "", "\n", "", "\r", "", " ", "").Replace(s)
		return base64.RawStdEncoding.DecodeString(t)
	case "latin1":
		out := make([]byte, 0, len(s))
		for _, r := range s {
			if r > 255 {
				return nil, errors.New("character outside latin1 range")
			}
			out = append(out, byte(r))
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown encoding %q", format)
}

func encodeText(format string, b []byte) (string, error) {
	switch format {
	case "utf8":
		if utf8.Valid(b) {
			return string(b), nil
		}
		return strings.ToValidUTF8(string(b), "�"), nil
	case "hex":
		return hex.EncodeToString(b), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(b), nil
	case "base64url":
		return base64.RawURLEncoding.EncodeToString(b), nil
	case "latin1":
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		return string(r), nil
	}
	return "", fmt.Errorf("unknown encoding %q", format)
}

func (h *host) fnConv(a []any) (any, error) {
	if len(argStr(a, 2)) > h.maxBytes()*2 {
		return nil, errors.New("conv: input exceeds sandbox limit")
	}
	b, err := decodeText(argStr(a, 0), argStr(a, 2))
	if err != nil {
		return nil, fmt.Errorf("conv: %w", err)
	}
	return encodeText(argStr(a, 1), b)
}

func hasher(alg string) (func() hash.Hash, error) {
	switch alg {
	case "md5":
		return md5.New, nil
	case "sha1":
		return sha1.New, nil
	case "sha224":
		return sha256.New224, nil
	case "sha256":
		return sha256.New, nil
	case "sha384":
		return sha512.New384, nil
	case "sha512":
		return sha512.New, nil
	}
	return nil, fmt.Errorf("unsupported hash %q", alg)
}

func (h *host) fnHash(a []any) (any, error) {
	f, err := hasher(argStr(a, 0))
	if err != nil {
		return nil, err
	}
	data, err := decodeHex(argStr(a, 1), h.maxBytes())
	if err != nil {
		return nil, err
	}
	s := f()
	s.Write(data)
	return hex.EncodeToString(s.Sum(nil)), nil
}

func (h *host) fnHMAC(a []any) (any, error) {
	f, err := hasher(argStr(a, 0))
	if err != nil {
		return nil, err
	}
	key, err := decodeHex(argStr(a, 1), h.maxBytes())
	if err != nil {
		return nil, err
	}
	data, err := decodeHex(argStr(a, 2), h.maxBytes())
	if err != nil {
		return nil, err
	}
	m := hmac.New(f, key)
	m.Write(data)
	return hex.EncodeToString(m.Sum(nil)), nil
}

func padPKCS7(b []byte) []byte {
	n := aes.BlockSize - len(b)%aes.BlockSize
	out := make([]byte, len(b)+n)
	copy(out, b)
	for i := len(b); i < len(out); i++ {
		out[i] = byte(n)
	}
	return out
}

func unpadPKCS7(b []byte) ([]byte, error) {
	if len(b) == 0 || len(b)%aes.BlockSize != 0 {
		return nil, errors.New("aes: invalid padded data length")
	}
	n := int(b[len(b)-1])
	if n == 0 || n > aes.BlockSize || n > len(b) {
		return nil, errors.New("aes: bad padding")
	}
	for _, c := range b[len(b)-n:] {
		if int(c) != n {
			return nil, errors.New("aes: bad padding")
		}
	}
	return b[:len(b)-n], nil
}

// fnAES(op, mode, keyHex, ivHex, dataHex, pad) -> hex.
func (h *host) fnAES(a []any) (any, error) {
	op, mode, pad := argStr(a, 0), argStr(a, 1), argStr(a, 5)
	key, err := decodeHex(argStr(a, 2), 64)
	if err != nil {
		return nil, err
	}
	iv, err := decodeHex(argStr(a, 3), 64)
	if err != nil {
		return nil, err
	}
	data, err := decodeHex(argStr(a, 4), h.maxBytes())
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	enc := op == "enc"
	stream := mode == "ctr" || mode == "cfb" || mode == "ofb"
	if pad == "pkcs7" && enc {
		data = padPKCS7(data)
	}
	if mode != "ecb" && len(iv) != aes.BlockSize {
		return nil, errors.New("aes: iv must be 16 bytes")
	}
	if !stream && len(data)%aes.BlockSize != 0 {
		return nil, errors.New("aes: data is not a multiple of the block size")
	}
	out := make([]byte, len(data))
	switch mode {
	case "cbc":
		if enc {
			cipher.NewCBCEncrypter(blk, iv).CryptBlocks(out, data)
		} else {
			cipher.NewCBCDecrypter(blk, iv).CryptBlocks(out, data)
		}
	case "ecb":
		for i := 0; i < len(data); i += aes.BlockSize {
			if enc {
				blk.Encrypt(out[i:], data[i:i+aes.BlockSize])
			} else {
				blk.Decrypt(out[i:], data[i:i+aes.BlockSize])
			}
		}
	case "ctr":
		cipher.NewCTR(blk, iv).XORKeyStream(out, data)
	case "ofb":
		cipher.NewOFB(blk, iv).XORKeyStream(out, data)
	case "cfb":
		if enc {
			cipher.NewCFBEncrypter(blk, iv).XORKeyStream(out, data) //nolint:staticcheck // CryptoJS compatibility
		} else {
			cipher.NewCFBDecrypter(blk, iv).XORKeyStream(out, data) //nolint:staticcheck // CryptoJS compatibility
		}
	default:
		return nil, fmt.Errorf("aes: unsupported mode %q", mode)
	}
	if !enc && pad == "pkcs7" {
		if out, err = unpadPKCS7(out); err != nil {
			return nil, err
		}
	}
	return hex.EncodeToString(out), nil
}

// fnGCM(op, keyHex, ivHex, dataHex, aadHex) -> hex (ciphertext||tag on enc).
func (h *host) fnGCM(a []any) (any, error) {
	key, err := decodeHex(argStr(a, 1), 64)
	if err != nil {
		return nil, err
	}
	iv, err := decodeHex(argStr(a, 2), 64)
	if err != nil {
		return nil, err
	}
	data, err := decodeHex(argStr(a, 3), h.maxBytes())
	if err != nil {
		return nil, err
	}
	aad, err := decodeHex(argStr(a, 4), h.maxBytes())
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	g, err := cipher.NewGCMWithNonceSize(blk, len(iv))
	if err != nil {
		return nil, fmt.Errorf("aes-gcm: %w", err)
	}
	if argStr(a, 0) == "enc" {
		return hex.EncodeToString(g.Seal(nil, iv, data, aad)), nil
	}
	pt, err := g.Open(nil, iv, data, aad)
	if err != nil {
		return nil, errors.New("aes-gcm: authentication failed")
	}
	return hex.EncodeToString(pt), nil
}

// fnPBKDF2(alg, passHex, saltHex, iterations, keyLenBytes) -> hex.
func (h *host) fnPBKDF2(a []any) (any, error) {
	f, err := hasher(argStr(a, 0))
	if err != nil {
		return nil, err
	}
	pass, err := decodeHex(argStr(a, 1), h.maxBytes())
	if err != nil {
		return nil, err
	}
	salt, err := decodeHex(argStr(a, 2), h.maxBytes())
	if err != nil {
		return nil, err
	}
	iter, n := argInt(a, 3), argInt(a, 4)
	if iter < 1 || iter > maxPBKDF2Iter || n < 1 || n > 1024 {
		return nil, fmt.Errorf("pbkdf2: iterations must be 1..%d and key length 1..1024", maxPBKDF2Iter)
	}
	k, err := pbkdf2.Key(f, string(pass), salt, iter, n)
	if err != nil {
		return nil, err
	}
	return hex.EncodeToString(k), nil
}

const maxPBKDF2Iter = 200000

func (h *host) randBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(h.rnd() * 256)
	}
	return b
}

func (h *host) fnRandHex(a []any) (any, error) {
	n := argInt(a, 0)
	if n < 0 || n > h.maxBytes() {
		return nil, errors.New("random size exceeds sandbox limit")
	}
	return hex.EncodeToString(h.randBytes(n)), nil
}

func (h *host) fnUUID([]any) (any, error) {
	b := h.randBytes(16)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	s := hex.EncodeToString(b)
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}

func (h *host) fnConsole(a []any) (any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conCut {
		return nil, nil
	}
	text := argStr(a, 1)
	max := h.in.Limits.withDefaults(h.in.Caps).ConsoleMax
	if h.conLen+len(text) > max {
		text = text[:max-h.conLen]
		h.conCut = true
	}
	h.conLen += len(text)
	h.con = append(h.con, scriptctx.ConsoleLine{Level: argStr(a, 0), Text: text})
	return nil, nil
}

func (h *host) fnScope(a []any) (any, error) {
	if h.in.ScopeCheck == nil {
		return "", nil
	}
	if err := h.in.ScopeCheck(argStr(a, 0)); err != nil {
		return err.Error(), nil
	}
	return "", nil
}

type sendWire struct {
	Method  string             `json:"method"`
	URL     string             `json:"url"`
	Headers []scriptctx.Header `json:"headers"`
	Body    scriptctx.Body     `json:"body"`
}

type sendReply struct {
	Error   string             `json:"error,omitempty"`
	Code    int                `json:"code,omitempty"`
	Status  string             `json:"status,omitempty"`
	Headers []scriptctx.Header `json:"headers,omitempty"`
	Body    string             `json:"body,omitempty"`
	TimeMs  float64            `json:"timeMs,omitempty"`
	Size    int                `json:"size,omitempty"`
}

func reply(r sendReply) (any, error) {
	b, _ := json.Marshal(r)
	return string(b), nil
}

// fnSend implements pm.sendRequest. Every refusal is returned as an error
// reply (the script sees a rejected promise / callback error), never a panic.
func (h *host) fnSend(a []any) (any, error) {
	lim := h.in.Limits.withDefaults(h.in.Caps)
	var w sendWire
	if err := json.Unmarshal([]byte(argStr(a, 0)), &w); err != nil {
		return reply(sendReply{Error: "pm.sendRequest: bad request"})
	}
	rec := scriptctx.SendRecord{Method: w.Method, URL: w.URL}
	finish := func(r sendReply) (any, error) {
		rec.Code, rec.Error = r.Code, r.Error
		h.mu.Lock()
		h.sends = append(h.sends, rec)
		h.mu.Unlock()
		return reply(r)
	}
	if !h.in.Caps.NetSend {
		return finish(sendReply{Error: "capability denied: pm.sendRequest requires netSend"})
	}
	h.mu.Lock()
	h.nsend++
	over := h.nsend > lim.MaxSends
	h.mu.Unlock()
	if over {
		return finish(sendReply{Error: fmt.Sprintf("pm.sendRequest budget exceeded (%d sends per script)", lim.MaxSends)})
	}
	if h.in.Sender == nil {
		return finish(sendReply{Error: "pm.sendRequest: no sender available"})
	}
	if h.in.ScopeCheck != nil {
		if err := h.in.ScopeCheck(w.URL); err != nil {
			return finish(sendReply{Error: "out of scope: " + err.Error()})
		}
	}
	resp, err := h.in.Sender.Send(h.ctx, scriptctx.SendRequest{Method: w.Method, URL: w.URL, Headers: w.Headers, Body: w.Body})
	if err != nil {
		return finish(sendReply{Error: err.Error()})
	}
	body := resp.Body
	if len(body) > lim.MaxResponse {
		body = body[:lim.MaxResponse]
	}
	return finish(sendReply{
		Code: resp.Code, Status: resp.Status, Headers: resp.Headers, Body: string(body),
		TimeMs: float64(resp.ResponseTime.Microseconds()) / 1000, Size: len(resp.Body),
	})
}
