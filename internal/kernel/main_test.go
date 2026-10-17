package kernel

import (
	"io"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	LogOutput = io.Discard
	os.Exit(m.Run())
}
