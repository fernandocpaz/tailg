package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

func aiNetworkFixture() core.NetworkReport {
	return core.NetworkReport{Context: "qa", Namespace: "apollo", At: time.Now(), Sources: map[string]bool{"pods": true, "services": true}, Rows: []core.NetworkRow{
		{Kind: "Service", Name: "api", Status: "REVIEW", Summary: "no ready backends", Detail: "password=sentinel-secret\n" + strings.Repeat("日本語 diagnostic detail\n", 1000), Objects: []string{"Pod/api-abc"}},
		{Kind: "Ingress", Name: "api", Status: "OK", Summary: "api.test -> api:80", Objects: []string{"Service/api", "Pod/api-abc"}},
	}, Events: []core.NamespaceEvent{{ID: "cni", Type: "Warning", Reason: "FailedCreatePodSandBox", Object: "Pod/api-abc", Message: "CNI failed Authorization: Bearer sentinel-token\x1b[31m\a", Count: 2}}}
}

func TestNetworkStructuredEvidenceAndSharedPodCorrelation(t *testing.T) {
	raw := aiNetworkFixture()
	issues := []Issue{{ID: "shared", Pods: []string{"api-abc"}}, {ID: "unrelated", Pods: []string{"another-pod"}}}
	r := BuildNetworkReport(raw, issues, 2*1024*1024)
	if r.SchemaVersion != NetworkSchemaVersion || r.Scope.Target != "namespace/*" || r.Coverage.Status != "complete" || r.Summary.Review != 1 || r.Summary.WarningEvents != 1 {
		t.Fatalf("wrong envelope: %+v", r.Summary)
	}
	if len(r.Findings) != 1 || r.Findings[0].Resource != "Service/api" || r.Findings[0].Code != "NETWORK_CONFIGURATION_REVIEW" {
		t.Fatalf("findings: %+v", r.Findings)
	}
	if len(r.Correlations) != 2 {
		t.Fatalf("correlations: %+v", r.Correlations)
	}
	for _, c := range r.Correlations {
		if c.IssueID != "shared" || c.Relationship != "shared_pod_evidence" || len(c.Pods) != 1 {
			t.Fatal("correlation inferred without a shared pod")
		}
	}
	for _, format := range []string{"json", "ndjson"} {
		var output strings.Builder
		if err := WriteNetworkReport(&output, r, format); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output.String(), "sentinel-") || strings.Contains(output.String(), `\u001b`) || strings.Contains(output.String(), `\u0007`) {
			t.Fatal("credentials or terminal controls escaped redaction")
		}
		var decoded NetworkReport
		if err := json.Unmarshal([]byte(output.String()), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Coverage.Status != "complete" || len(decoded.Resources) != 2 || decoded.Summary.Status != "review" {
			t.Fatal("structured fields lost")
		}
	}
}

func TestNetworkLimitsRetainCountsAndNeverReportTruncatedHealth(t *testing.T) {
	for _, format := range []string{"json", "ndjson"} {
		r := BuildNetworkReport(aiNetworkFixture(), nil, 4096)
		original := r.Resources[0].Detail
		limited, err := LimitNetworkReport(r, format)
		if err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		if err = WriteNetworkReport(&output, limited, format); err != nil {
			t.Fatal(err)
		}
		if output.Len() > 4096 || !limited.Truncated || limited.Coverage.Status == "complete" || limited.Summary.Resources != 2 || limited.Summary.Review != 1 || NetworkExitCode(limited) != 3 {
			t.Fatalf("limit not reflected: %d %+v", output.Len(), limited.Summary)
		}
		if r.Resources[0].Detail != original {
			t.Fatal("limiter mutated the caller's evidence")
		}
	}
	raw := aiNetworkFixture()
	raw.Rows[0].Status = "OK"
	raw.Events = nil
	r := BuildNetworkReport(raw, nil, 4096)
	limited, err := LimitNetworkReport(r, "json")
	if err != nil {
		t.Fatal(err)
	}
	if limited.Summary.Status != "unknown" || NetworkExitCode(limited) != 3 {
		t.Fatal("truncated checks_passed remained healthy")
	}
}

func TestNetworkPartialCoverageAndUnknownRowsStayUnknown(t *testing.T) {
	raw := aiNetworkFixture()
	raw.Sources["services"] = false
	raw.Errors = []string{"Forbidden password=sentinel-secret"}
	r := BuildNetworkReport(raw, nil, 4096)
	if r.Coverage.Status != "partial" || r.Coverage.Sources["services"] || NetworkExitCode(r) != 3 || strings.Contains(r.CollectionErrors[0].Message, "sentinel") {
		t.Fatal("RBAC gap/redaction lost")
	}
	raw.Sources = nil
	r = BuildNetworkReport(raw, nil, 4096)
	if r.Coverage.Status != "unavailable" {
		t.Fatal("missing source coverage treated as complete")
	}
	raw = aiNetworkFixture()
	raw.Rows[1].Status = "UNKNOWN"
	r = BuildNetworkReport(raw, nil, 4096)
	if NetworkExitCode(r) != 3 {
		t.Fatal("unknown route hidden by other REVIEW findings")
	}
}

func TestDiagnoseIncludesNetworkAcrossAllFormatsWithinSharedLimit(t *testing.T) {
	for _, format := range []string{"json", "ndjson", "text"} {
		r := Report{SchemaVersion: SchemaVersion, Kind: "DiagnosticReport", Limits: Limits{MaxBytes: 4096}, Summary: Summary{Status: "healthy"}, Coverage: Coverage{Status: "complete"}, Pods: []Pod{}, Issues: []Issue{}, KubernetesEvents: []KubernetesEvent{}, CollectionErrors: []CollectionError{}}
		AttachNetwork(&r, BuildNetworkReport(aiNetworkFixture(), nil, 4096))
		if r.Summary.Status != "warning" {
			t.Fatal("network findings omitted from report status")
		}
		var output strings.Builder
		if err := WriteReport(&output, r, format); err != nil {
			t.Fatal(err)
		}
		if output.Len() > 4096 || !strings.Contains(output.String(), "network") && !strings.Contains(output.String(), "NETWORK") || !strings.Contains(output.String(), "truncated") {
			t.Fatalf("combined limit/format lost network evidence: %s", output.String())
		}
	}
}
