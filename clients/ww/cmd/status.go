package cmd

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/skthomasjr/autonomous-agent/clients/ww/internal/client"
	"github.com/skthomasjr/autonomous-agent/clients/ww/internal/output"
	"github.com/spf13/cobra"
)

// agentEntry mirrors the subset of /agents we care about.
type agentEntry struct {
	ID    string                 `json:"id"`
	URL   string                 `json:"url"`
	Role  string                 `json:"role"`
	Model string                 `json:"model,omitempty"`
	Card  map[string]interface{} `json:"card,omitempty"`
}

type statusRow struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	URL      string `json:"url"`
	Healthy  bool   `json:"healthy"`
	Status   string `json:"status"`
	Latency  string `json:"latency,omitempty"`
	LastSeen string `json:"last_seen,omitempty"`
	Error    string `json:"error,omitempty"`
}

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "List team members and health",
		Long: "Fetches /agents from the harness to discover the team, then probes each\n" +
			"member's /health endpoint concurrently and prints a consolidated table.",
		RunE: func(cc *cobra.Command, args []string) error {
			ctx := cc.Context()
			c := ClientFromCtx(ctx)
			out := OutFromCtx(ctx)

			var agents []agentEntry
			if err := c.DoJSON(ctx, http.MethodGet, "/agents", nil, &agents, false); err != nil {
				return handleErr(out, err)
			}

			rows := probeAll(ctx, c, agents)

			if out.IsJSON() {
				return out.EmitJSON(rows)
			}
			headers := []string{"NAME", "ROLE", "URL", "STATUS", "LATENCY"}
			data := make([][]string, 0, len(rows))
			for _, r := range rows {
				status := "UP"
				if !r.Healthy {
					status = "DOWN"
				}
				data = append(data, []string{r.Name, r.Role, r.URL, status, r.Latency})
			}
			output.Table(out.Out, headers, data)
			return nil
		},
	}
	return cmd
}

func probeAll(ctx context.Context, c *client.Client, agents []agentEntry) []statusRow {
	rows := make([]statusRow, len(agents))
	var wg sync.WaitGroup
	for i, a := range agents {
		wg.Add(1)
		go func(i int, a agentEntry) {
			defer wg.Done()
			rows[i] = probeOne(ctx, c, a)
		}(i, a)
	}
	wg.Wait()
	return rows
}

func probeOne(ctx context.Context, c *client.Client, a agentEntry) statusRow {
	row := statusRow{Name: a.ID, Role: a.Role, URL: a.URL}
	if a.URL == "" {
		row.Status = "no URL"
		return row
	}
	start := time.Now()
	// Use GetBytes on the absolute URL so Resolve treats it as-is.
	hc := c.HTTP()
	u := strings.TrimRight(a.URL, "/") + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		row.Error = err.Error()
		return row
	}
	if tok := c.Token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.Header.Set("User-Agent", "ww/"+Version)
	resp, err := hc.Do(req)
	if err != nil {
		row.Error = err.Error()
		row.Status = "error"
		return row
	}
	defer resp.Body.Close()
	row.Latency = fmt.Sprintf("%dms", time.Since(start).Milliseconds())
	row.Healthy = resp.StatusCode >= 200 && resp.StatusCode < 300
	row.Status = resp.Status
	return row
}
