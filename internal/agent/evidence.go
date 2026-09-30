package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

const (
	EvidenceSchemaVersion    = "tailg.evidence/v1"
	DefaultEvidencePageBytes = 16 * 1024
	MinEvidencePageBytes     = 1024
	MaxEvidencePageBytes     = 256 * 1024
)

var verifiedEvidenceMu sync.Mutex
var verifiedEvidenceFiles = map[string]evidenceFileSignature{}

type evidenceFileSignature struct {
	size    int64
	modTime int64
}

// EvidenceSnapshot is a complete, redacted, immutable excerpt for one issue.
// Content is readable text; timestamps and pod/container source metadata are
// included by CaptureIssueEvidence. Redact is a credential scrubber, not a
// guarantee that content is free of personal or other sensitive information.
type EvidenceSnapshot struct {
	Scope     Scope  `json:"scope"`
	IssueID   string `json:"issueId"`
	CreatedAt string `json:"createdAt"`
	Content   string `json:"content"`
}

// EvidenceManifest describes a saved evidence snapshot. EvidenceID hashes the
// canonical scope, issue ID, and redacted content; creation time is excluded.
type EvidenceManifest struct {
	SchemaVersion string `json:"schemaVersion"`
	EvidenceID    string `json:"evidenceId"`
	Scope         Scope  `json:"scope"`
	IssueID       string `json:"issueId"`
	CreatedAt     string `json:"createdAt"`
	TotalBytes    int    `json:"totalBytes"`
}

// EvidencePage has byte-limited Text. maxBytes caps Text bytes, not JSON
// serialization overhead. Cursor is the input cursor; NextCursor is empty at
// EOF. Complete is true only when the returned page includes the final byte.
type EvidencePage struct {
	Manifest   EvidenceManifest `json:"manifest"`
	Text       string           `json:"text"`
	Cursor     string           `json:"cursor,omitempty"`
	NextCursor string           `json:"nextCursor,omitempty"`
	Complete   bool             `json:"complete"`
}

// CaptureIssueEvidence captures full retained logs for the selected inventory
// items and extracts the latest event matching options.IssueID. options.Since
// and report line limits are intentionally ignored: retained evidence uses
// Tail=-1. The caller owns timeout policy through ctx.
func CaptureIssueEvidence(ctx context.Context, client KubernetesClient, items []core.InventoryItem, options CollectOptions) (EvidenceSnapshot, error) {
	if client == nil {
		return EvidenceSnapshot{}, errors.New("Kubernetes client is not configured")
	}
	if err := validateIssueID(options.IssueID); err != nil {
		return EvidenceSnapshot{}, err
	}
	scope := Scope{Context: options.Context, Namespace: options.Namespace, Target: options.Target, Pods: core.UniquePods(items)}
	if err := validateEvidenceScope(scope); err != nil {
		return EvidenceSnapshot{}, err
	}
	if len(items) == 0 {
		return EvidenceSnapshot{}, errors.New("no selected containers for evidence capture")
	}

	var latest core.LogEvent
	found := false
	var collectionErrors []string
	rowsByItem := make(map[string][]core.LogEvent, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Pod) == "" || strings.TrimSpace(item.Container) == "" {
			collectionErrors = append(collectionErrors, "selected container has an empty pod or container name")
			continue
		}
		rows, err := client.Snapshot(ctx, item, kube.LogOptions{Tail: -1})
		if err != nil {
			collectionErrors = append(collectionErrors, fmt.Sprintf("%s/%s: %s", item.Pod, item.Container, Redact(err.Error())))
			continue
		}
		rowsByItem[item.Key()] = rows
		for _, event := range rows {
			if workload := options.Workloads[event.Pod]; workload != "" {
				event.Workload = workload
			}
			issue, ok := core.ClassifyIssue(event)
			matches := ok && issueID(issue.Key) == options.IssueID
			if !matches && event.Workload != "" {
				// Existing issue IDs are unscoped. Accept them for compatibility,
				// while preferring the new workload-scoped identity above.
				legacy := event
				legacy.Workload = ""
				legacyIssue, legacyOK := core.ClassifyIssue(legacy)
				matches = legacyOK && issueID(legacyIssue.Key) == options.IssueID
			}
			if !matches {
				continue
			}
			if !found || event.ObservedAt.After(latest.ObservedAt) {
				latest, found = event, true
			}
		}
	}
	if len(collectionErrors) > 0 {
		sort.Strings(collectionErrors)
		return EvidenceSnapshot{}, fmt.Errorf("evidence collection incomplete: %s", strings.Join(collectionErrors, "; "))
	}
	if !found {
		return EvidenceSnapshot{}, fmt.Errorf("issue %s was not found in retained logs for the selected scope", options.IssueID)
	}
	// Keep the original rows so LogBlock uses exactly the retained snapshot read.
	sourceRows := rowsByItem[(core.InventoryItem{Pod: latest.Pod, Container: latest.Container}).Key()]
	if options.Workloads[latest.Pod] != "" {
		for index := range sourceRows {
			sourceRows[index].Workload = options.Workloads[latest.Pod]
		}
	}
	block, ok := core.LogBlock(sourceRows, latest)
	if !ok {
		return EvidenceSnapshot{}, fmt.Errorf("issue %s anchor is no longer present in retained logs", options.IssueID)
	}
	var content strings.Builder
	for _, event := range block {
		line := fmt.Sprintf("[%s] %s/%s: %s", timestamp(event.ObservedAt), event.Pod, event.Container, event.Message)
		content.WriteString(Redact(strings.ToValidUTF8(line, "�")))
		content.WriteByte('\n')
	}
	created := time.Now().UTC()
	if options.Now != nil {
		created = options.Now().UTC()
	}
	return EvidenceSnapshot{Scope: scope, IssueID: options.IssueID, CreatedAt: timestamp(created), Content: content.String()}, nil
}

