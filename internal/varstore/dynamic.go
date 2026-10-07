package varstore

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	mrand "math/rand/v2"
	"strings"
	"sync"
	"time"
)

// Rand is a goroutine-safe deterministic random source. Seed it for
// reproducible runs (--seed).
type Rand struct {
	mu sync.Mutex
	r  *mrand.Rand
}

// NewRand returns a source that yields the same sequence for the same seed.
func NewRand(seed uint64) *Rand {
	return &Rand{r: mrand.New(mrand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
}

// NewRandomRand seeds from the OS entropy pool.
func NewRandomRand() *Rand {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return NewRand(binary.LittleEndian.Uint64(b[:]))
}

// Intn returns a value in [0, n).
func (r *Rand) Intn(n int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.r.IntN(n)
}

func (r *Rand) pick(list []string) string { return list[r.Intn(len(list))] }

func (r *Rand) str(alphabet string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(alphabet[r.Intn(len(alphabet))])
	}
	return b.String()
}

const (
	alnum    = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	lowerHex = "0123456789abcdef"
)

var (
	firstNames = []string{"Alex", "Sam", "Jordan", "Taylor", "Morgan", "Casey", "Riley", "Jamie", "Robin", "Avery"}
	lastNames  = []string{"Smith", "Jones", "Brown", "Miller", "Davis", "Wilson", "Moore", "Clark", "Lewis", "Walker"}
	words      = []string{"alpha", "bravo", "delta", "echo", "foxtrot", "gamma", "kilo", "lima", "sierra", "zulu"}
)

func (r *Rand) uuid() string {
	var b [16]byte
	for i := range b {
		b[i] = byte(r.Intn(256))
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// payloads are fixed, benign probe strings for {{$payload:kind}}.
var payloads = map[string]string{
	"xss":  `"><svg onload=alert(1)>`,
	"sqli": `' OR '1'='1' -- `,
	"ssti": `{{7*7}}`,
}

// dynamic evaluates a $-prefixed variable. ok is false for unknown names.
func dynamic(name string, now time.Time, r *Rand) (string, bool) {
	if kind, found := strings.CutPrefix(name, "$payload:"); found {
		v, ok := payloads[kind]
		return v, ok
	}
	switch name {
	case "$guid", "$randomUUID":
		return r.uuid(), true
	case "$timestamp":
		return fmt.Sprint(now.Unix()), true
	case "$isoTimestamp":
		return now.UTC().Format("2006-01-02T15:04:05.000Z"), true
	case "$randomInt":
		return fmt.Sprint(r.Intn(1001)), true
	case "$randomAlphaNumeric":
		return r.str(alnum, 1), true
	case "$randomBoolean":
		return fmt.Sprint(r.Intn(2) == 1), true
	case "$randomFirstName":
		return r.pick(firstNames), true
	case "$randomLastName":
		return r.pick(lastNames), true
	case "$randomFullName":
		return r.pick(firstNames) + " " + r.pick(lastNames), true
	case "$randomEmail":
		return strings.ToLower(r.pick(firstNames)+"."+r.pick(lastNames)) + fmt.Sprint(r.Intn(1000)) + "@example.com", true
	case "$randomUserName":
		return strings.ToLower(r.pick(firstNames)) + r.str(lowerHex, 4), true
	case "$randomIP":
		return fmt.Sprintf("%d.%d.%d.%d", 1+r.Intn(254), r.Intn(256), r.Intn(256), 1+r.Intn(254)), true
	case "$randomPassword":
		return r.str(alnum, 15), true
	case "$randomHexColor":
		return "#" + r.str(lowerHex, 6), true
	case "$randomWord":
		return r.pick(words), true
	case "$randomPhoneNumber":
		return fmt.Sprintf("555-%03d-%04d", r.Intn(1000), r.Intn(10000)), true
	}
	return "", false
}
