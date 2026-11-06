package pmsandbox

import (
	"testing"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

// pm.* header key casing follows postman-collection: header lists are
// case-insensitive, so toObject() lowercases keys and keeps duplicates as an
// array, and pm.request.getHeaders() preserves case by default and lowercases
// with ignoreCase. The legacy request/responseHeaders globals stay
// case-preserving (the pm corpus pins request.headers["Content-Type"]).
func TestHeaderObjectKeyCaseMatchesPostman(t *testing.T) {
	in := base(scriptctx.PhaseTest, `
const ro = pm.response.headers.toObject();
pm.test("response headers lowercase", () => {
  pm.expect(ro["content-type"]).to.equal("application/json");
  pm.expect(ro["Content-Type"]).to.be.undefined;
});
pm.test("duplicate headers array", () => pm.expect(ro["set-cookie"]).to.eql(["a=1", "b=2"]));
pm.test("getHeaders keeps case by default", () => pm.expect(pm.request.getHeaders()["Content-Type"]).to.equal("application/json"));
pm.test("getHeaders ignoreCase lowercases", () => pm.expect(pm.request.getHeaders({ignoreCase: true})["content-type"]).to.equal("application/json"));
pm.test("getHeaders enabled:true omits disabled", () => pm.expect(pm.request.getHeaders({enabled: true})["X-Disabled"]).to.be.undefined);
pm.test("legacy request headers keep case", () => pm.expect(request.headers["Content-Type"]).to.equal("application/json"));
pm.test("legacy response headers keep case", () => pm.expect(responseHeaders["Content-Type"]).to.equal("application/json"));
`)
	in.Request.Headers = append(in.Request.Headers, scriptctx.Header{Key: "X-Disabled", Value: "yes", Disabled: true})
	in.Response = &scriptctx.Response{
		Code: 200, Status: "OK",
		Headers: []scriptctx.Header{
			{Key: "Content-Type", Value: "application/json"},
			{Key: "Set-Cookie", Value: "a=1"},
			{Key: "Set-Cookie", Value: "b=2"},
		},
		Body: []byte("{}"),
	}
	mustPass(t, run(t, in))
}