// SaveEvidence writes an immutable snapshot as one private JSON file named by
// its content identity. The directory must not be a symlink; existing files
// with the same identity are verified and treated idempotently.
func SaveEvidence(dir string, snapshot EvidenceSnapshot) (EvidenceManifest, error) {
	if err := validateEvidenceScope(snapshot.Scope); err != nil {
		return EvidenceManifest{}, err
	}
	if err := validateIssueID(snapshot.IssueID); err != nil {
		return EvidenceManifest{}, err
	}
	if snapshot.CreatedAt == "" {
		return EvidenceManifest{}, errors.New("evidence creation time is required")
	}
	if !utf8.ValidString(snapshot.Content) {
		return EvidenceManifest{}, errors.New("evidence content must be valid UTF-8")
	}
	snapshot.Content = Redact(snapshot.Content)
	if err := ensurePrivateDirectory(dir); err != nil {
		return EvidenceManifest{}, err
	}
	id, err := evidenceIdentity(snapshot)
	if err != nil {
		return EvidenceManifest{}, err
	}
	manifest := EvidenceManifest{SchemaVersion: EvidenceSchemaVersion, EvidenceID: id, Scope: snapshot.Scope, IssueID: snapshot.IssueID, CreatedAt: snapshot.CreatedAt, TotalBytes: len(snapshot.Content)}
	data, err := json.Marshal(manifest)
	if err != nil {
		return EvidenceManifest{}, err
	}
	final := filepath.Join(dir, id)
	if existing, err := readEvidenceRecord(final, id); err == nil {
		if existing.Manifest.EvidenceID != manifest.EvidenceID || existing.Content != snapshot.Content || !sameScope(existing.Manifest.Scope, snapshot.Scope) || existing.Manifest.IssueID != snapshot.IssueID {
			return EvidenceManifest{}, errors.New("evidence identity collision or corrupt existing snapshot")
		}
		return existing.Manifest, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return EvidenceManifest{}, err
	}
	tmpDir, err := os.MkdirTemp(dir, ".evidence-*.tmp")
	if err != nil {
		return EvidenceManifest{}, err
	}
	defer os.RemoveAll(tmpDir)
	if err := writePrivateFile(filepath.Join(tmpDir, "content.txt"), []byte(snapshot.Content)); err != nil {
		return EvidenceManifest{}, err
	}
	if err := writePrivateFile(filepath.Join(tmpDir, "manifest.json"), data); err != nil {
		return EvidenceManifest{}, err
	}
	if err := os.Chmod(tmpDir, 0700); err != nil {
		return EvidenceManifest{}, err
	}
	// Rename publishes both immutable files atomically as one directory.
	if err := os.Rename(tmpDir, final); err != nil {
		if existing, readErr := readEvidenceRecord(final, id); readErr == nil && existing.Content == snapshot.Content && existing.Manifest.IssueID == snapshot.IssueID && sameScope(existing.Manifest.Scope, snapshot.Scope) {
			return existing.Manifest, nil
		}
		return EvidenceManifest{}, err
	}
	return manifest, nil
}

