package cli

import (
	"encoding/json"
	"os"
)

// jsonEncoder returns a stdout JSON encoder with readable indentation.
func jsonEncoder() *json.Encoder {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc
}
