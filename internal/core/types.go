package core

import "time"

const (
	DefaultTailLines       = 500
	DefaultBufferLines     = 50_000
	DefaultHeartbeatWindow = 15 * time.Minute
	DefaultStatusInterval  = 5 * time.Second
	DefaultStatusTimeout   = 10 * time.Minute
	SearchContextLines     = 5
)

var DefaultExcludePatterns = []string{"health|ready|live"}

type InventoryItem struct {
	Pod       string
	Container string
}

func (i InventoryItem) Key() string { return i.Pod + "\x00" + i.Container }

type MappedResource struct {
	Kind    string
	Name    string
	Sources []string
}

type AppChoice struct {
	Namespace string
	Name      string
	Pods      []string
	Selector  string
	Ready     string
	Phases    string
	Restarts  int
}

type LogEvent struct {
	Pod        string
	Container  string
	Message    string
	ObservedAt time.Time
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
