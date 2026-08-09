package control

import (
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func putTestBody(t *testing.T, st *store.Store, body []byte) string {
	t.Helper()
	writer, err := st.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	hash, _, err := writer.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	return hash
}
