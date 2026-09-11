package kube

import (
	"strings"
	"unicode/utf8"
)

// formatTable renders plain ASCII so status output remains readable in a
// terminal, CI log, or redirected file. Cells wrap instead of losing text.
func formatTable(headers []string, rows [][]string, maximums []int) string {
	widths := make([]int, len(headers))
	for column, header := range headers {
		widths[column] = runeWidth(header)
	}
	for _, row := range rows {
		for column := range headers {
			if column < len(row) {
				widths[column] = max(widths[column], longestLine(row[column]))
			}
		}
	}
	for column, maximum := range maximums {
		if column < len(widths) && maximum > 0 {
			widths[column] = min(widths[column], maximum)
		}
	}

	border := tableBorder(widths)
	lines := []string{border, tableLine(headers, widths), border}
	for _, row := range rows {
		wrapped := make([][]string, len(headers))
		height := 1
		for column := range headers {
			value := ""
			if column < len(row) {
				value = row[column]
			}
			wrapped[column] = wrapCell(value, widths[column])
			height = max(height, len(wrapped[column]))
		}
		for line := 0; line < height; line++ {
			values := make([]string, len(headers))
			for column := range headers {
				if line < len(wrapped[column]) {
					values[column] = wrapped[column][line]
				}
			}
			lines = append(lines, tableLine(values, widths))
		}
		lines = append(lines, border)
	}
	return strings.Join(lines, "\n") + "\n"
}

func tableBorder(widths []int) string {
	var builder strings.Builder
	builder.WriteByte('+')
	for _, width := range widths {
		builder.WriteString(strings.Repeat("-", width+2))
		builder.WriteByte('+')
	}
	return builder.String()
}

func tableLine(values []string, widths []int) string {
	var builder strings.Builder
	builder.WriteByte('|')
	for column, width := range widths {
		value := ""
		if column < len(values) {
			value = values[column]
		}
		builder.WriteByte(' ')
		builder.WriteString(value)
		builder.WriteString(strings.Repeat(" ", max(0, width-runeWidth(value))))
		builder.WriteString(" |")
	}
	return builder.String()
}

func wrapCell(value string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	var result []string
	for _, sourceLine := range strings.Split(value, "\n") {
		words := strings.Fields(sourceLine)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}
		line := ""
		for _, word := range words {
			for runeWidth(word) > width {
				if line != "" {
					result = append(result, line)
					line = ""
				}
				chunk, rest := splitRunes(word, width)
				result = append(result, chunk)
				word = rest
			}
			if word == "" {
				continue
			}
			if line == "" {
				line = word
			} else if runeWidth(line)+1+runeWidth(word) <= width {
				line += " " + word
			} else {
				result = append(result, line)
				line = word
			}
		}
		if line != "" {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return []string{""}
	}
	return result
}

func splitRunes(value string, width int) (string, string) {
	runes := []rune(value)
	cut := width
	// Pod and resource names are commonly hyphenated. Prefer a natural break
	// near the edge while retaining the delimiter and every character.
	for index := width - 1; index >= width/2; index-- {
		if runes[index] == '-' || runes[index] == '/' {
			cut = index + 1
			break
		}
	}
	return string(runes[:cut]), string(runes[cut:])
}

func longestLine(value string) int {
	longest := 0
	for _, line := range strings.Split(value, "\n") {
		longest = max(longest, runeWidth(line))
	}
	return longest
}

func runeWidth(value string) int { return utf8.RuneCountInString(value) }
