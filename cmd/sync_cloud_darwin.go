package cmd

import (
	"io/fs"
	"syscall"
)

// sfDataless is SF_DATALESS from <sys/stat.h>: the file's contents are not on
// this disk and are fetched when it is first read.
const sfDataless = 0x40000000

func datalessFile(info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Flags&sfDataless != 0
}
