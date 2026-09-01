package lite

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// Group is a read-only, concurrency-safe set of configs under one prefix.
// It stays bound to its immutable snapshot, preventing mixed-version reads;
// call Client.Group again for the latest snapshot.
type Group struct {
	prefix string
	snap   *view
	strict bool
	log    *slog.Logger
}

// Prefix returns the group's prefix.
func (g Group) Prefix() string { return g.prefix }

// Revision returns the bound snapshot's revision.
func (g Group) Revision() int64 { return g.snap.revision }

// Keys returns sorted relative keys suitable for Get.
// The result is a fresh slice: the snapshot's own key list stays immutable.
func (g Group) Keys() []string {
	lo, hi := g.snap.prefixRange(g.prefix)
	out := make([]string, 0, hi-lo)
	for _, k := range g.snap.keys[lo:hi] {
		out = append(out, strings.TrimPrefix(k, g.prefix))
	}
	return out
}

// Len returns the number of configs in the group.
func (g Group) Len() int {
	lo, hi := g.snap.prefixRange(g.prefix)
	return hi - lo
}

// Raw returns the raw text stored under a relative key.
func (g Group) Raw(key string) (string, bool) {
	c, ok := g.snap.byKey[g.prefix+key]
	if !ok {
		return "", false
	}
	return c.Value, true
}

// Get decodes the value at a relative key into T.
// Results are cached per snapshot and type; pointers, slices, and maps may
// therefore be shared and must be copied before modification.
func (g Group) Get[T any](key string) (T, error) {
	full := g.prefix + key
	c, ok := g.snap.byKey[full]
	if !ok {
		var zero T
		return zero, fmt.Errorf("%w: %s", ErrNotFound, full)
	}
	v, err := cachedDecode[T](g.snap, full, c, g.strict)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("%s: %w", full, err)
	}
	return v, nil
}

// GetOr decodes a relative key, returning def on failure and inferring T from def.
// Missing keys are silent; decode failures are logged so malformed values do
// not masquerade as absent configuration.
func (g Group) GetOr[T any](key string, def T) T {
	v, err := g.Get[T](key)
	if err != nil {
		if !errors.Is(err, ErrNotFound) && g.log != nil {
			g.log.Warn("config decode failed; using default", "key", g.prefix+key, "err", err)
		}
		return def
	}
	return v
}
