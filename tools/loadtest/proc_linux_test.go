//go:build linux

package main

import (
	"os"
	"testing"
)

func TestSampleProcessSelf(t *testing.T) {
	if !processSamplerAvailable() {
		t.Fatal("expected linux sampler")
	}
	rss, cpu, err := sampleProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if rss < 1<<20 {
		t.Fatalf("rss too small: %d", rss)
	}
	if cpu < 0 {
		t.Fatalf("cpu=%v", cpu)
	}
}
