package services

import (
	"slices"
	"testing"
)

func TestDefaultHasFiveServices(t *testing.T) {
	r := Default(testDeps(t))
	var names []string
	for _, s := range r.list {
		names = append(names, s.Name())
	}
	if want := []string{"mail", "storage", "chat", "analytics", "backups"}; !slices.Equal(names, want) {
		t.Fatalf("services %v, want %v", names, want)
	}
}
