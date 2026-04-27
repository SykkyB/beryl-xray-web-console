//go:build !linux

package logs

import "os"

// On non-Linux build hosts (developers' macs) we can't read the inode
// out of FileInfo's Sys() generically. Returning 0 disables rotation
// detection by inode — the size-shrunk path still works.
func inodeOf(_ os.FileInfo) uint64 { return 0 }
