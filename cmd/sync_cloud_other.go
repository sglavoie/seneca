//go:build !darwin

package cmd

import "io/fs"

// Only macOS keeps files whose contents are elsewhere.
func datalessFile(fs.FileInfo) bool { return false }
