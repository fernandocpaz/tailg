package core

import (
	"fmt"
	"strings"
	"time"
)

// NetworkRow describes observed configuration, not a live connectivity test.
type NetworkRow struct {
	Kind, Name, Status, Summary, Detail string
	Objects                             []string
}

func (r NetworkRow) ID() string { return r.Kind + "/" + r.Name }

type NetworkReport struct {
	Context, Namespace string
	At                 time.Time
	Rows               []NetworkRow
	Events             []NamespaceEvent
	Errors             []string
	Sources            map[string]bool
}

func (r NetworkReport) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Network review | Context: %s | Namespace: %s\nCollected: %s\n", r.Context, r.Namespace, r.At.Local().Format(time.RFC3339))
	b.WriteString("Configuration and retained Kubernetes events; no DNS, TLS handshake, or traffic probes.\n")
	for _, err := range r.Errors {
		fmt.Fprintf(&b, "INCOMPLETE: %s\n", err)
	}
	for _, row := range r.Rows {
		fmt.Fprintf(&b, "\n[%s] %s | %s\n%s\n", row.Status, row.ID(), row.Summary, row.Detail)
	}
	if len(r.Rows) == 0 {
		b.WriteString("\nNo network resources available in this snapshot.\n")
	}
	b.WriteString("\nRelated Kubernetes events (retained by the cluster)\n")
	for _, e := range r.Events {
		fmt.Fprintf(&b, "%s | %s | %s | %s x%d | %s\n", e.LastAt.Local().Format(time.RFC3339), e.Type, e.Object, e.Reason, e.Count, e.Message)
	}
	if len(r.Events) == 0 {
		b.WriteString("No related retained events available; this does not prove that traffic is healthy.\n")
	}
	return b.String()
}
