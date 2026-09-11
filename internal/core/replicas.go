package core

// ReplicaExplanation is a compact, evidence based explanation of the
// controller and replica state for one pod. Details and Warnings are kept
// separate so callers can show a useful one-line summary while still making
// incomplete API observations visible.
type ReplicaExplanation struct {
	Pod      string
	Workload string
	Summary  string
	Details  []string
	Warnings []string
}
