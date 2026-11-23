# stepinto-mcp

An MCP server for the posts on [stepinto.dev](https://stepinto.dev), and a test
harness that checks it without a model in the loop.

## The server

Four tools over the site's public search index and the Markdown copy every
post has:

| Tool | |
|---|---|
| `search_posts` | `query`, optional `lane` and `limit` (1 to 20). Best matches first |
| `recent_posts` | Newest first, optional `lane` and `limit` |
| `list_lanes` | Each lane (series) and how many posts it has |
| `get_post` | One post as Markdown, by the `url` the other tools give |

```sh
go install github.com/AndyCross/stepinto-mcp/cmd/stepinto-mcp@latest
```

Then add it to your MCP client as a stdio server running `stepinto-mcp`.
`-base` (or `STEPINTO_BASE`) points it at another copy of the site.

## The harness

`go test ./...` runs three kinds of test against the real binary, started as a
process and spoken to over stdio, with the site served from `testdata/site`.
It needs no key and no network.

- **Contract** (`TestContract`). Every tool's name, description, input schema
  and output schema, compared with `testdata/contract/tools.json`. These are
  what the model reads, so a change to any of them changes what it does.
- **Tools** (`TestTools`). Direct calls with saved results in
  `testdata/tools/`, the errors included, since an error's text is also
  something the model reads and acts on.
- **Conversations** (`TestConversations`). The tool loop in `agent/` runs
  against recorded model turns, served by
  [claude-like-replayable](https://github.com/AndyCross/claude-like-replayable),
  while the tool calls run against the real server. The transcript, every
  call with its result and the final answer, is compared with
  `testdata/conversations/`.

```sh
go test ./harness                              check
go test ./harness -update                      accept the current contract and results
RECORD_ONLY=<name> go test ./harness -run TestConversations -record -update
                                               record one conversation again (needs ANTHROPIC_API_KEY)
```

A failing conversation means the server now answers differently from when
the conversation was recorded. The model's later turns were made against the
old answer, so record it again rather than accepting the new transcript.

## Licence

MIT
