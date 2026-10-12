//go:build !linux

package monitor

import (
	"fmt"
	"os"
)

// Production runs on Linux/WSL. Other platforms are suitable for parser tests;
// production inode tracking requires the Linux implementation.
func fileIdentity(info os.FileInfo) string {
	return fmt.Sprintf("%s:%s", info.Name(), info.ModTime().UTC())
}
