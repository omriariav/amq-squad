package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// presenceFile mirrors AMQ's presence.json shape.
type presenceFile struct {
	Schema   int       `json:"schema"`
	Handle   string    `json:"handle"`
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen"`
}

func presencePath(agentDir string) string {
	return filepath.Join(agentDir, "presence.json")
}

func readPresence(agentDir string) (presenceFile, error) {
	data, err := os.ReadFile(presencePath(agentDir))
	if err != nil {
		return presenceFile{}, err
	}
	var pres presenceFile
	if err := json.Unmarshal(data, &pres); err != nil {
		return presenceFile{}, err
	}
	return pres, nil
}
