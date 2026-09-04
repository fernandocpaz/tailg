package agent

import "time"

const SchemaVersion = "tailg.ai/v1"

type Mode string

const (
	ModeIssues   Mode = "issues"
	ModeDiagnose Mode = "diagnose"
)

type Limits struct {
	Tail         int `json:"tailPerContainer"`
	MaxLines     int `json:"maxLines"`
	MaxIssues    int `json:"maxIssues"`
	ContextLines int `json:"contextLines"`
	MaxBytes     int `json:"maxBytes"`
}

type Scope struct {
	Context   string   `json:"context,omitempty"`
	Namespace string   `json:"namespace"`
	Target    string   `json:"target"`
	Pods      []string `json:"pods"`
}

type Summary struct {
	Status        string `json:"status"`
	IssueGroups   int    `json:"issueGroups"`
	IssueEvents   int    `json:"issueEvents"`
	Errors        int    `json:"errors"`
	Warnings      int    `json:"warnings"`
	UnhealthyPods int    `json:"unhealthyPods"`
	LogLines      int    `json:"logLines"`
}

type LogLine struct {
	Timestamp string `json:"timestamp,omitempty"`
	Pod       string `json:"pod"`
	Container string `json:"container"`
	Message   string `json:"message"`
}

type IssueContext struct {
	Before []LogLine `json:"before"`
	Match  LogLine   `json:"match"`
	After  []LogLine `json:"after"`
}

type Issue struct {
	ID         string       `json:"id"`
	Severity   string       `json:"severity"`
	Kind       string       `json:"kind"`
	Summary    string       `json:"summary"`
	SearchTerm string       `json:"searchTerm"`
	Service    string       `json:"service"`
	Pods       []string     `json:"pods"`
	Count      int          `json:"count"`
	TotalCount int          `json:"totalCount"`
	FirstSeen  string       `json:"firstSeen"`
	LastSeen   string       `json:"lastSeen"`
	Increasing bool         `json:"increasing"`
	Context    IssueContext `json:"context"`
}

type Container struct {
	Name           string `json:"name"`
	Kind           string `json:"kind,omitempty"`
	Ready          bool   `json:"ready"`
	Restarts       int    `json:"restarts"`
	State          string `json:"state,omitempty"`
	Reason         string `json:"reason,omitempty"`
	ExitCode       int    `json:"exitCode,omitempty"`
	StartedAt      string `json:"startedAt,omitempty"`
	FinishedAt     string `json:"finishedAt,omitempty"`
	LastReason     string `json:"lastReason,omitempty"`
	LastExitCode   int    `json:"lastExitCode,omitempty"`
	LastFinishedAt string `json:"lastFinishedAt,omitempty"`
}

type Pod struct {
	Name       string      `json:"name"`
	Phase      string      `json:"phase"`
	Ready      int         `json:"ready"`
	Total      int         `json:"total"`
	Restarts   int         `json:"restarts"`
	Issues     []string    `json:"issues"`
	Containers []Container `json:"containers,omitempty"`
}

type KubernetesEvent struct {
	Timestamp string `json:"timestamp,omitempty"`
	Type      string `json:"type,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Object    string `json:"object,omitempty"`
	Message   string `json:"message"`
}

type CollectionError struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

type Report struct {
	SchemaVersion    string            `json:"schemaVersion"`
	Kind             string            `json:"kind"`
	GeneratedAt      string            `json:"generatedAt"`
	Window           string            `json:"window"`
	Scope            Scope             `json:"scope"`
	Limits           Limits            `json:"limits"`
	Summary          Summary           `json:"summary"`
	Pods             []Pod             `json:"pods"`
	Issues           []Issue           `json:"issues"`
	KubernetesEvents []KubernetesEvent `json:"kubernetesEvents"`
	Recommendations  []string          `json:"recommendations,omitempty"`
	CollectionErrors []CollectionError `json:"collectionErrors"`
	Truncated        bool              `json:"truncated"`
}

type CollectOptions struct {
	Mode         Mode
	Namespace    string
	Context      string
	Target       string
	Since        string
	Limits       Limits
	IssueID      string
	IncludeEvent func(string) bool
	Now          func() time.Time
}

type ErrorEnvelope struct {
	SchemaVersion string `json:"schemaVersion"`
	Kind          string `json:"kind"`
	Error         struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
