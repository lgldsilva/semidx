//go:build windows

package localstore

import "os"

// Schema init is still guarded by SQLite busy_timeout and IF NOT EXISTS DDL;
// cross-process flock is Unix-only (unix.Flock).
func flockExclusive(*os.File) error { return nil }

func flockUnlock(*os.File) error { return nil }
