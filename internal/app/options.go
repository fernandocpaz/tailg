package app

import "time"

type Options struct {
	Target             string
	LegacyNamespace    string
	Namespace          string
	Context            string
	Status             bool
	StatusInterval     time.Duration
	StatusTimeout      time.Duration
	Selector           string
	Container          string
	Detail             bool
	Tail               int
	TailSet            bool
	BufferLines        int
	Since              string
	RefreshInterval    time.Duration
	HeartbeatWindow    time.Duration
	Include            []string
	Exclude            []string
	NoDefaultExclude   bool
	NoFollow           bool
	DumpRequested      bool
	DumpDirectory      string
	DeploymentDump     bool
	DeploymentDumpPath string
	ShowPod            *bool
	SplitPanes         bool
	TileWindows        bool
	LiveFilter         bool
	FilterFile         string
	NoColor            bool
}
