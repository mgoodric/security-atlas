package apikeystore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// #2384: an issue or rotate audit row names the key by its label, and an
// unlabelled key's row carries no label field.
func TestKeyAuditOf_Label(t *testing.T) {
	t.Parallel()
	labelled, err := json.Marshal(keyAuditOf(credstore.Credential{ID: "key_x", Label: "CI deploy"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(labelled), `"label":"CI deploy"`) {
		t.Errorf("labelled key audit = %s, want label", labelled)
	}
	plain, err := json.Marshal(keyAuditOf(credstore.Credential{ID: "key_y"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), `"label"`) {
		t.Errorf("unlabelled key audit = %s, want no label", plain)
	}
}
