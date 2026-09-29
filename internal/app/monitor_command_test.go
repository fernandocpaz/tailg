package app

import (
	"context"
	"strings"
	"testing"
)

func TestMonitorRequiresExplicitStateAndScope(t *testing.T) {
	var output strings.Builder
	command := NewCommand(context.Background(), strings.NewReader(""), &output, &output)
	command.SetArgs([]string{"monitor", "--once"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "--context, --namespace, and --state") {
		t.Fatalf("monitor missing scope error = %v", err)
	}
}
