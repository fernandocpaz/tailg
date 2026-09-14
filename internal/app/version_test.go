package app

import (
	"bytes"
	"context"
	"runtime/debug"
	"strings"
	"testing"
)

func TestBuildMetadataFromInfo(t *testing.T) {
	for _, version := range []string{
		"v0.0.0-20260910120000-abcdef012345",
		"v0.4.0-0.20260910120000-abcdef012345",
		"v0.4.0-rc.1.0.20260910120000-abcdef012345",
	} {
		info := &debug.BuildInfo{Main: debug.Module{Version: version}}
		got := buildMetadataFromInfo(info)
		if got.version != info.Main.Version || got.commit != "abcdef012345" || got.commitTime != "20260910120000" {
			t.Fatalf("%s: unexpected metadata: %+v", version, got)
		}
	}
}

func TestBuildMetadataVCSSettings(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "v0.4.0"}, Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "abcdef0123456789"},
		{Key: "vcs.time", Value: "2026-09-10T12:00:00Z"},
		{Key: "vcs.modified", Value: "true"},
	}}
	got := buildMetadataFromInfo(info)
	if got.commit != "abcdef0123456789" || got.commitTime != "2026-09-10T12:00:00Z" || !got.dirty {
		t.Fatalf("unexpected metadata: %+v", got)
	}
}

func TestFormatBuildDescription(t *testing.T) {
	got := formatBuildDescription(buildMetadata{version: "v1.2.3", commit: "0123456789abcdef", dirty: true, buildTime: "2026-09-10T12:00:00Z"})
	want := "v1.2.3 commit 0123456789ab-dirty built 2026-09-10T12:00:00Z"
	if got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
}

func TestFormatBuildDescriptionCommitTime(t *testing.T) {
	got := formatBuildDescription(buildMetadata{version: "v0.4.0", commit: "abcdef012345", commitTime: "2026-09-10T12:00:00Z"})
	want := "v0.4.0 commit abcdef012345 committed 2026-09-10T12:00:00Z"
	if got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
}

func TestFormatBuildDescriptionOmitsUnavailableFields(t *testing.T) {
	if got, want := formatBuildDescription(buildMetadata{version: "dev"}), "dev"; got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
}

func TestVersionCommandsDoNotRunApplication(t *testing.T) {
	oldVersion, oldCommit, oldBuildTime := Version, Commit, BuildTime
	Version, Commit, BuildTime = "v9.8.7", "0123456789abcdef", "2026-09-10T12:00:00Z"
	defer func() { Version, Commit, BuildTime = oldVersion, oldCommit, oldBuildTime }()

	for _, args := range [][]string{{"--version"}, {"version"}} {
		var output bytes.Buffer
		command := NewCommand(context.Background(), strings.NewReader(""), &output, &output)
		command.SetArgs(args)
		if err := command.Execute(); err != nil {
			t.Fatalf("args %v: execute: %v", args, err)
		}
		if !strings.Contains(output.String(), "v9.8.7") || !strings.Contains(output.String(), "commit 0123456789ab") {
			t.Errorf("args %v: output %q lacks version metadata", args, output.String())
		}
	}
}
