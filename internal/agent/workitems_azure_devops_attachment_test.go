package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const testAttachmentID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func TestAzureDevOpsAttachesFullEvidenceAndRetryReusesIt(t *testing.T) {
	incident := workItemTestIncident("open")
	spec := MonitorSpec{Context: "staging", Namespace: "payments"}
	evidenceDir := filepath.Join(t.TempDir(), "evidence")
	content := strings.Repeat("[2026-09-30T12:00:00Z] api-1/api: full exception frame 🚀\n", 110000) // >5 MiB
	manifest, err := SaveEvidence(evidenceDir, EvidenceSnapshot{Scope: Scope{Context: spec.Context, Namespace: spec.Namespace, Target: "api", Pods: []string{"api-1"}},
		IssueID: incident.IssueID, CreatedAt: "2026-09-30T12:00:00Z", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	var created, linked atomic.Bool
	var uploads, links atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/wiql"):
			if created.Load() {
				_, _ = io.WriteString(w, `{"workItems":[{"id":4711}]}`)
			} else {
				_, _ = io.WriteString(w, `{"workItems":[]}`)
			}
		case strings.HasSuffix(r.URL.Path, "/workitems/$Bug") && r.Method == http.MethodPost:
			created.Store(true)
			_, _ = io.WriteString(w, `{"id":4711,"fields":{"System.Title":"Full exception"}}`)
		case strings.HasSuffix(r.URL.Path, "/workitems/4711") && r.Method == http.MethodGet:
			if linked.Load() {
				_, _ = io.WriteString(w, fmt.Sprintf(`{"id":4711,"rev":2,"fields":{"System.Title":"Full exception"},"relations":[{"rel":"AttachedFile","url":%q,"attributes":{"comment":"Tailg evidence %s"}}]}`,
					server.URL+"/vitas/EMR/_apis/wit/attachments/"+testAttachmentID, manifest.EvidenceID))
			} else {
				_, _ = io.WriteString(w, `{"id":4711,"rev":1,"fields":{"System.Title":"Full exception"},"relations":[]}`)
			}
		case strings.HasSuffix(r.URL.Path, "/attachments") && r.Method == http.MethodPost:
			uploads.Add(1)
			if r.Header.Get("Content-Type") != "application/octet-stream" || r.ContentLength != int64(manifest.TotalBytes) || !strings.Contains(r.URL.Query().Get("fileName"), manifest.EvidenceID) {
				t.Errorf("unexpected upload metadata: type=%q bytes=%d url=%s", r.Header.Get("Content-Type"), r.ContentLength, r.URL.String())
			}
			data, readErr := io.ReadAll(r.Body)
			if readErr != nil || string(data) != content {
				t.Errorf("full evidence was not uploaded: got=%d bytes err=%v", len(data), readErr)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, fmt.Sprintf(`{"id":%q,"url":%q}`, testAttachmentID,
				server.URL+"/vitas/EMR/_apis/wit/attachments/"+testAttachmentID))
		case strings.HasSuffix(r.URL.Path, "/workitems/4711") && r.Method == http.MethodPatch:
			links.Add(1)
			var patch []workItemPatch
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil || len(patch) != 2 || patch[0].Op != "test" || patch[0].Path != "/rev" || patch[0].Value != float64(1) || patch[1].Path != "/relations/-" {
				t.Errorf("invalid attachment patch: %+v err=%v", patch, err)
			}
			linked.Store(true)
			_, _ = io.WriteString(w, `{"id":4711}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := AzureDevOpsWorkItems{Organization: "vitas", Project: "EMR", Token: "secret", BaseURL: server.URL, Client: server.Client()}
	first, err := client.CreateForIncidentWithEvidence(context.Background(), incident, "Full exception", "summary", evidenceDir, manifest.EvidenceID, spec)
	if err != nil || first.ID != 4711 || !first.Attached || first.Existing || first.EvidenceID != manifest.EvidenceID || first.AttachmentURL == "" {
		t.Fatalf("first attachment result=%+v err=%v", first, err)
	}
	second, err := client.CreateForIncidentWithEvidence(context.Background(), incident, "Full exception", "summary", evidenceDir, manifest.EvidenceID, spec)
	if err != nil || !second.Attached || !second.Existing || uploads.Load() != 1 || links.Load() != 1 {
		t.Fatalf("retry created a duplicate attachment: result=%+v err=%v uploads=%d links=%d", second, err, uploads.Load(), links.Load())
	}
}

func TestAzureDevOpsRejectsWrongEvidenceBeforeCreatingWorkItem(t *testing.T) {
	incident := workItemTestIncident("open")
	evidenceDir := filepath.Join(t.TempDir(), "evidence")
	manifest, err := SaveEvidence(evidenceDir, EvidenceSnapshot{Scope: Scope{Context: "staging", Namespace: "other", Target: "api"},
		IssueID: incident.IssueID, CreatedAt: "2026-09-30T12:00:00Z", Content: "ERROR test\n"})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	client := AzureDevOpsWorkItems{Organization: "vitas", Project: "EMR", Token: "secret", BaseURL: server.URL, Client: server.Client()}
	_, err = client.CreateForIncidentWithEvidence(context.Background(), incident, "", "", evidenceDir, manifest.EvidenceID, MonitorSpec{Context: "staging", Namespace: "payments"})
	if err == nil || !strings.Contains(err.Error(), "does not match") || requests.Load() != 0 {
		t.Fatalf("mismatched evidence reached Azure DevOps: err=%v requests=%d", err, requests.Load())
	}
}

func TestAzureDevOpsUsesChunkedUploadForLargeEvidence(t *testing.T) {
	incident := workItemTestIncident("open")
	spec := MonitorSpec{Context: "staging", Namespace: "payments"}
	evidenceDir := filepath.Join(t.TempDir(), "evidence")
	content := strings.Repeat("large error frame\n", 1000)
	manifest, err := SaveEvidence(evidenceDir, EvidenceSnapshot{Scope: Scope{Context: spec.Context, Namespace: spec.Namespace, Target: "api"},
		IssueID: incident.IssueID, CreatedAt: "2026-09-30T12:00:00Z", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	var chunkBytes atomic.Int64
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/wiql"):
			_, _ = io.WriteString(w, `{"workItems":[{"id":4711}]}`)
		case strings.HasSuffix(r.URL.Path, "/workitems/4711") && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"id":4711,"rev":1,"fields":{"System.Title":"Existing"},"relations":[]}`)
		case strings.HasSuffix(r.URL.Path, "/attachments") && r.Method == http.MethodPost:
			if r.URL.Query().Get("uploadType") != "chunked" || r.ContentLength != 0 {
				t.Errorf("chunked upload was not initialized correctly: %s", r.URL.String())
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, fmt.Sprintf(`{"id":%q,"url":%q}`, testAttachmentID,
				server.URL+"/vitas/EMR/_apis/wit/attachments/"+testAttachmentID))
		case strings.HasSuffix(r.URL.Path, "/attachments/"+testAttachmentID) && r.Method == http.MethodPut:
			body, readErr := io.ReadAll(r.Body)
			offset := chunkBytes.Load()
			if readErr != nil || r.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", offset, offset+int64(len(body))-1, manifest.TotalBytes) || content[offset:offset+int64(len(body))] != string(body) {
				t.Errorf("invalid chunk range or content: range=%q bytes=%d err=%v", r.Header.Get("Content-Range"), len(body), readErr)
			}
			chunkBytes.Add(int64(len(body)))
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"`+testAttachmentID+`"}`)
		case strings.HasSuffix(r.URL.Path, "/workitems/4711") && r.Method == http.MethodPatch:
			_, _ = io.WriteString(w, `{"id":4711}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := AzureDevOpsWorkItems{Organization: "vitas", Project: "EMR", Token: "secret", BaseURL: server.URL, Client: server.Client(), simpleUploadLimit: 4096, uploadChunkSize: 2048}
	result, err := client.CreateForIncidentWithEvidence(context.Background(), incident, "", "", evidenceDir, manifest.EvidenceID, spec)
	if err != nil || !result.Attached || chunkBytes.Load() != int64(len(content)) {
		t.Fatalf("chunked evidence upload incomplete: result=%+v err=%v bytes=%d", result, err, chunkBytes.Load())
	}
}

