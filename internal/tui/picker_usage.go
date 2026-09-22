package tui

import (
	"sort"
	"time"
)

const frequentChoices = 5

type ChoiceUsage struct {
	Count    int       `json:"count"`
	LastUsed time.Time `json:"lastUsed"`
}

// rankPickerNames keeps the active choice visible and places frequently used
// alternatives first. Ties favor recent use, then stable alphabetical order.
func rankPickerNames(names []string, current string, usage map[string]ChoiceUsage) []string {
	ranked := append([]string(nil), names...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if (ranked[i] == current) != (ranked[j] == current) {
			return ranked[i] == current
		}
		left, right := usage[ranked[i]], usage[ranked[j]]
		if left.Count != right.Count {
			return left.Count > right.Count
		}
		if !left.LastUsed.Equal(right.LastUsed) {
			return left.LastUsed.After(right.LastUsed)
		}
		return ranked[i] < ranked[j]
	})
	return ranked
}

func visibleChoiceCount(total int, expanded bool) int {
	if !expanded && total > frequentChoices {
		return frequentChoices + 1 // The final row is More, not a choice.
	}
	return total
}

func isMoreChoice(index, total int, expanded bool) bool {
	return !expanded && total > frequentChoices && index == frequentChoices
}
