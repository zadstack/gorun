//go:build !unix

package cli

import (
	"errors"
	"os"
)

func lockUpgradeDirectory(string) (*os.File, error) {
	return nil, errors.New("binary upgrades require Linux or macOS")
}
