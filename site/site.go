// Package site reads stepinto.dev's public data: the search index at
// /index.json and the Markdown copy every post has at <post>/index.md.
package site

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// Post is one entry in the search index, with its lane taken from the
// address (/go/<slug>/ is in the Go lane).
type Post struct {
	Title   string   `json:"title"`
	URL     string   `json:"url"`
	Date    string   `json:"date"`
	Lane    string   `json:"lane"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
	content string
}

// Client fetches from one copy of the site. The index is read once and kept.
type Client struct {
	Base string // e.g. https://stepinto.dev
	HTTP *http.Client

	mu    sync.Mutex
	posts []Post // nil until the index has been read; a failed read is tried again next time
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Base, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", path, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

// Posts returns every post in the index, newest first.
func (c *Client) Posts(ctx context.Context) ([]Post, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.posts != nil {
		return c.posts, nil
	}
	b, err := c.get(ctx, "/index.json")
	if err != nil {
		return nil, err
	}
	{
		var raw []struct {
			Title     string   `json:"title"`
			Permalink string   `json:"permalink"`
			Date      string   `json:"date"`
			Summary   string   `json:"summary"`
			Tags      []string `json:"tags"`
			Content   string   `json:"content"`
		}
		if err := json.Unmarshal(b, &raw); err != nil {
			return nil, fmt.Errorf("index.json: %w", err)
		}
		posts := []Post{}
		for _, r := range raw {
			u, err := url.Parse(r.Permalink)
			if err != nil {
				continue
			}
			lane, _, _ := strings.Cut(strings.Trim(u.Path, "/"), "/")
			posts = append(posts, Post{
				Title: r.Title, URL: u.Path, Date: r.Date, Lane: lane,
				Summary: r.Summary, Tags: r.Tags, content: r.Content,
			})
		}
		sort.SliceStable(posts, func(i, j int) bool { return posts[i].Date > posts[j].Date })
		c.posts = posts
	}
	return c.posts, nil
}

// Lanes returns each lane's name and how many posts it has, in order of name.
func (c *Client) Lanes(ctx context.Context) (map[string]int, error) {
	posts, err := c.Posts(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, p := range posts {
		out[p.Lane]++
	}
	return out, nil
}

// Search scores every post against the words of the query: a word in the
// title counts 5, in a tag 3, in the summary 2, and each time it appears in
// the text 1 (up to 5). Posts that match no word are left out. Ties go to the
// newer post.
func (c *Client) Search(ctx context.Context, query, lane string, limit int) ([]Post, error) {
	posts, err := c.Posts(ctx)
	if err != nil {
		return nil, err
	}
	words := strings.Fields(strings.ToLower(query))
	type hit struct {
		p     Post
		score int
	}
	var hits []hit
	for _, p := range posts {
		if lane != "" && p.Lane != lane {
			continue
		}
		title, summary, text := strings.ToLower(p.Title), strings.ToLower(p.Summary), strings.ToLower(p.content)
		tags := strings.ToLower(strings.Join(p.Tags, " "))
		score := 0
		for _, w := range words {
			if strings.Contains(title, w) {
				score += 5
			}
			if strings.Contains(tags, w) {
				score += 3
			}
			if strings.Contains(summary, w) {
				score += 2
			}
			score += min(strings.Count(text, w), 5)
		}
		if score > 0 {
			hits = append(hits, hit{p, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	var out []Post
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].p)
	}
	return out, nil
}

// Markdown fetches the post at path (/go/<slug>/) as Markdown.
func (c *Client) Markdown(ctx context.Context, path string) (string, error) {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return "", fmt.Errorf("%q is not a post address; use the url from search_posts or recent_posts, like /go/some-post/", path)
	}
	posts, err := c.Posts(ctx)
	if err != nil {
		return "", err
	}
	want := "/" + strings.Trim(path, "/") + "/"
	for _, p := range posts {
		if p.URL == want {
			b, err := c.get(ctx, want+"index.md")
			return string(b), err
		}
	}
	return "", fmt.Errorf("no post at %s", want)
}
