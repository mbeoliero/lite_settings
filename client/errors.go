package lite

import "errors"

var (
	// ErrNotFound means the snapshot has no such key.
	ErrNotFound = errors.New("lite: config not found")
	// ErrDecode means the value exists but does not decode into the requested type.
	ErrDecode = errors.New("lite: config decode failed")
	// ErrNoSnapshot means the first fetch failed with no fallback available,
	// so the client holds no usable configuration.
	ErrNoSnapshot = errors.New("lite: no config snapshot available")
)
