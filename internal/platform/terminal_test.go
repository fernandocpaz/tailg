package platform

import (
	"math"
	"strconv"
	"testing"
)

func TestSplitPaneArgsKeepPaneAreasBalanced(t *testing.T) {
	pods := []string{"pod-1", "pod-2", "pod-3", "pod-4"}
	args := splitPaneArgs(pods, func(pod string) []string { return []string{"tailg", pod} })

	var sizes []float64
	for index, arg := range args {
		if arg != "--size" {
			continue
		}
		if index+1 >= len(args) {
			t.Fatal("--size has no value")
		}
		size, err := strconv.ParseFloat(args[index+1], 64)
		if err != nil {
			t.Fatalf("invalid pane size %q: %v", args[index+1], err)
		}
		sizes = append(sizes, size)
	}

	wantSizes := []float64{0.75, 2.0 / 3.0, 0.5}
	if len(sizes) != len(wantSizes) {
		t.Fatalf("sizes=%v, want %v", sizes, wantSizes)
	}
	for index := range wantSizes {
		if math.Abs(sizes[index]-wantSizes[index]) > 0.000001 {
			t.Fatalf("sizes[%d]=%v, want %v", index, sizes[index], wantSizes[index])
		}
	}

	remainingArea := 1.0
	for index, size := range sizes {
		paneArea := remainingArea * (1 - size)
		if math.Abs(paneArea-0.25) > 0.000001 {
			t.Fatalf("pane %d area=%v, want 0.25", index, paneArea)
		}
		remainingArea *= size
	}
	if math.Abs(remainingArea-0.25) > 0.000001 {
		t.Fatalf("last pane area=%v, want 0.25", remainingArea)
	}
}

func TestSplitPaneArgsRemoveDuplicatePods(t *testing.T) {
	args := splitPaneArgs([]string{"pod-1", "pod-1", "pod-2"}, func(pod string) []string { return []string{"tailg", pod} })

	splits := 0
	for _, arg := range args {
		if arg == "split-pane" {
			splits++
		}
	}
	if splits != 1 {
		t.Fatalf("split-pane count=%d, want 1; args=%v", splits, args)
	}
}
