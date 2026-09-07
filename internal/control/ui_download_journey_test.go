package control

import (
	"os/exec"
	"strings"
	"testing"
)

func TestUIDownloadDoesNotOpenNativeSavePicker(t *testing.T) {
	src := readUIAsset(t, "js/core.js")
	start := strings.Index(src, "export async function saveFile(")
	if start < 0 {
		t.Fatal("download helper missing")
	}
	end := strings.Index(src[start:], "const MAX_LIST_FILE")
	if end < 0 {
		t.Fatal("download helper boundary missing")
	}
	script := `const window={get showSaveFilePicker(){throw new Error('native Save picker accessed')}};
let downloaded,revoked,received;const anchor={click(){downloaded={name:this.download,href:this.href}}};
const document={createElement:()=>anchor};
const URL={createObjectURL:blob=>{received=blob;return 'blob:fixture'},revokeObjectURL:url=>revoked=url};
` + strings.TrimPrefix(src[start:start+end], "export ") + `
(async()=>{
const blob=new Blob(['generic report'],{type:'text/plain'});
const name=await saveFile(blob,'report.txt');
if(name!=='report.txt'||downloaded?.name!=='report.txt'||downloaded?.href!=='blob:fixture'||received!==blob||revoked!=='blob:fixture')throw new Error('download content, filename, or resource cleanup lost');
})().catch(e=>{console.error(e);process.exitCode=1});`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("app download: %v\n%s", err, out)
	}
}
