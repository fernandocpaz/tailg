// Package update checks and installs the published tailg release binaries.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxBinarySize = 128 << 20

type Release struct {
	Version    string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

type Asset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type Client struct {
	Version                         string
	http                            *http.Client
	apiURL, assetBase, goos, goarch string
	executable                      func() (string, error)
	validate                        func(context.Context, string, string) error
	once                            sync.Once
	release                         Release
	checkErr                        error
	mu                              sync.Mutex
	dismissed                       bool
	session                         context.Context
}

func New(version string) *Client {
	return &Client{Version: version, apiURL: "https://api.github.com/repos/fernandocpaz/tailg/releases/latest",
		assetBase: "https://github.com/fernandocpaz/tailg/releases/download", goos: runtime.GOOS, goarch: runtime.GOARCH,
		executable: os.Executable, validate: validateBinary,
		http: &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 || req.URL.Scheme != "https" {
				return fmt.Errorf("unsafe release redirect")
			}
			return nil
		}},
	}
}

var stableVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:([-+])([0-9A-Za-z.-]+))?$`)

func NewSession(ctx context.Context, version string) *Client {
	c := New(version)
	c.session = ctx
	return c
}

func KnownVersion(version string) bool { _, _, known := versionParts(version); return known }

func versionParts(version string) ([3]uint64, bool, bool) {
	var result [3]uint64
	match := stableVersion.FindStringSubmatch(version)
	if match == nil {
		return result, false, false
	}
	for i := range result {
		n, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return result, false, false
		}
		result[i] = n
	}
	return result, match[4] == "-", true
}

// Newer compares a stable release with tagged, prerelease or pseudo versions.
// Untagged development builds are never silently replaced by an older release.
func Newer(current, candidate string) bool {
	left, pre, known := versionParts(current)
	right, candidatePre, valid := versionParts(candidate)
	if !known || !valid || candidatePre {
		return false
	}
	for i := range left {
		if right[i] != left[i] {
			return right[i] > left[i]
		}
	}
	return pre
}

func (c *Client) Dismiss() { c.mu.Lock(); c.dismissed = true; c.mu.Unlock() }

// Offer performs at most one short check per session and never re-prompts
// after Later, including when the picker is reopened after viewing logs.
func (c *Client) Offer(ctx context.Context) (Release, bool) {
	if c == nil {
		return Release{}, false
	}
	if _, _, known := versionParts(c.Version); !known {
		return Release{}, false
	}
	c.once.Do(func() {
		if c.session != nil {
			ctx = c.session
		}
		limited, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		c.release, c.checkErr = c.Check(limited)
	})
	c.mu.Lock()
	dismissed := c.dismissed
	c.mu.Unlock()
	return c.release, !dismissed && c.checkErr == nil && Newer(c.Version, c.release.Version)
}

func (c *Client) request(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tailg/"+c.Version)
	req.Header.Set("Accept", "application/octet-stream")
	if url == c.apiURL {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("release server returned HTTP %d", response.StatusCode)
	}
	return response, nil
}

func (c *Client) Check(ctx context.Context) (Release, error) {
	response, err := c.request(ctx, c.apiURL)
	if err != nil {
		return Release{}, err
	}
	defer response.Body.Close()
	var release Release
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release); err != nil {
		return Release{}, fmt.Errorf("invalid release metadata: %w", err)
	}
	_, pre, valid := versionParts(release.Version)
	if !valid || pre || release.Draft || release.Prerelease {
		return Release{}, fmt.Errorf("latest release is not a stable version")
	}
	return release, nil
}

func (c *Client) asset(release Release) (Asset, error) {
	name := fmt.Sprintf("tailg_%s_%s_%s", release.Version, c.goos, c.goarch)
	if c.goos == "windows" {
		name += ".exe"
	}
	for _, asset := range release.Assets {
		if asset.Name != name {
			continue
		}
		digest, err := hex.DecodeString(strings.TrimPrefix(asset.Digest, "sha256:"))
		if !strings.HasPrefix(asset.Digest, "sha256:") || err != nil || len(digest) != sha256.Size || asset.Size <= 0 || asset.Size > maxBinarySize {
			return Asset{}, fmt.Errorf("release binary has no valid SHA-256 digest or size")
		}
		return asset, nil
	}
	return Asset{}, fmt.Errorf("no release binary for %s/%s", c.goos, c.goarch)
}

// Install stages the verified download beside the running executable. The
// original is renamed only after checksum and executable-version validation.
func (c *Client) Install(ctx context.Context, release Release) error {
	limited, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	ctx = limited
	_, pre, valid := versionParts(release.Version)
	if !valid || pre || release.Draft || release.Prerelease {
		return fmt.Errorf("refusing an invalid release")
	}
	asset, err := c.asset(release)
	if err != nil {
		return err
	}
	path, err := c.executable()
	if err != nil {
		return err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	original, err := os.Stat(path)
	if err != nil || !original.Mode().IsRegular() {
		return fmt.Errorf("cannot inspect installed executable: %v", err)
	}
	lockPath := path + ".update-lock"
	if info, err := os.Lstat(lockPath); err == nil && info.Mode().IsRegular() && time.Since(info.ModTime()) > 10*time.Minute {
		_ = os.Remove(lockPath)
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("cannot lock %s for update (another update may be running): %w", path, err)
	}
	lock.Close()
	defer os.Remove(lockPath)
	if _, _, known := versionParts(c.Version); known {
		if err := c.validate(ctx, path, c.Version); err != nil {
			return fmt.Errorf("installed copy no longer matches the running version; start tailg again: %w", err)
		}
	}
	extension := ""
	if c.goos == "windows" {
		extension = ".exe"
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".tailg-update-*"+extension)
	if err != nil {
		return err
	}
	staged := file.Name()
	defer os.Remove(staged)
	defer file.Close()
	response, err := c.request(ctx, c.assetBase+"/"+release.Version+"/"+asset.Name)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, asset.Size+1))
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	if size != asset.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(asset.Digest) {
		return fmt.Errorf("release checksum or size does not match; existing executable kept")
	}
	if err := file.Chmod(original.Mode().Perm()); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := c.validate(ctx, staged, release.Version); err != nil {
		return err
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(original, current) {
		return fmt.Errorf("installed executable changed during download; update canceled")
	}
	return replaceExecutable(path, staged)
}

func validateBinary(ctx context.Context, path, version string) error {
	limited, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(limited, path, "version").Output()
	if err != nil {
		return fmt.Errorf("downloaded binary did not run: %w", err)
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 || fields[0] != version {
		return fmt.Errorf("downloaded binary reports the wrong version")
	}
	return nil
}

func replaceExecutable(path, staged string) error {
	backup, err := os.CreateTemp(filepath.Dir(path), ".tailg-old-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := backup.Name()
	if err := backup.Close(); err != nil {
		return err
	}
	if err := os.Remove(name); err != nil {
		return err
	}
	if err := os.Rename(path, name); err != nil {
		return fmt.Errorf("cannot move existing executable: %w", err)
	}
	if err := os.Rename(staged, path); err != nil {
		if restore := os.Rename(name, path); restore != nil {
			return fmt.Errorf("install failed: %v; restore failed: %v; original saved at %s", err, restore, name)
		}
		return fmt.Errorf("install failed; original restored: %w", err)
	}
	// Windows may keep the previous running image locked until restart.
	_ = os.Remove(name)
	return nil
}

// Cleanup removes only updater-owned backup files from prior sessions. A
// one-day grace period keeps concurrent rollback/restart operations untouched.
func (c *Client) Cleanup() {
	path, err := c.executable()
	if err != nil {
		return
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".tailg-old-"+filepath.Base(path)+"-") || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err == nil && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.Remove(filepath.Join(filepath.Dir(path), entry.Name()))
		}
	}
}
