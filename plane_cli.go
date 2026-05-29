package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
)

// runPlaneCLI handles `featmap plane sync ...`. It is a thin HTTP client over the
// REST endpoint (SYNC-032), so it shares one code path with every other trigger.
func runPlaneCLI(args []string) {
	if len(args) < 1 || args[0] != "sync" {
		fmt.Fprintln(os.Stderr, "usage: featmap plane sync --url <base> --key <api-key> --workspace <ws-id> --project <id> [--feature <id>] [--json]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("plane sync", flag.ExitOnError)
	urlF := fs.String("url", "http://localhost:5000", "featmap server base URL")
	keyF := fs.String("key", os.Getenv("FEATMAP_API_KEY"), "featmap API key (or FEATMAP_API_KEY)")
	wsF := fs.String("workspace", "", "workspace id (Workspace header)")
	projF := fs.String("project", "", "featmap project id")
	featF := fs.String("feature", "", "optional feature id to scope the sync")
	jsonF := fs.Bool("json", false, "emit raw JSON")
	_ = fs.Parse(args[1:])

	if *projF == "" || *keyF == "" || *wsF == "" {
		fmt.Fprintln(os.Stderr, "error: --project, --key, and --workspace are required")
		os.Exit(2)
	}

	endpoint := fmt.Sprintf("%s/v1/projects/%s/plane/sync", *urlF, *projF)
	if *featF != "" {
		endpoint += "?feature_id=" + *featF
	}
	req, _ := http.NewRequest("POST", endpoint, bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer "+*keyF)
	req.Header.Set("Workspace", *wsF)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "server returned %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}
	if *jsonF {
		fmt.Println(string(body))
		return
	}
	var res SyncResult
	if err := json.Unmarshal(body, &res); err != nil {
		fmt.Println(string(body))
		return
	}
	fmt.Printf("synced: pushed=%d pulled=%d across %d link(s)\n", res.Pushed, res.Pulled, len(res.PerLink))
	for _, l := range res.PerLink {
		fmt.Printf("  link %s: %s pushed=%d pulled=%d %s\n", l.FeatureID, l.Status, l.Pushed, l.Pulled, l.Error)
	}
}
