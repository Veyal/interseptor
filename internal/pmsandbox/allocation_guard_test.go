package pmsandbox

import (
	"testing"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

// Buffer is a global a script may construct directly. Its class was defined
// with `extends Uint8Array` before the length guards wrapped the global, so
// `new Buffer(n)` called the raw typed-array constructor and allocated past
// MaxAlloc — while `Buffer.alloc(n)` was rejected. Buffer is the documented
// path, so its constructor must enforce the same cap.
func TestBufferConstructorEnforcesMaxAlloc(t *testing.T) {
	in := base(scriptctx.PhaseTest, `
let big = "no error";
try { new Buffer(64 * 1024 * 1024); } catch (e) { big = e.message; }
pm.test("constructor is capped", () => {
  pm.expect(big).to.not.equal("no error");
  pm.expect(big).to.include("sandbox limit");
});
pm.test("ordinary construction still works", () => {
  pm.expect(new Buffer(4).length).to.equal(4);
  pm.expect(new Buffer(new Uint8Array([1, 2])).length).to.equal(2);
  pm.expect(Buffer.from("hi").toString()).to.equal("hi");
  pm.expect(Buffer.alloc(3).length).to.equal(3);
  pm.expect(Buffer.concat([Buffer.from("a"), Buffer.from("b")]).toString()).to.equal("ab");
});
`)
	in.Caps = scriptctx.Caps{VarsRead: true, VarsWrite: true}
	in.Limits.Timeout = 10e9
	in.Limits.HeapGrowth = 1 << 40
	mustPass(t, run(t, in))
}
