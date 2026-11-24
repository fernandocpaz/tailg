package tui

import "time"

// A fixed-width ASCII spinner works in Windows Terminal, monochrome and
// narrow panes. Existing log and monitor ticks drive the redraw; no polling
// or network request is initiated by the animation itself.
const loadingFrames = "|/-\\"

func loadingLabelAt(label string, at time.Time) string {
 frame := int(at.UnixMilli()/250 % int64(len(loadingFrames)))
 if frame < 0 { frame += len(loadingFrames) }
 return "[" + string(loadingFrames[frame]) + "] " + label
}

func loadingLabel(label string) string { return loadingLabelAt(label,time.Now()) }
