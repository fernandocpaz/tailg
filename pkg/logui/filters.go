package logui

import (
 "github.com/fernandocpaz/tailg/internal/tui"
)

// InitializeSharedFilter creates the exact filter state files understood by
// tailg's F1 and live filter UI. External sources may pass the same FilterFile
// to multiple Run sessions, including separate Windows Terminal processes.
// Filter text and matches-only mode then stay synchronized across panes.
func InitializeSharedFilter(path,initialText string)error{
 return tui.InitializeSharedFilterWithText(path,initialText)
}
