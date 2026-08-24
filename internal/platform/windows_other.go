//go:build !windows

package platform

import "time"

func visibleWindows() map[uintptr]string                                         { return map[uintptr]string{} }
func waitForWindows(_ map[uintptr]string, _ []string, _ time.Duration) []uintptr { return nil }
func tileHandles(_ []uintptr) int                                                { return 0 }
