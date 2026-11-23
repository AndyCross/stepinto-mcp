// Package harness tests the stepinto MCP server from the outside, the way a
// client sees it: the real binary, started as a process, spoken to over
// stdio. Nothing here needs a model, a key or the network.
//
//	go test ./harness                    check everything
//	go test ./harness -update            accept the current contract and outputs
//	go test ./harness -record            re-record the conversations (needs ANTHROPIC_API_KEY)
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	replayable "github.com/AndyCross/claude-like-replayable"
	"github.com/AndyCross/stepinto-mcp/agent"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	update = flag.Bool("update", false, "write the current contract and outputs as the expected ones")
	record = flag.Bool("record", false, "record the conversations again through the real API")
)

var (
	binary string // the server, built once
	siteURL string // a copy of the site served from testdata/site
)

func TestMain(m *testing.M) {
	flag.Parse()
	dir, err := os.MkdirTemp("", "stepinto-mcp")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "stepinto-mcp")
	build := exec.Command("go", "build", "-o", binary, "../cmd/stepinto-mcp")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	site := httptest.NewServer(http.FileServer(http.Dir("../testdata/site")))
	siteURL = site.URL
	code := m.Run()
	site.Close()
	os.RemoveAll(dir)
	os.Exit(code)
}

// connect starts the server against the test copy of the site and opens a
// session with it over stdio, exactly as a desktop client would.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "harness", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(binary, "-base", siteURL)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// golden compares got with the file, or writes it with -update. fix says
// what to do when they differ.
func golden(t *testing.T, path string, got any, fix string) {
	t.Helper()
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	path = filepath.Join("..", "testdata", path)
	if *update {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(want, b) {
		t.Errorf("%s differs from what the server gives now; %s\n%s", path, fix, diff(string(want), string(b)))
	}
}

// diff shows the lines that differ, which is enough for files this size.
func diff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var out []string
	for i := 0; i < max(len(w), len(g)); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			out = append(out, fmt.Sprintf("line %d\n  want %s\n  got  %s", i+1, a, b))
		}
		if len(out) == 10 {
			out = append(out, "...")
			break
		}
	}
	return strings.Join(out, "\n")
}

// TestContract holds what the model reads: every tool's name, description
// and schemas. A change to any of them changes what the model does, so it
// fails here until someone looks at it and accepts it with -update, and the
// accepted file shows up in review like any other change to an API.
func TestContract(t *testing.T) {
	session := connect(t)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	type tool struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		InputSchema  any    `json:"inputSchema"`
		OutputSchema any    `json:"outputSchema,omitempty"`
	}
	var tools []tool
	for _, t := range res.Tools {
		tools = append(tools, tool{t.Name, t.Description, t.InputSchema, t.OutputSchema})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	golden(t, "contract/tools.json", tools, "the model will read something different. If that is intended, run with -update and record the conversations again")
}

// TestTools calls each tool directly and compares the whole result, errors
// included, with the saved one.
func TestTools(t *testing.T) {
	cases := []struct {
		name, tool string
		args       map[string]any
	}{
		{"search-testing", "search_posts", map[string]any{"query": "testing mocks", "limit": 3}},
		{"search-in-lane", "search_posts", map[string]any{"query": "claude", "lane": "hot-path"}},
		{"search-no-match", "search_posts", map[string]any{"query": "zeppelin"}},
		{"search-empty-query", "search_posts", map[string]any{"query": ""}},
		{"search-limit-too-big", "search_posts", map[string]any{"query": "go", "limit": 50}},
		{"search-wrong-type", "search_posts", map[string]any{"query": "go", "limit": "three"}},
		{"search-unknown-argument", "search_posts", map[string]any{"query": "go", "max": 3}},
		{"recent-all", "recent_posts", map[string]any{"limit": 3}},
		{"recent-in-lane", "recent_posts", map[string]any{"lane": "world-models"}},
		{"lanes", "list_lanes", map[string]any{}},
		{"get-post", "get_post", map[string]any{"url": "/go/testing-without-a-framework/"}},
		{"get-post-no-such", "get_post", map[string]any{"url": "/go/no-such-post/"}},
		{"get-post-not-a-path", "get_post", map[string]any{"url": "https://stepinto.dev/go/testing-without-a-framework/"}},
	}
	session := connect(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: c.tool, Arguments: c.args})
			type outcome struct {
				ProtocolError string `json:"protocolError,omitempty"`
				IsError       bool   `json:"isError,omitempty"`
				Text          string `json:"text,omitempty"`
				Structured    any    `json:"structured,omitempty"`
			}
			var got outcome
			if err != nil {
				got.ProtocolError = err.Error()
			} else {
				got.IsError = res.IsError
				got.Structured = res.StructuredContent
				if res.StructuredContent == nil || res.IsError {
					for _, c := range res.Content {
						if tc, ok := c.(*mcp.TextContent); ok {
							got.Text += tc.Text
						}
					}
				}
			}
			golden(t, "tools/"+c.name+".json", got, "run with -update if the change is intended")
		})
	}
}

// TestConversations runs the real tool loop against recorded model turns.
// The model's side comes from claude-like-replayable; the tool calls run
// against the real server. If the server's answers change, the recorded
// conversation no longer matches what a model would have seen, and the
// saved transcript says so.
func TestConversations(t *testing.T) {
	only := os.Getenv("RECORD_ONLY") // with -record, record just this conversation
	cases := []struct{ name, question string }{
		{"testing-in-go", "Has stepinto.dev written about testing Go code without a mocking library? Give me the post and one thing it recommends."},
		{"fastest-claude-client", "Which posts on stepinto.dev are about calling Claude quickly from a small client? Just list them."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := *record && (only == "" || only == c.name)
			if rec && os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("ANTHROPIC_AUTH_TOKEN") == "" {
				t.Fatal("-record needs ANTHROPIC_API_KEY")
			}
			model := &replayable.Server{Dir: "../testdata/recordings", NoDelay: true, Logf: t.Logf}
			if rec {
				model.Upstream = "https://api.anthropic.com"
			}
			ts := httptest.NewServer(model)
			defer ts.Close()

			answer, err := agent.Ask(context.Background(), connect(t), c.question, 6,
				option.WithBaseURL(ts.URL),
				option.WithAPIKey("placeholder"), // the stand-in swaps in the real key when recording
				option.WithHeader(replayable.ScenarioHeader, c.name),
				option.WithMaxRetries(0),
			)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, "conversations/"+c.name+".json", answer,
				"the model's later turns were recorded against the old answers, so record this conversation again with -record -update")
		})
	}
}
