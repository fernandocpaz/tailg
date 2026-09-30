package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

type evidenceFakeClient struct {
	rows map[string][]core.LogEvent
	errs map[string]error
}

func (f evidenceFakeClient) Snapshot(_ context.Context, item core.InventoryItem, options kube.LogOptions) ([]core.LogEvent, error) {
	if options.Tail != -1 {
		return nil, errors.New("evidence must request all retained logs")
	}
	if err := f.errs[item.Key()]; err != nil {
		return nil, err
	}
	return append([]core.LogEvent(nil), f.rows[item.Key()]...), nil
}

func (evidenceFakeClient) JSON(context.Context, ...string) (map[string]any, error) {
	return nil, nil
}

func TestCaptureIssueEvidenceCapturesUncappedExceptionBlockAndRedacts(t *testing.T) {
	when := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	item := core.InventoryItem{Pod: "api-abc", Container: "api"}
	rows := []core.LogEvent{
		{Pod: item.Pod, Container: item.Container, Message: "ERROR database failure password=topsecret", ObservedAt: when},
	}
	for i := 0; i < 80; i++ {
		rows = append(rows, core.LogEvent{Pod: item.Pod, Container: item.Container, Message: "    at service.方法(" + strings.Repeat("x", 12) + ")", ObservedAt: when.Add(time.Duration(i+1) * time.Millisecond)})
	}
	issue, ok := core.ClassifyIssue(rows[0])
	if !ok {
		t.Fatal("test error did not classify")
	}
	client := evidenceFakeClient{rows: map[string][]core.LogEvent{item.Key(): rows}}
	snapshot, err := CaptureIssueEvidence(context.Background(), client, []core.InventoryItem{item}, CollectOptions{
		IssueID: issueID(issue.Key), Namespace: "default", Target: "api", Context: "dev", Now: func() time.Time { return when },
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snapshot.Content, "topsecret") || !strings.Contains(snapshot.Content, "password=[REDACTED]") {
		t.Fatalf("credential was not redacted: %q", snapshot.Content[:min(120, len(snapshot.Content))])
	}
	if got := strings.Count(snapshot.Content, "at service"); got != 80 {
		t.Fatalf("captured %d stack frames, want 80", got)
	}
}

func TestCaptureIssueEvidenceReportsMissingAndCollectionErrors(t *testing.T) {
	item := core.InventoryItem{Pod: "api-abc", Container: "api"}
	base := CollectOptions{IssueID: "0123456789abcdef", Namespace: "default", Target: "api"}
	if _, err := CaptureIssueEvidence(context.Background(), evidenceFakeClient{}, []core.InventoryItem{item}, base); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected explicit missing issue error, got %v", err)
	}
	client := evidenceFakeClient{errs: map[string]error{item.Key(): errors.New("kubectl failed token=private")}}
	if _, err := CaptureIssueEvidence(context.Background(), client, []core.InventoryItem{item}, base); err == nil || !strings.Contains(err.Error(), "incomplete") || strings.Contains(err.Error(), "private") {
		t.Fatalf("expected redacted collection error, got %v", err)
	}
}

func TestCaptureIssueEvidenceAcceptsScopedAndLegacyIDs(t *testing.T) {
	item := core.InventoryItem{Pod: "api-abc", Container: "api"}
	when := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	event := core.LogEvent{Pod: item.Pod, Container: item.Container, Message: "ERROR upstream timeout", ObservedAt: when, Workload: "deployment/api"}
	scoped, ok := core.ClassifyIssue(event)
	if !ok {
		t.Fatal("test error did not classify")
	}
	legacy := event
	legacy.Workload = ""
	legacyIssue, ok := core.ClassifyIssue(legacy)
	if !ok {
		t.Fatal("test legacy error did not classify")
	}
	client := evidenceFakeClient{rows: map[string][]core.LogEvent{item.Key(): {legacy}}}
	for _, id := range []string{issueID(scoped.Key), issueID(legacyIssue.Key)} {
		snapshot, err := CaptureIssueEvidence(context.Background(), client, []core.InventoryItem{item}, CollectOptions{
			IssueID: id, Namespace: "default", Target: "api", Workloads: map[string]string{item.Pod: "deployment/api"},
		})
		if err != nil {
			t.Fatalf("capture ID %s: %v", id, err)
		}
		if snapshot.IssueID != id || !strings.Contains(snapshot.Content, "ERROR upstream timeout") {
			t.Fatalf("unexpected evidence for %s: %+v", id, snapshot)
		}
	}
}

