package app

import (
	"path/filepath"
	"testing"
)

func TestPickerUsagePersistsNamespacesByContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "tailg", "picker-usage.json")
	u := loadPickerUsageAt(path)
	u.record("tkgs-dev", "apollo")
	u.record("tkgs-dev", "apollo")
	u.record("tkgs-qa", "lrjob")
	reloaded := loadPickerUsageAt(path)
	if got := reloaded.Contexts["tkgs-dev"].Count; got != 2 {
		t.Fatalf("dev uses = %d, want 2", got)
	}
	if got := reloaded.Namespaces["tkgs-dev"]["apollo"].Count; got != 2 {
		t.Fatalf("dev/apollo uses = %d, want 2", got)
	}
	if got := reloaded.Namespaces["tkgs-qa"]["lrjob"].Count; got != 1 {
		t.Fatalf("qa/lrjob uses = %d, want 1", got)
	}
	if reloaded.Namespaces["tkgs-qa"]["apollo"].Count != 0 {
		t.Fatal("namespace usage leaked between contexts")
	}
}
