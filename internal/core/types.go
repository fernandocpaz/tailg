package core

import "time"

const (
	DefaultTailLines       = 500
	DefaultBufferLines     = 50_000
	DefaultHeartbeatWindow = 15 * time.Minute
	DefaultStatusInterval  = 5 * time.Second
	DefaultStatusTimeout   = 10 * time.Minute
	DefaultStatusLookback  = 2 * time.Hour
	SearchContextLines     = 5
)

var DefaultExcludePatterns = []string{"health|ready|live"}

type InventoryItem struct {
    Workload string // optional logical workload for non-Kubernetes providers
	Pod          string
	Container    string
	PodUID       string
	ContainerID  string
	RestartCount int
	Completed    bool // Kubernetes reports pod phase Succeeded; no stream retry needed.
}

func (i InventoryItem) Key() string { return i.Pod + "\x00" + i.Container }

// Replaced compares runtime identity while keeping Key stable for UI sources.
// An absent container ID during startup is not itself another restart.
func (i InventoryItem) Replaced(previous InventoryItem) bool {
	return (i.PodUID != "" && previous.PodUID != "" && i.PodUID != previous.PodUID) ||
		i.RestartCount != previous.RestartCount ||
		(i.ContainerID != "" && previous.ContainerID != "" && i.ContainerID != previous.ContainerID)
}

type MappedResource struct {
	Kind    string
	Name    string
	Sources []string
}

type AppChoice struct {
    LastLogAt time.Time // provider-supplied observation, never a deployment timestamp
	Namespace  string
	Name       string
	Pods       []string
	PodChoices []PodChoice
	Selector   string
	Ready      string
	Phases     string
	Restarts   int
	StartedAt  time.Time
	DeployedAt time.Time
	ImageTag   string
	CronJob    *CronJobInfo
}

type CronJobInfo struct {
	Name            string
	Schedule        string
	TimeZone        string
	Suspended       bool
	Active          int
	LastScheduledAt time.Time
}

type PodChoice struct {
	Name      string
	Ready     string
	Phase     string
	StartedAt time.Time
	Sidecars  []string
}

type LogEvent struct {
    // Provider-supplied log metadata (e.g. from Dynatrace Grail). These fields
    // are optional: Kubernetes tailg continues parsing its raw log messages.
    Level string
    TraceID string
    SpanID string
    RequestID string
    // Optional structured HTTP completion metadata supplied by remote log
    // providers. These measurements describe logged completions, not APM.
    HTTPMethod string
    HTTPPath string
    HTTPStatusCode int
    HTTPDuration time.Duration
    HasHTTPDuration bool
	Workload   string // verified controller identity, optional for legacy callers
	Pod        string
	Container  string
	Message    string
	ObservedAt time.Time
	ReceivedAt time.Time
	Started    bool // kubectl started; the remote stream is not yet confirmed
	Replayed   bool // already delivered on an earlier attempt; activity only
	Closed     bool
	Err        error
}

type HeartbeatSample struct {
	Timestamp   time.Time
	Pod         string
	Container   string
	Uptime      time.Duration
	HasUptime   bool
	Inflight    int
	OK          int
	Skipped     int
	Failed      int
	DLQ         int
	LastConsume string
	LastCommit  string
	BatchAge    time.Duration
	HasBatchAge bool
	BatchSize   int
	Breaker     string
}

type HeartbeatInterval struct {
	Start        time.Time
	End          time.Time
	Pod          string
	Container    string
	SampleCount  int
	Uptime       time.Duration
	HasUptime    bool
	Inflight     int
	OKTotal      int
	SkippedTotal int
	FailedTotal  int
	DLQTotal     int
	OKDelta      int
	SkippedDelta int
	FailedDelta  int
	DLQDelta     int
	LastConsume  string
	LastCommit   string
	BatchAge     time.Duration
	HasBatchAge  bool
	BatchSize    int
	Breaker      string
	Severity     string
	Reasons      []string
}
