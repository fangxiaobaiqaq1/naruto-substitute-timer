package capture

import "fmt"

// Target is the stable, provider-neutral identity of an emulator instance.
// Provider-specific fields are kept in the concrete target configuration.
type Target struct {
	Provider string `json:"provider"`
	StableID string `json:"stable_id"`
	Name     string `json:"name,omitempty"`
	Running  bool   `json:"running"`
	PID      int    `json:"pid,omitempty"`
}

func (t Target) Label() string {
	name := t.Name
	if name == "" {
		name = t.StableID
	}
	if t.Provider == "" {
		return name
	}
	return fmt.Sprintf("%s · %s", t.Provider, name)
}
