package store

import "errors"

var (
	// ErrNotFound means the key or the requested version does not exist.
	ErrNotFound = errors.New("not found")

	// ErrNotMigrated means the lite_config_meta seed row is missing:
	// Migrate has not run, or the row was deleted.
	ErrNotMigrated = errors.New("schema not migrated: lite_config_meta row id=1 is missing")

	// ErrInvalidKey means the key violates ValidateKey's charset or length limits.
	ErrInvalidKey = errors.New("invalid key")

	// ErrInvalidValue means the value is too large, or does not parse as its format.
	ErrInvalidValue = errors.New("invalid value")

	// ErrInvalidChange means an audit field exceeds its column width.
	ErrInvalidChange = errors.New("invalid change metadata")

	// ErrUnsupportedDriver means no dialect implements that driver name.
	ErrUnsupportedDriver = errors.New("unsupported driver")
)
