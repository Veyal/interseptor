package pmsandbox

import (
	"testing"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

// Body property lists (urlencoded/formdata) are not header lists. In
// postman-collection PropertyList.toObject() preserves the key case and keeps
// one value per key, while HeaderList.toObject() lowercases keys and turns
// duplicate names into an array. The sandbox builds every one of those lists
// from its internal Header item type, so keying header behaviour off the item
// type silently lowercased urlencoded/formdata keys too.
func TestBodyPropertyListsKeepKeyCase(t *testing.T) {
	in := base(scriptctx.PhasePreRequest, `
pm.test("urlencoded keys keep case", () => {
  const o = pm.request.body.urlencoded.toObject();
  pm.expect(o["UserName"]).to.equal("a");
  pm.expect(o["username"]).to.be.undefined;
});
pm.test("formdata keys keep case", () => {
  const o = pm.request.body.formdata.toObject();
  pm.expect(o["SessionId"]).to.equal("s");
  pm.expect(o["sessionid"]).to.be.undefined;
});
pm.test("urlencoded duplicate key keeps one value", () => {
  const o = pm.request.body.urlencoded.toObject();
  pm.expect(o["tag"]).to.equal("b");
});
`)
	in.Request.Body = scriptctx.Body{
		Mode:       "urlencoded",
		URLEncoded: []scriptctx.Param{{Key: "UserName", Value: "a"}, {Key: "tag", Value: "a"}, {Key: "tag", Value: "b"}},
		FormData:   []scriptctx.Param{{Key: "SessionId", Value: "s"}},
	}
	mustPass(t, run(t, in))
}
