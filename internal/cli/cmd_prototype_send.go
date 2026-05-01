package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
			path, err := registry.DefaultPath()
			if err != nil {
				return err
			}
			entry, err := (&registry.Registry{Path: path}).Lookup(project, name)
			if err != nil {
				return err
			}

			body, _ := json.Marshal(map[string]string{
				"session": session,
				"text":    text,
			})
			ctx := cmd.Context()
			if ctx == nil {
				ctx = cmd.Root().Context()
			}
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
