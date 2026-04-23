// internal/tui/helpers.go
package tui

import (
	"encoding/json"
	"io"
	"os"
)

func getenv(key string) string { return os.Getenv(key) }

func jsonDecode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}
