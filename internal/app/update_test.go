package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateChecksSkipNonInteractiveAndMachineCommands(t *testing.T) {
	if startupUpdates(context.Background(), Options{LiveFilter: true}, &bytes.Buffer{}, &bytes.Buffer{}) != nil {
		t.Fatal("noninteractive invocation enabled startup check")
	}
	var output bytes.Buffer
	for _, args := range [][]string{{"version"}, {"--version"}, {"update", "--help"}, {"--no-update-check", "version"}} {
		command := NewCommand(context.Background(), &bytes.Buffer{}, &output, &output)
		command.SetArgs(args)
		if err := command.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if !strings.Contains(strings.Join(childArgs(Options{}, "apollo")("api-1"), " "), "--no-update-check") {
		t.Fatal("child panes can independently prompt for updates")
	}
}

func TestRestartUsesProvidedExecutableAndPreservesArguments(t *testing.T) {
	mode := os.Getenv("TAILG_TEST_RESTART_MODE")
	if mode == "second" {
		if err := os.WriteFile(os.Getenv("TAILG_TEST_RESTART_OUTPUT"), []byte(strings.Join(os.Args[1:], "\n")), 0600); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	if mode == "first" {
		os.Setenv("TAILG_TEST_RESTART_MODE", "second")
		path, _ := os.Executable()
		if err := Restart(path, []string{"-test.run=^TestRestartUsesProvidedExecutableAndPreservesArguments$", "--", "two words", "pod/api", "--context", "qa"}); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	file := filepath.Join(t.TempDir(), "restart.txt")
	path, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "-test.run=^TestRestartUsesProvidedExecutableAndPreservesArguments$")
	command.Env = append(os.Environ(), "TAILG_TEST_RESTART_MODE=first", "TAILG_TEST_RESTART_OUTPUT="+file)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("restart failed: %v %s", err, output)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(file); err == nil {
			if !strings.Contains(string(data), "two words\npod/api\n--context\nqa") {
				t.Fatalf("arguments changed: %s", data)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("restarted process did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
