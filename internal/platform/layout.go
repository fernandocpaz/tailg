package platform

import "math"

type Rect struct{ X, Y, Width, Height int }

func TiledRectangles(count int, workArea Rect) []Rect {
	if count <= 0 {
		return nil
	}
	columns := max(1, int(math.Ceil(math.Sqrt(float64(count)))))
	rows := (count + columns - 1) / columns
	result := make([]Rect, 0, count)
	for index := 0; index < count; index++ {
		row, column := index/columns, index%columns
		windowsInRow := min(columns, count-row*columns)
		x1 := workArea.X + workArea.Width*column/windowsInRow
		x2 := workArea.X + workArea.Width*(column+1)/windowsInRow
		y1 := workArea.Y + workArea.Height*row/rows
		y2 := workArea.Y + workArea.Height*(row+1)/rows
		result = append(result, Rect{X: x1, Y: y1, Width: x2 - x1, Height: y2 - y1})
	}
	return result
}
