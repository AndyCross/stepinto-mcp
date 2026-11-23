// Command stepinto-mcp is an MCP server, over stdio, for the posts on
// stepinto.dev.
//
//	stepinto-mcp                       read from https://stepinto.dev
//	stepinto-mcp -base http://...      read from another copy of the site
package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/AndyCross/stepinto-mcp/server"
	"github.com/AndyCross/stepinto-mcp/site"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	base := os.Getenv("STEPINTO_BASE")
	if base == "" {
		base = "https://stepinto.dev"
	}
	flag.StringVar(&base, "base", base, "the site to read from (or STEPINTO_BASE)")
	flag.Parse()

	srv := server.New(&site.Client{Base: base})
	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