// ReadEvidencePage reads a saved immutable snapshot. maxBytes=0 selects 16 KiB;
// otherwise maxBytes must be within 1 KiB..256 KiB. Cursor is a URL-safe base64
// encoding of "<evidence-id>:<byte-offset>" and is bound to the requested ID.
func ReadEvidencePage(dir, id, cursor string, maxBytes int) (EvidencePage, error) {
	if !validHash(id) {
		return EvidencePage{}, errors.New("invalid evidence ID")
	}
	if maxBytes == 0 {
		maxBytes = DefaultEvidencePageBytes
	}
	if maxBytes < MinEvidencePageBytes || maxBytes > MaxEvidencePageBytes {
		return EvidencePage{}, fmt.Errorf("maxBytes must be between %d and %d", MinEvidencePageBytes, MaxEvidencePageBytes)
	}
	if err := validateExistingDirectory(dir); err != nil {
		return EvidencePage{}, err
	}
	path := filepath.Join(dir, id)
	manifest, err := readEvidenceManifest(path, id)
	if err != nil {
		return EvidencePage{}, err
	}
	offset, err := decodeEvidenceCursor(id, cursor)
	if err != nil {
		return EvidencePage{}, err
	}
	contentPath := filepath.Join(path, "content.txt")
	contentInfo, err := os.Lstat(contentPath)
	if err != nil {
		return EvidencePage{}, err
	}
	if contentInfo.Mode()&os.ModeSymlink != 0 || !contentInfo.Mode().IsRegular() || !isPrivate(contentInfo) || int64(manifest.TotalBytes) != contentInfo.Size() {
		return EvidencePage{}, errors.New("evidence content file permissions, type, or size are invalid")
	}
	signature := evidenceFileSignature{size: contentInfo.Size(), modTime: contentInfo.ModTime().UnixNano()}
	cacheKey := contentPath + "\x00" + id
	verifiedEvidenceMu.Lock()
	verified := verifiedEvidenceFiles[cacheKey] == signature
	verifiedEvidenceMu.Unlock()
	if !verified {
		if err := verifyEvidenceContent(contentPath, manifest, id); err != nil {
			return EvidencePage{}, err
		}
		verifiedEvidenceMu.Lock()
		if len(verifiedEvidenceFiles) >= 4096 {
			clear(verifiedEvidenceFiles)
		}
		verifiedEvidenceFiles[cacheKey] = signature
		verifiedEvidenceMu.Unlock()
	}
	if offset > manifest.TotalBytes {
		return EvidencePage{}, errors.New("evidence cursor offset is invalid")
	}
	f, err := os.Open(contentPath)
	if err != nil {
		return EvidencePage{}, err
	}
	defer f.Close()
	if offset < manifest.TotalBytes {
		var first [1]byte
		if _, err := f.ReadAt(first[:], int64(offset)); err != nil {
			return EvidencePage{}, err
		}
		if !utf8.RuneStart(first[0]) {
			return EvidencePage{}, errors.New("evidence cursor offset is not a UTF-8 boundary")
		}
	}
	remaining := manifest.TotalBytes - offset
	readSize := maxBytes + utf8.UTFMax
	if remaining < readSize {
		readSize = remaining
	}
	buffer := make([]byte, readSize)
	if len(buffer) > 0 {
		n, readErr := f.ReadAt(buffer, int64(offset))
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return EvidencePage{}, readErr
		}
		if n != len(buffer) {
			return EvidencePage{}, errors.New("evidence content file was truncated while reading")
		}
	}
	pageSize := len(buffer)
	if pageSize > maxBytes {
		pageSize = maxBytes
		for pageSize > 0 && !utf8.RuneStart(buffer[pageSize]) {
			pageSize--
		}
	}
	if pageSize == 0 && remaining > 0 {
		_, size := utf8.DecodeRune(buffer)
		pageSize = size
	}
	text := string(buffer[:pageSize])
	if !utf8.ValidString(text) {
		return EvidencePage{}, errors.New("evidence content contains invalid UTF-8")
	}
	end := offset + pageSize
	complete := end == manifest.TotalBytes
	page := EvidencePage{Manifest: manifest, Text: text, Cursor: cursor, Complete: complete}
	if !complete {
		page.NextCursor = encodeEvidenceCursor(id, end)
	}
	return page, nil
}

