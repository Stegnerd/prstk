package plan

import "path/filepath"

// ArchiveRoot is where torn-down initiatives live: <repoDir>/_archive.
func ArchiveRoot(repoDir string) string { return filepath.Join(repoDir, "_archive") }

// ArchivePath is the archived copy of initiative dir name:
// <repoDir>/_archive/<name>. Legacy single-file archives
// (<repoDir>/_archive/<name>.md) may sit alongside it.
func ArchivePath(repoDir, name string) string { return filepath.Join(ArchiveRoot(repoDir), name) }
