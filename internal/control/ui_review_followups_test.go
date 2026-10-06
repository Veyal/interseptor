package control

import "testing"

func TestUIInspectorFilterSignatureIncludesSort(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "sort:(state.sort&&state.sort.key)||'',dir:sortDirParam(),")
}

func TestUITagAndScopeMutationFailuresUseToastError(t *testing.T) {
	tags := executableJS(readUIAsset(t, "js/tags.js"))
	requireUIContains(t, tags,
		"import { $, esc, escAttr, api, state, toast, toastError,",
		"toastError('Tag colour not saved',e)",
		"toastError('Tagging failed',e)",
	)
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, proxy,
		"toastError('Scope rule not saved',e)",
		"toastError('Scope rule not deleted',e)",
	)
}
