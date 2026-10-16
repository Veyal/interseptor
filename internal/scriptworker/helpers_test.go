package scriptworker

import (
	"bufio"
	"bytes"
	"encoding/json"
)

type bytesBuf = bytes.Buffer

func bufioReader(b *bytes.Buffer) *bufio.Reader { return bufio.NewReader(b) }
func jsonUnmarshal(p []byte, v any) error       { return json.Unmarshal(p, v) }
