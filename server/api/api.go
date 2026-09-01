// Package api defines stdlib-only HTTP wire types shared by server and lsctl.
// The client keeps independent types to avoid importing server dependencies;
// TestWireCompatibility guards their compatibility.
package api

import "time"

// Config is one minimal snapshot entry.
type Config struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Format string `json:"format"`
}

// Snapshot is one full fetch.
// Revision must be read before Configs: reversing the order can label stale
// data with a fresh revision and permanently skip a change. A low label is
// corrected by the next poll.
type Snapshot struct {
	Revision int64    `json:"revision"`
	Configs  []Config `json:"configs"`
}

// ConfigDetail is the full record of one entry, for humans, off the hot path.
type ConfigDetail struct {
	Config
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
}

// HistoryEntry is one recorded version.
type HistoryEntry struct {
	Version   int64     `json:"version"`
	Value     string    `json:"value"`
	Format    string    `json:"format"`
	Op        string    `json:"op"`
	Comment   string    `json:"comment,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

// WriteResult is returned by set, delete, and rollback.
type WriteResult struct {
	Key      string `json:"key"`
	Version  int64  `json:"version"`
	Revision int64  `json:"revision"`
}

// RollbackRequest is the rollback request body.
type RollbackRequest struct {
	Version int64 `json:"version"`
}

// RevisionResponse carries the current watermark.
type RevisionResponse struct {
	Revision int64 `json:"revision"`
}

// Health is the /healthz response.
type Health struct {
	OK       bool   `json:"ok"`
	Revision int64  `json:"revision"`
	DBError  string `json:"db_error,omitempty"`
}

// ErrorResponse is the shape of every non-2xx response.
type ErrorResponse struct {
	Error string `json:"error"`
}
