package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAzureDevOpsCreatesIncidentWorkItemWithSafeEvidence(t *testing.T) {
	incident := workItemTestIncident("open")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(":secret")) {
			t.Errorf("authorization header did not use configured token")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/_apis/wit/wiql"):
			if r.Method != http.MethodPost {
				t.Errorf("WIQL method=%s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"workItems":[]}`)
		case strings.HasSuffix(r.URL.Path, "/_apis/wit/workitems/$Bug") || strings.HasSuffix(r.URL.EscapedPath(), "/_apis/wit/workitems/%24Bug"):
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json-patch+json" {
				t.Errorf("create request method=%s content-type=%q", r.Method, r.Header.Get("Content-Type"))
			}
			var patch []workItemPatch
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Errorf("decode patch: %v", err)
			}
			values := map[string]string{}
			for _, item := range patch {
				values[item.Path] = item.Value.(string)
			}
			if !strings.Contains(values["/fields/System.Description"], "&lt;script&gt;") || strings.Contains(values["/fields/System.Description"], "<script>") || strings.Contains(values["/fields/System.Description"], "abc123") {
				t.Errorf("agent description was not escaped in HTML: %s", values["/fields/System.Description"])
			}
			if !strings.Contains(values["/fields/System.Tags"], "TailgIncident-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
				t.Errorf("idempotency tag missing: %s", values["/fields/System.Tags"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":4711,"fields":{"System.Title":"[Tailg] upstream timeout"}}`)
		default:
			t.Errorf("unexpected request path %s", r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := AzureDevOpsWorkItems{Organization: "vitas", Project: "EMR Project", WorkItemType: "Bug", Token: "secret", BaseURL: server.URL, Client: server.Client()}
	result, err := client.CreateForIncident(context.Background(), incident, "[Tailg] upstream timeout", "<script>alert(1); token=abc123</script>")
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != 4711 || result.Existing || result.Provider != "azure-devops" || !strings.Contains(result.URL, "/_workitems/edit/4711") {
		t.Fatalf("unexpected created result: %+v", result)
	}
}

func TestAzureDevOpsReusesExistingWorkItemAndRejectsResolvedIncident(t *testing.T) {
	incident := workItemTestIncident("open")
	created := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_apis/wit/wiql") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"workItems":[{"id":123}]}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/_apis/wit/workitems/123") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":123,"fields":{"System.Title":"Existing ticket"}}`)
			return
		}
		created = true
		http.Error(w, "unexpected create", http.StatusInternalServerError)
	}))
	defer server.Close()
	client := AzureDevOpsWorkItems{Organization: "vitas", Project: "EMR", Token: "secret", BaseURL: server.URL, Client: server.Client()}
	result, err := client.CreateForIncident(context.Background(), incident, "", "")
	if err != nil || result.ID != 123 || result.Title != "Existing ticket" || !result.Existing || created {
		t.Fatalf("existing work item was not reused: result=%+v err=%v created=%v", result, err, created)
	}
	incident.Status = "resolved"
	if _, err := client.CreateForIncident(context.Background(), incident, "", ""); err == nil || !strings.Contains(err.Error(), "open incidents") {
		t.Fatalf("resolved incident was not rejected: %v", err)
	}
}

func TestAzureDevOpsDoesNotExposeServerErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "credential=must-not-leak", http.StatusUnauthorized)
	}))
	defer server.Close()
	client := AzureDevOpsWorkItems{Organization: "vitas", Project: "EMR", Token: "secret", BaseURL: server.URL, Client: server.Client()}
	_, err := client.CreateForIncident(context.Background(), workItemTestIncident("open"), "incident", "")
	if err == nil || strings.Contains(err.Error(), "must-not-leak") || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("server error did not stay sanitized: %v", err)
	}
}

func workItemTestIncident(status string) Incident {
	return Incident{ID: strings.Repeat("a", 32), IssueID: "0123456789abcdef", Workload: "Deployment/api", Service: "api", Kind: "application_error",
		Severity: "error", Summary: "upstream timeout", Status: status, Occurrence: 1, FirstSeen: "2026-09-29T12:00:00Z",
		LastSeen: "2026-09-29T12:05:00Z", WindowCount: 4, Pods: []string{"api-1"}}
}
