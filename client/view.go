package lite

import (
	"reflect"
	"slices"
	"strings"
	"sync"
)

// view is an immutable in-memory snapshot. Wholesale replacement lets readers
// retain a self-consistent view without locks.
type view struct {
	revision int64
	byKey    map[string]Config
	keys     []string // sorted, so a prefix's keys form one contiguous run

	// dec caches decode results for this snapshot, keyed by (key, type).
	// Hanging it off the snapshot rather than the Client means there is
	// no invalidation logic: replacing the snapshot drops the cache.
	dec sync.Map
}

func newView(s *Snapshot) *view {
	out := &view{
		revision: s.Revision,
		byKey:    make(map[string]Config, len(s.Configs)),
		keys:     make([]string, 0, len(s.Configs)),
	}
	for _, c := range s.Configs {
		out.byKey[c.Key] = c
		out.keys = append(out.keys, c.Key)
	}
	slices.Sort(out.keys)
	return out
}

func emptyView() *view {
	// -1 means "no baseline", matching the server's default revision for
	// /v1/watch: no real revision equals it, so the first poll always
	// returns data.
	return &view{revision: -1, byKey: map[string]Config{}}
}

type decodeKey struct {
	key string
	typ reflect.Type
}

type decodeResult struct {
	val any
	err error
}

// cachedDecode memoizes successes and failures by key and type.
// Snapshot replacement discards the cache, so no TTL or invalidation is needed.
// strict stays out of the key: it is fixed for the owning Client, so a
// per-Group strict would have to join decodeKey.
func cachedDecode[T any](v *view, key string, c Config, strict bool) (T, error) {
	k := decodeKey{key: key, typ: reflect.TypeFor[T]()}
	if hit, ok := v.dec.Load(k); ok {
		return cached[T](hit)
	}
	val, err := decode[T](c.Value, c.Format, strict)
	// LoadOrStore, not Store: racing goroutines would otherwise hand callers
	// different pointers for one (key, type). The first writer wins.
	hit, _ := v.dec.LoadOrStore(k, decodeResult{val: val, err: err})
	return cached[T](hit)
}

// cached unpacks an entry. A cached failure holds a nil val, so the assertion
// yields T's zero value — what the error path wants.
func cached[T any](hit any) (T, error) {
	r := hit.(decodeResult)
	val, _ := r.val.(T)
	return val, r.err
}

// prefixRange returns the half-open index range of keys carrying prefix.
// keys is sorted, so the matches are contiguous and each bound is one binary
// search instead of an O(n) scan per Group.Keys or Group.Len.
func (s *view) prefixRange(prefix string) (lo, hi int) {
	lo, _ = slices.BinarySearch(s.keys, prefix)
	// Reporting a prefixed key as "less" settles the search on the first key
	// past the run.
	hi, _ = slices.BinarySearchFunc(s.keys, prefix, func(k, p string) int {
		if strings.HasPrefix(k, p) {
			return -1
		}
		return strings.Compare(k, p)
	})
	return lo, hi
}

func (s *view) toWire() *Snapshot {
	out := &Snapshot{Revision: s.revision, Configs: make([]Config, 0, len(s.keys))}
	for _, k := range s.keys {
		out.Configs = append(out.Configs, s.byKey[k])
	}
	return out
}

// diff returns sorted added, modified, or deleted keys.
// Full snapshots imply deletion when a previously present key is absent.
func diff(old, new *view) []string {
	var changed []string
	for k, nc := range new.byKey {
		oc, ok := old.byKey[k]
		if !ok || oc.Value != nc.Value || oc.Format != nc.Format {
			changed = append(changed, k)
		}
	}
	for k := range old.byKey {
		if _, ok := new.byKey[k]; !ok {
			changed = append(changed, k)
		}
	}
	slices.Sort(changed)
	return changed
}
