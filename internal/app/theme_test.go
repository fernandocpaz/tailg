package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestInvalidThemeRejectedBeforeKubectl(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(context.Background(), Options{Theme: "invalid"}, &bytes.Buffer{}, &bytes.Buffer{}, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--theme must") {
		t.Fatalf("code=%d error=%q", code, stderr.String())
	}
}

func TestChildPanesInheritTheme(t *testing.T) {
	args := childArgs(Options{Theme: "light"}, "apollo")("example-pod")
	for i, arg := range args {
		if arg == "--theme" && i+1 < len(args) && args[i+1] == "light" {
			return
		}
	}
	t.Fatalf("theme missing from child: %v", args)
}
