package db

import (
	"testing"
	"time"
)

func TestNewIDIsULIDSortable(t *testing.T) {
	a, b := NewID(), NewID()
	if len(a) != 26 || a >= b {
		t.Fatal(a, b)
	}
}

func TestNowIsRFC3339UTC(t *testing.T) {
	s := Now()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil || ts.Location() != time.UTC || s[len(s)-1] != 'Z' {
		t.Fatalf("%q %v", s, err)
	}
}
