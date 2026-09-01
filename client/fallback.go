// The on-disk snapshot is only a cold-start fallback; normal reads stay in memory.
package lite

import (
	jsonv2 "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
)

// Permissions are owner-only. A snapshot is the whole configuration,
// credentials and tokens included, and the default path can land in a
// directory other local accounts can read.
const (
	fallbackFileMode = 0o600
	fallbackDirMode  = 0o700
)

// writeFallback uses write-then-rename so interruption leaves the previous
// complete snapshot intact.
func writeFallback(path string, s *view) error {
	if err := os.MkdirAll(filepath.Dir(path), fallbackDirMode); err != nil {
		return fmt.Errorf("create fallback directory: %w", err)
	}
	data, err := jsonv2.Marshal(s.toWire())
	if err != nil {
		return fmt.Errorf("marshal fallback file: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, fallbackFileMode); err != nil {
		return fmt.Errorf("write fallback file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace fallback file: %w", err)
	}
	// Rename preserves an existing destination mode, so tighten legacy files too.
	if err := os.Chmod(path, fallbackFileMode); err != nil {
		return fmt.Errorf("restrict fallback file permissions: %w", err)
	}
	return nil
}

func readFallback(path string) (*view, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := jsonv2.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse fallback file %s: %w", path, err)
	}
	return newView(&s), nil
}
