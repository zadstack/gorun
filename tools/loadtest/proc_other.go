//go:build !linux

package main

import "fmt"

func processSamplerAvailable() bool { return false }

func sampleProcess(pid int) (rssBytes int64, cpuSeconds float64, err error) {
	return 0, 0, fmt.Errorf("process sampling not supported on this OS (pid %d)", pid)
}
