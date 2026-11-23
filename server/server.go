// Package server is the stepinto.dev MCP server: three tools over the site's
// search index and the Markdown copies of its posts.
package server

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/AndyCross/stepinto-mcp/site"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is reported to clients in the initialize handshake.
const Version = "0.1.0"

type SearchIn struct {
	Query string `json:"query" jsonschema:"Words to look for in titles, tags, summaries and post text"`
	Lane  string `json:"lane,omitempty" jsonschema:"Only search this lane, e.g. go or world-models. list_lanes gives the names"`
	Limit int    `json:"limit,omitempty" jsonschema:"How many posts to return, 1 to 20. Default 5"`
}

type RecentIn struct {
	Lane  string `json:"lane,omitempty" jsonschema:"Only posts in this lane. list_lanes gives the names"`
	Limit int    `json:"limit,omitempty" jsonschema:"How many posts to return, 1 to 20. Default 5"`
}

type GetIn struct {
	URL string `json:"url" jsonschema:"The post's address as search_posts or recent_posts gave it, like /go/some-post/"`
}

type PostsOut struct {
	Posts []site.Post `json:"posts"`
}

type Lane struct {
	Name  string `json:"name"`
	Posts int    `json:"posts"`
}

type LanesOut struct {
	Lanes []Lane `json:"lanes"`
}

type GetOut struct {
	URL      string `json:"url"`
	Markdown string `json:"markdown"`
}

func limit(n int) (int, error) {
	switch {
	case n == 0:
		return 5, nil
	case n < 1 || n > 20:
		return 0, fmt.Errorf("limit must be between 1 and 20, not %d", n)
	}
	return n, nil
}

// New returns the server, reading from s.
func New(s *site.Client) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "stepinto", Version: Version}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "search_posts",
		Description: "Search the posts on stepinto.dev, a blog about Go for C# developers, world models, " +
			"AI law in the UK and EU, European AI sovereignty and calling Claude fast. " +
			"Returns the best matches first, each with its title, url, date, lane, summary and tags. " +
			"Use get_post with a url to read the whole post.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchIn) (*mcp.CallToolResult, PostsOut, error) {
		n, err := limit(in.Limit)
		if err != nil {
			return nil, PostsOut{}, err
		}
		if in.Query == "" {
			return nil, PostsOut{}, errors.New("query is empty")
		}
		posts, err := s.Search(ctx, in.Query, in.Lane, n)
		if posts == nil {
			posts = []site.Post{}
		}
		return nil, PostsOut{posts}, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "recent_posts",
		Description: "The newest posts on stepinto.dev, newest first, optionally from one lane.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in RecentIn) (*mcp.CallToolResult, PostsOut, error) {
		n, err := limit(in.Limit)
		if err != nil {
			return nil, PostsOut{}, err
		}
		all, err := s.Posts(ctx)
		if err != nil {
			return nil, PostsOut{}, err
		}
		out := []site.Post{}
		for _, p := range all {
			if len(out) == n {
				break
			}
			if in.Lane == "" || p.Lane == in.Lane {
				out = append(out, p)
			}
		}
		return nil, PostsOut{out}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_lanes",
		Description: "The lanes (series) posts on stepinto.dev belong to, with how many posts each has.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, LanesOut, error) {
		counts, err := s.Lanes(ctx)
		if err != nil {
			return nil, LanesOut{}, err
		}
		out := LanesOut{Lanes: []Lane{}}
		for name, n := range counts {
			out.Lanes = append(out.Lanes, Lane{name, n})
		}
		sort.Slice(out.Lanes, func(i, j int) bool { return out.Lanes[i].Name < out.Lanes[j].Name })
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_post",
		Description: "The whole of one post on stepinto.dev as Markdown, by the url search_posts or recent_posts gave.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetIn) (*mcp.CallToolResult, GetOut, error) {
		md, err := s.Markdown(ctx, in.URL)
		if err != nil {
			return nil, GetOut{}, err
		}
		return nil, GetOut{URL: in.URL, Markdown: md}, nil
	})

	return srv
}
