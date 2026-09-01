package lite

// Config is one entry in a snapshot.
//
// Three fields only: this is the client hot path, and updated_at /
// updated_by contribute nothing to decoding.
type Config struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Format string `json:"format"`
}

// Snapshot is one full fetch and Source's return type.
// It lives in the SDK for third-party Source implementations; server/api keeps
// a field-compatible type without introducing a server dependency.
//
// Revision must be read before Configs. Reversing the order can label stale
// data with a fresh revision and permanently skip a change; a low label
// self-corrects on the next poll.
type Snapshot struct {
	Revision int64    `json:"revision"`
	Configs  []Config `json:"configs"`
}