func TestEvidencePaginationReconstructsLargeUnicodeSnapshotAndIsStable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private-evidence")
	content := strings.Repeat("λ🚀 multiline evidence\n", 240000) // >5 MiB
	snapshot := EvidenceSnapshot{
		Scope:   Scope{Namespace: "default", Target: "api", Pods: []string{"api-1"}},
		IssueID: "0123456789abcdef", CreatedAt: "2026-09-29T12:00:00Z", Content: content,
	}
	manifest, err := SaveEvidence(dir, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.EvidenceID) != 64 || manifest.TotalBytes != len(content) {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("evidence directory mode=%v", info.Mode())
	}
	var rebuilt strings.Builder
	cursor := ""
	for {
		page, err := ReadEvidencePage(dir, manifest.EvidenceID, cursor, 4096)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Text) > 4096 {
			t.Fatalf("page has %d text bytes", len(page.Text))
		}
		rebuilt.WriteString(page.Text)
		if page.Complete {
			if page.NextCursor != "" {
				t.Fatal("complete page unexpectedly has a next cursor")
			}
			break
		}
		if page.NextCursor == "" {
			t.Fatal("incomplete page has no next cursor")
		}
		cursor = page.NextCursor
	}
	if rebuilt.String() != content {
		t.Fatalf("reconstructed content differs: got %d bytes want %d", rebuilt.Len(), len(content))
	}
	// Saved pages are immutable; changing live logs does not affect pagination.
	page, err := ReadEvidencePage(dir, manifest.EvidenceID, "", 4096)
	if err != nil || !strings.HasPrefix(page.Text, "λ🚀") {
		t.Fatalf("saved snapshot was not stable: %v", err)
	}
}

func TestSaveEvidenceIdempotentAndCursorAndPathValidation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evidence")
	snapshot := EvidenceSnapshot{Scope: Scope{Namespace: "default", Target: "api", Pods: []string{"api"}}, IssueID: "0123456789abcdef", CreatedAt: "2026-09-29T12:00:00Z", Content: "secret=abc\n"}
	first, err := SaveEvidence(dir, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshot := snapshot
	secondSnapshot.CreatedAt = "2026-09-30T00:00:00Z"
	second, err := SaveEvidence(dir, secondSnapshot)
	if err != nil || first.EvidenceID != second.EvidenceID {
		t.Fatalf("duplicate save was not idempotent: %+v %v", second, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, first.EvidenceID, "content.txt"))
	if err != nil || strings.Contains(string(data), "secret=abc") || !strings.Contains(string(data), "secret=[REDACTED]") {
		t.Fatalf("persisted evidence was not redacted: err=%v", err)
	}
	if _, err := ReadEvidencePage(dir, first.EvidenceID, "not-a-cursor", 1024); err == nil {
		t.Fatal("accepted malformed cursor")
	}
	other := strings.Repeat("0", 64)
	if _, err := ReadEvidencePage(dir, other, "", 1024); err == nil {
		t.Fatal("accepted an unknown/path-like ID")
	}
	if _, err := ReadEvidencePage(dir, "../"+first.EvidenceID, "", 1024); err == nil {
		t.Fatal("accepted traversal ID")
	}
	link := filepath.Join(t.TempDir(), "evidence-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEvidencePage(link, first.EvidenceID, "", 1024); err == nil {
		t.Fatal("accepted symlink directory")
	}
	contentPath := filepath.Join(dir, first.EvidenceID, "content.txt")
	if err := os.WriteFile(contentPath, []byte("secret=[REDACTED]\r"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEvidencePage(dir, first.EvidenceID, "", 1024); err == nil || !strings.Contains(err.Error(), "identity verification") {
		t.Fatalf("same-size evidence tampering was not detected: %v", err)
	}
}