// verifyEvidenceContent recomputes the content-addressed evidence ID while
// streaming the file, so page reads detect corruption without loading the
// entire snapshot into memory.
func verifyEvidenceContent(path string, manifest EvidenceManifest, id string) error {
	canonical := struct {
		Scope   Scope  `json:"scope"`
		IssueID string `json:"issueId"`
		Content string `json:"content"`
	}{Scope: manifest.Scope, IssueID: manifest.IssueID}
	prefix, err := json.Marshal(canonical)
	if err != nil {
		return err
	}
	// The empty content field ends in `""}`. Keep its opening quote as the
	// start of the streamed JSON string, then append the final object brace.
	if len(prefix) < 2 || string(prefix[len(prefix)-2:]) != `"}` {
		return errors.New("could not prepare evidence identity verification")
	}
	h := sha256.New()
	if _, err := h.Write(prefix[:len(prefix)-2]); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	for {
		r, size, readErr := reader.ReadRune()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
		if r == utf8.RuneError && size == 1 {
			return errors.New("evidence snapshot content is invalid UTF-8")
		}
		var encoded []byte
		switch r {
		case '"':
			encoded = []byte(`\"`)
		case '\\':
			encoded = []byte(`\\`)
		case '\b':
			encoded = []byte(`\b`)
		case '\f':
			encoded = []byte(`\f`)
		case '\n':
			encoded = []byte(`\n`)
		case '\r':
			encoded = []byte(`\r`)
		case '\t':
			encoded = []byte(`\t`)
		case '<':
			encoded = []byte(`\u003c`)
		case '>':
			encoded = []byte(`\u003e`)
		case '&':
			encoded = []byte(`\u0026`)
		case '\u2028':
			encoded = []byte(`\u2028`)
		case '\u2029':
			encoded = []byte(`\u2029`)
		default:
			if r < 0x20 {
				encoded = []byte(fmt.Sprintf(`\u%04x`, r))
			} else {
				encoded = make([]byte, size)
				utf8.EncodeRune(encoded, r)
			}
		}
		if _, err := h.Write(encoded); err != nil {
			return err
		}
	}
	if _, err := h.Write([]byte(`"}`)); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != id {
		return errors.New("evidence snapshot identity verification failed")
	}
	return nil
}

type evidenceRecord struct {
	Manifest EvidenceManifest `json:"manifest"`
	Content  string           `json:"content"`
}

