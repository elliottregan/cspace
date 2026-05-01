package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/spf13/cobra"
)

func newPrototypeSendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prototype-send <name>[:<session>] <text>",
		Short: "P0: inject a user turn into a sandbox session",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, text := args[0], args[1]

			name, session := target, "primary"
			if i := strings.Index(target, ":"); i >= 0 {
				name, session = target[:i], target[i+1:]
			}

			project := projectName()

			entry, err := resolveEntry(project, name)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = cmd.Root().Context()
			}

			body, _ := json.Marshal(map[string]string{
				"session": session,
				"text":    text,
			})
			req, _ := http.NewRequestWithContext(ctx,
				"POST", entry.ControlURL+"/send", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if entry.Token != "" {
				req.Header.Set("Authorization", "Bearer "+entry.Token)
			}

			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("post /send: %w", err)
			}
			defer resp.Body.Close()
			out, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != 200 {
				return fmt.Errorf("status %d: %s", resp.StatusCode, out)
			}
			fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(string(out)))
			return nil
		},
	}
}

// resolveEntry looks up a sandbox entry. When CSPACE_REGISTRY_URL is set
// (in-sandbox path), it queries the host registry-daemon over HTTP. Otherwise
// it falls back to the local sandbox-registry.json file (host path).
func resolveEntry(project, name string) (registry.Entry, error) {
	if rURL := os.Getenv("CSPACE_REGISTRY_URL"); rURL != "" {
		client := &http.Client{Timeout: 5 * time.Second}
		url := fmt.Sprintf("%s/lookup/%s/%s", strings.TrimRight(rURL, "/"), project, name)
		resp, err := client.Get(url)
		if err != nil {
			return registry.Entry{}, fmt.Errorf("registry daemon %s: %w", rURL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			return registry.Entry{}, fmt.Errorf("registry lookup status %d: %s",
				resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var entry registry.Entry
		if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
			return registry.Entry{}, fmt.Errorf("decode registry response: %w", err)
		}
		// The daemon doesn't populate Project/Name (they're map keys);
		// patch them in case downstream code wants them.
		entry.Project, entry.Name = project, name
		return entry, nil
	}

	path, err := registry.DefaultPath()
	if err != nil {
		return registry.Entry{}, err
	}
	return (&registry.Registry{Path: path}).Lookup(project, name)
}