func TestAzureDevOpsRechecksAttachmentAfterRevisionConflict(t *testing.T) {
	incident := workItemTestIncident("open")
	spec := MonitorSpec{Context: "staging", Namespace: "payments"}
	evidenceDir := filepath.Join(t.TempDir(), "evidence")
	manifest, err := SaveEvidence(evidenceDir, EvidenceSnapshot{Scope: Scope{Context: spec.Context, Namespace: spec.Namespace, Target: "api"},
		IssueID: incident.IssueID, CreatedAt: "2026-09-30T12:00:00Z", Content: "full error\n"})
	if err != nil {
		t.Fatal(err)
	}
	var reads, links atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/wiql"):
			_, _ = io.WriteString(w, `{"workItems":[{"id":4711}]}`)
		case strings.HasSuffix(r.URL.Path, "/workitems/4711") && r.Method == http.MethodGet:
			if reads.Add(1) == 1 {
				_, _ = io.WriteString(w, `{"id":4711,"rev":1,"fields":{"System.Title":"Existing"},"relations":[]}`)
			} else {
				_, _ = io.WriteString(w, fmt.Sprintf(`{"id":4711,"rev":2,"fields":{"System.Title":"Existing"},"relations":[{"rel":"AttachedFile","url":%q,"attributes":{"comment":"Tailg evidence %s"}}]}`,
					server.URL+"/vitas/EMR/_apis/wit/attachments/"+testAttachmentID, manifest.EvidenceID))
			}
		case strings.HasSuffix(r.URL.Path, "/attachments") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, fmt.Sprintf(`{"id":%q,"url":%q}`, testAttachmentID, server.URL+"/vitas/EMR/_apis/wit/attachments/"+testAttachmentID))
		case strings.HasSuffix(r.URL.Path, "/workitems/4711") && r.Method == http.MethodPatch:
			links.Add(1)
			http.Error(w, "revision mismatch", http.StatusConflict)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := AzureDevOpsWorkItems{Organization: "vitas", Project: "EMR", Token: "secret", BaseURL: server.URL, Client: server.Client()}
	result, err := client.CreateForIncidentWithEvidence(context.Background(), incident, "", "", evidenceDir, manifest.EvidenceID, spec)
	if err != nil || !result.Attached || result.AttachmentURL == "" || reads.Load() != 2 || links.Load() != 1 {
		t.Fatalf("revision conflict did not reuse competing attachment: result=%+v err=%v reads=%d links=%d", result, err, reads.Load(), links.Load())
	}
}
