package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

func examineRepositories(workloads []string, namespace string, input io.Reader, output io.Writer) error {
	memoryPath := deploymentMemoryPath()
	mappings := loadMappings(memoryPath)
	scanner := bufio.NewScanner(input)
	for _, workload := range workloads {
		key := namespace + "/" + workload
		remote := mappings[key]
		if remote == "" {
			fmt.Fprintf(output, "Remote Git repository for %s (owner/repo or URL): ", key)
			if !scanner.Scan() {
				return fmt.Errorf("repository input was cancelled")
			}
			candidate := strings.TrimSpace(scanner.Text())
			normalized, err := normalizeRemote(candidate)
			if err != nil {
				return err
			}
			remote = normalized
			mappings[key] = remote
			if err := saveMappings(memoryPath, mappings); err != nil {
				return err
			}
		}
		report, err := recentChanges(remote)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "\nRepository changes for %s\n%s\n", key, report)
	}
	return nil
}

func deploymentMemoryPath() string {
	if custom := os.Getenv("TAILG_REPO_MEMORY_FILE"); custom != "" {
		return custom
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = os.TempDir()
		}
		return filepath.Join(base, "tailg", "deployment-repos.json")
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "tailg", "deployment-repos.json")
}
func loadMappings(path string) map[string]string {
	content, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	result := map[string]string{}
	if json.Unmarshal(content, &result) != nil {
		return map[string]string{}
	}
	return result
}
func saveMappings(path string, mappings map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	content, _ := json.MarshalIndent(mappings, "", "  ")
	return os.WriteFile(path, append(content, '\n'), 0o600)
}

var githubShort = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func normalizeRemote(candidate string) (string, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", fmt.Errorf("remote repository is required")
	}
	if githubShort.MatchString(candidate) {
		return "https://github.com/" + strings.TrimSuffix(candidate, ".git") + ".git", nil
	}
	lower := strings.ToLower(candidate)
	if strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "ssh://") || strings.HasPrefix(lower, "git@") {
		return candidate, nil
	}
	return "", fmt.Errorf("use a remote repository URL or owner/repository; local paths are not accepted")
}
func recentChanges(remote string) (string, error) {
	directory, err := os.MkdirTemp("", "tailg-git-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(directory)
	repo := filepath.Join(directory, "repo")
	clone := exec.Command("git", "clone", "--filter=blob:none", "--no-checkout", "--depth", "6", remote, repo)
	if output, err := clone.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git clone: %s", strings.TrimSpace(string(output)))
	}
	log := exec.Command("git", "-C", repo, "log", "-5", "--date=short", "--pretty=format:%h %ad %an %s")
	logOutput, err := log.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git log: %s", strings.TrimSpace(string(logOutput)))
	}
	files := exec.Command("git", "-C", repo, "show", "--stat", "--oneline", "--no-renames", "HEAD")
	filesOutput, err := files.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git show: %s", strings.TrimSpace(string(filesOutput)))
	}
	return fmt.Sprintf("Remote: %s\n\nRecent commits:\n%s\n\nLatest changes:\n%s", remote, logOutput, filesOutput), nil
}
