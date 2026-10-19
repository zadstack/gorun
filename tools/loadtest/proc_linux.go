//go:build linux

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func processSamplerAvailable() bool { return true }

// sampleProcess returns RSS bytes and cumulative CPU seconds (user+system)
// for pid from /proc.
func sampleProcess(pid int) (rssBytes int64, cpuSeconds float64, err error) {
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, e := strconv.ParseInt(fields[1], 10, 64)
				if e != nil {
					return 0, 0, e
				}
				rssBytes = kb * 1024
			}
			break
		}
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return rssBytes, 0, err
	}
	// After comm (in parens), fields: … utime(14) stime(15) — 1-indexed in man proc.
	closeParen := strings.LastIndex(string(stat), ")")
	if closeParen < 0 {
		return rssBytes, 0, fmt.Errorf("parse /proc/%d/stat", pid)
	}
	fields := strings.Fields(string(stat)[closeParen+1:])
	// fields[0] is state → utime is fields[11], stime fields[12] (0-indexed after comm).
	if len(fields) < 13 {
		return rssBytes, 0, fmt.Errorf("short /proc/%d/stat", pid)
	}
	utime, err1 := strconv.ParseUint(fields[11], 10, 64)
	stime, err2 := strconv.ParseUint(fields[12], 10, 64)
	if err1 != nil || err2 != nil {
		return rssBytes, 0, fmt.Errorf("cpu ticks: %v %v", err1, err2)
	}
	// Linux USER_HZ is almost always 100.
	const userHz = 100.0
	cpuSeconds = float64(utime+stime) / userHz
	return rssBytes, cpuSeconds, nil
}
