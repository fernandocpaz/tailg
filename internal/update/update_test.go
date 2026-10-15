package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if version := os.Getenv("TAILG_TEST_BINARY_VERSION"); version != "" {
		fmt.Println(version)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestNewerStableTagsAndPseudoVersions(t *testing.T) {
	for _, test := range []struct {
		current, latest string
		newer           bool
	}{
		{"v0.1.9", "v0.1.10", true}, {"v1.0.0", "v0.99.99", false}, {"v1.2.3", "v1.2.3", false},
		{"v1.2.3-rc.1", "v1.2.3", true}, {"v1.2.4-0.20261015120000-abcdef012345", "v1.2.3", false},
		{"v1.2.4-0.20261015120000-abcdef012345", "v1.2.4", true}, {"v1.2.3+custom", "v1.2.4", true},
		{"dev", "v1.0.0", false}, {"v01.2.3", "v1.2.4", false}, {"v1.2.3", "v1.3.0-rc.1", false},
		{"v1.2.3", "../bad", false}, {"v99999999999999999999999.0.0", "v1.0.0", false},
	} {
		if got := Newer(test.current, test.latest); got != test.newer {
			t.Fatalf("%s → %s = %t", test.current, test.latest, got)
		}
	}
}

func releaseFixture(t *testing.T, body []byte, status int) (*Client, Release, *atomic.Int32) {
	t.Helper()
	name := fmt.Sprintf("tailg_v1.2.4_%s_%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	hash := sha256.Sum256(body)
	release := Release{Version: "v1.2.4", Assets: []Asset{{Name: name, Size: int64(len(body)), Digest: "sha256:" + hex.EncodeToString(hash[:])}}}
	count := &atomic.Int32{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			count.Add(1)
			if status != 200 {
				w.WriteHeader(status)
				return
			}
			_ = json.NewEncoder(w).Encode(release)
			return
		}
		if r.URL.Path != "/download/v1.2.4/"+name {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	client := New("v1.2.3")
	client.http, client.apiURL, client.assetBase = server.Client(), server.URL+"/latest", server.URL+"/download"
	return client, release, count
}

func TestOfferChecksOnceAndLaterIsSessionScoped(t *testing.T) {
	client, _, count := releaseFixture(t, []byte("binary"), 200)
	for range 2 {
		if _, offer := client.Offer(context.Background()); !offer {
			t.Fatal("new release not offered")
		}
	}
	client.Dismiss()
	if _, offer := client.Offer(context.Background()); offer {
		t.Fatal("Later re-prompted in the same session")
	}
	if count.Load() != 1 {
		t.Fatal("startup checked repeatedly")
	}
	client, _, _ = releaseFixture(t, []byte("binary"), 403)
	if _, offer := client.Offer(context.Background()); offer {
		t.Fatal("rate limit created an update prompt")
	}
}

func TestSessionCheckSurvivesPickerCancellationAndRequestsRespectTimeout(t *testing.T) {
	client, _, _ := releaseFixture(t, []byte("binary"), 200)
	client.session = context.Background()
	picker, cancel := context.WithCancel(context.Background())
	cancel()
	if _, offer := client.Offer(picker); !offer {
		t.Fatal("closing picker canceled session check")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Check(ctx); err == nil {
		t.Fatal("canceled HTTP request succeeded")
	}
}

func TestInstallChecksIntegrityBeforeReplacingAndPreservesMode(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			client, release, _ := releaseFixture(t, []byte("new executable"), 200)
			path := filepath.Join(t.TempDir(), "tailg")
			if err := os.WriteFile(path, []byte("old executable"), 0755); err != nil {
				t.Fatal(err)
			}
			client.executable = func() (string, error) { return path, nil }
			client.validate = func(_ context.Context, file, version string) error {
				data, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				if file == path && version == "v1.2.3" && string(data) == "old executable" {
					return nil
				}
				if file != path && version == "v1.2.4" && string(data) == "new executable" {
					return nil
				}
				return fmt.Errorf("wrong staged binary")
			}
			if corrupt {
				release.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64)
			}
			err := client.Install(context.Background(), release)
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if corrupt {
				if err == nil || string(data) != "old executable" {
					t.Fatal("corrupt binary replaced the original")
				}
			} else {
				if err != nil || string(data) != "new executable" {
					t.Fatalf("install failed: %v", err)
				}
				info, _ := os.Stat(path)
				if runtime.GOOS != "windows" && info.Mode().Perm() != 0755 {
					t.Fatal("executable mode lost")
				}
			}
			if _, err := os.Stat(path + ".update-lock"); !os.IsNotExist(err) {
				t.Fatal("update lock leaked")
			}
		})
	}
}

func TestUnsupportedOrUnverifiedAssetCannotInstall(t *testing.T) {
	client, release, _ := releaseFixture(t, []byte("binary"), 200)
	for _, assets := range [][]Asset{nil, {{Name: release.Assets[0].Name, Size: 6}}, {{Name: release.Assets[0].Name, Size: maxBinarySize + 1, Digest: release.Assets[0].Digest}}} {
		candidate := release
		candidate.Assets = assets
		if _, err := client.asset(candidate); err == nil {
			t.Fatal("invalid asset accepted")
		}
	}
}

func TestReplacementRestoresOriginalOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tailg")
	if err := os.WriteFile(path, []byte("original"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(path, path+".missing"); err == nil {
		t.Fatal("missing staged file accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatal("failed replacement did not restore original")
	}
}

// Runs natively on Windows in CI as well as Unix: the image being renamed is
// actually executing, rather than a stand-in text file.
func TestReplaceRunningExecutable(t *testing.T) {
	if os.Getenv("TAILG_TEST_REPLACE_SELF") == "1" {
		path, _ := os.Executable()
		if err := replaceExecutable(path, path+".new"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tailg-test.exe")
	for _, name := range []string{path, path + ".new"} {
		if err := os.WriteFile(name, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "-test.run=^TestReplaceRunningExecutable$")
	command.Env = append(os.Environ(), "TAILG_TEST_REPLACE_SELF=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("running executable could not update: %v\n%s", err, output)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("new binary missing")
	}
}

func TestDownloadedBinaryMustReportExpectedVersion(t *testing.T) {
	path, _ := os.Executable()
	t.Setenv("TAILG_TEST_BINARY_VERSION", "v1.2.4 commit abc")
	if err := validateBinary(context.Background(), path, "v1.2.4"); err != nil {
		t.Fatal(err)
	}
	if err := validateBinary(context.Background(), path, "v1.2.5"); err == nil {
		t.Fatal("wrong executable version accepted")
	}
}
