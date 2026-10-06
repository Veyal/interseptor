package control

import "testing"

func TestUIInspectorFilterSignatureIncludesSort(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "sort:(state.sort&&state.sort.key)||'',dir:sortDirParam(),")
}
