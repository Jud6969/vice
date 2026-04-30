// client/replay/prune.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package replay

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileEntry is a replay file in a directory.
type FileEntry struct {
	Path  string
	MTime time.Time
	Size  int64
}

// ListMostRecent lists *.bin files in dir, newest first.
func ListMostRecent(dir string) ([]FileEntry, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []FileEntry
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".bin") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, FileEntry{
			Path:  filepath.Join(dir, e.Name()),
			MTime: info.ModTime(),
			Size:  info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].MTime.After(out[j].MTime)
	})
	return out, nil
}

// Prune deletes the oldest .bin files in dir, keeping at most `keep`. Returns
// the list of deleted paths.
func Prune(dir string, keep int) ([]string, error) {
	if keep < 0 {
		keep = 0
	}
	entries, err := ListMostRecent(dir)
	if err != nil {
		return nil, err
	}
	if len(entries) <= keep {
		return nil, nil
	}
	var deleted []string
	for _, e := range entries[keep:] {
		if err := os.Remove(e.Path); err != nil {
			return deleted, fmt.Errorf("remove %q: %w", e.Path, err)
		}
		deleted = append(deleted, e.Path)
	}
	return deleted, nil
}
