package app

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// These variables can be populated by release builds with -ldflags. Go also
// supplies the same information in runtime/debug.BuildInfo for ordinary
// builds made from a version-controlled checkout and for go install.
var (
	Version   = "dev"
	Commit    string
	BuildTime string
)

type buildMetadata struct {
	version    string
	commit     string
	dirty      bool
	buildTime  string
	commitTime string
}

var pseudoVersionPattern = regexp.MustCompile(`[-.]([0-9]{14})-([0-9a-fA-F]{7,40})(?:\+incompatible)?$`)

// buildMetadataFromInfo extracts reproducible metadata from Go's build info.
// Keeping this separate from BuildDescription makes formatting tests
// independent of the binary in which they run.
func buildMetadataFromInfo(info *debug.BuildInfo) buildMetadata {
	metadata := buildMetadata{}
	if info == nil {
		return metadata
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		metadata.version = info.Main.Version
		if match := pseudoVersionPattern.FindStringSubmatch(info.Main.Version); len(match) == 3 {
			metadata.commitTime = match[1]
			metadata.commit = match[2]
		}
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			metadata.commit = strings.TrimSpace(setting.Value)
		case "vcs.time":
			metadata.commitTime = strings.TrimSpace(setting.Value)
		case "vcs.modified":
			metadata.dirty = strings.EqualFold(strings.TrimSpace(setting.Value), "true")
		}
	}
	return metadata
}

func currentBuildMetadata() buildMetadata {
	metadata := buildMetadata{version: Version, commit: Commit, buildTime: BuildTime}
	if info, ok := debug.ReadBuildInfo(); ok {
		fromInfo := buildMetadataFromInfo(info)
		if metadata.version == "" || metadata.version == "dev" {
			metadata.version = fromInfo.version
		}
		if metadata.commit == "" {
			metadata.commit = fromInfo.commit
		}
		if metadata.commitTime == "" {
			// vcs.time is the source commit time. Release builds provide the
			// actual build time through the BuildTime linker variable.
			metadata.commitTime = fromInfo.commitTime
		}
		metadata.dirty = fromInfo.dirty
	}
	if metadata.version == "" || metadata.version == "(devel)" {
		metadata.version = "dev"
	}
	return metadata
}

// BuildDescription returns the user-facing version and build metadata.
func BuildDescription() string {
	return formatBuildDescription(currentBuildMetadata())
}

func formatBuildDescription(metadata buildMetadata) string {
	version := metadata.version
	if version == "" || version == "(devel)" {
		version = "dev"
	}
	parts := []string{version}
	if metadata.commit != "" {
		commit := metadata.commit
		if len(commit) > 12 {
			commit = commit[:12]
		}
		if metadata.dirty {
			commit += "-dirty"
		}
		parts = append(parts, "commit "+commit)
	} else if metadata.dirty {
		parts = append(parts, "dirty")
	}
	if metadata.buildTime != "" {
		parts = append(parts, "built "+metadata.buildTime)
	}
	if metadata.commitTime != "" && metadata.buildTime == "" {
		parts = append(parts, "committed "+metadata.commitTime)
	}
	return strings.Join(parts, " ")
}