func evidenceIdentity(snapshot EvidenceSnapshot) (string, error) {
	canonical := struct {
		Scope   Scope  `json:"scope"`
		IssueID string `json:"issueId"`
		Content string `json:"content"`
	}{Scope: snapshot.Scope, IssueID: snapshot.IssueID, Content: snapshot.Content}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func readEvidenceRecord(path, id string) (evidenceRecord, error) {
	manifest, err := readEvidenceManifest(path, id)
	if err != nil {
		return evidenceRecord{}, err
	}
	contentPath := filepath.Join(path, "content.txt")
	info, err := os.Lstat(contentPath)
	if err != nil {
		return evidenceRecord{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !isPrivate(info) || int64(manifest.TotalBytes) != info.Size() {
		return evidenceRecord{}, errors.New("evidence content file permissions, type, or size are unsafe")
	}
	f, err := os.Open(contentPath)
	if err != nil {
		return evidenceRecord{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return evidenceRecord{}, err
	}
	content := string(data)
	if !utf8.ValidString(content) {
		return evidenceRecord{}, errors.New("evidence snapshot content is invalid UTF-8")
	}
	actual, err := evidenceIdentity(EvidenceSnapshot{Scope: manifest.Scope, IssueID: manifest.IssueID, Content: content})
	if err != nil || actual != id {
		return evidenceRecord{}, errors.New("evidence snapshot identity verification failed")
	}
	return evidenceRecord{Manifest: manifest, Content: content}, nil
}

func readEvidenceManifest(dir, id string) (EvidenceManifest, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return EvidenceManifest{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !isPrivate(info) {
		return EvidenceManifest{}, errors.New("evidence snapshot directory permissions or type are unsafe")
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	info, err = os.Lstat(manifestPath)
	if err != nil {
		return EvidenceManifest{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !isPrivate(info) || info.Size() > 1024*1024 {
		return EvidenceManifest{}, errors.New("evidence manifest permissions, type, or size are unsafe")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return EvidenceManifest{}, err
	}
	var manifest EvidenceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return EvidenceManifest{}, fmt.Errorf("invalid evidence manifest: %w", err)
	}
	if manifest.SchemaVersion != EvidenceSchemaVersion || manifest.EvidenceID != id || !validHash(manifest.EvidenceID) || manifest.TotalBytes < 0 {
		return EvidenceManifest{}, errors.New("evidence snapshot manifest is invalid")
	}
	if err := validateIssueID(manifest.IssueID); err != nil {
		return EvidenceManifest{}, errors.New("evidence snapshot manifest is invalid")
	}
	if err := validateEvidenceScope(manifest.Scope); err != nil {
		return EvidenceManifest{}, errors.New("evidence snapshot manifest is invalid")
	}
	return manifest, nil
}

func writePrivateFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func validateIssueID(id string) error {
	if len(id) != 16 {
		return errors.New("issue ID must be 16 hexadecimal characters")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return errors.New("issue ID must be 16 hexadecimal characters")
	}
	return nil
}

func validHash(id string) bool {
	if len(id) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func validateEvidenceScope(scope Scope) error {
	if strings.TrimSpace(scope.Namespace) == "" || strings.TrimSpace(scope.Target) == "" {
		return errors.New("evidence scope requires namespace and target")
	}
	for _, value := range append([]string{scope.Context, scope.Namespace, scope.Target}, scope.Pods...) {
		if strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("evidence scope contains invalid control characters")
		}
	}
	for _, pod := range scope.Pods {
		if strings.TrimSpace(pod) == "" {
			return errors.New("evidence scope contains an empty pod name")
		}
	}
	return nil
}

func sameScope(a, b Scope) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func ensurePrivateDirectory(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("evidence directory is required")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := rejectSymlinkPath(abs, true); err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("evidence path must be a real directory, not a symlink")
	}
	if !isPrivate(info) {
		return errors.New("evidence directory must have private permissions")
	}
	return nil
}

func validateExistingDirectory(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("evidence directory is required")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := rejectSymlinkPath(abs, false); err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !isPrivate(info) {
		return errors.New("evidence directory permissions or type are unsafe")
	}
	return nil
}

func rejectSymlinkPath(path string, allowMissing bool) error {
	volume := filepath.VolumeName(path)
	current := volume + string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(path, current), string(filepath.Separator))
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && allowMissing {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("evidence path contains a symlink: %s", current)
		}
	}
	return nil
}

func encodeEvidenceCursor(id string, offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%d", id, offset)))
}

func decodeEvidenceCursor(id, cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, errors.New("invalid evidence cursor")
	}
	parts := strings.Split(string(decoded), ":")
	if len(parts) != 2 || parts[0] != id || parts[1] == "" {
		return 0, errors.New("evidence cursor does not match snapshot")
	}
	for _, digit := range parts[1] {
		if digit < '0' || digit > '9' {
			return 0, errors.New("invalid evidence cursor offset")
		}
	}
	offset, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, errors.New("invalid evidence cursor offset")
	}
	return offset, nil
}
