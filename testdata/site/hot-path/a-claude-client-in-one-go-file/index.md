# A Claude Client in One Go File

> The second client on the hot path: net/http, encoding/json and a bufio scanner for Server-Sent Events, in a static binary from a scratch image. What the standard library gives you, and the one thing it does not.

> Published: 2026-10-29

Last week I wrote a Claude client in [bash](/posts/claude-hot-path-bash/), parsed the stream with regular expressions, and pinned it as the thing to beat. This week it's Go, which has it rather easy by comparison.

Go is the first language in the series where the standard library covers everything the spec asks for: an HTTP client with TLS, a JSON codec, a buffered line reader, and a timezone database baked into the binary. The whole client is 175 lines in one file, and it runs from a `scratch` image.

The code is at [github.com/AndyCross/claude-hot-path](https://github.com/AndyCross/claude-hot-path/tree/main/clients/go) and the leaderboard is at [/claude-hot-path/](/claude-hot-path/). The spec, which every client has to pass, is on that page.

## Reading the stream

An SSE stream is lines. `event:` lines, `data:` lines, blank separators, and the occasional `ping`. The client only acts on `data:` lines, so a `bufio.Scanner` over the response body with a prefix check is the entire transport layer:

```go
sc := bufio.NewScanner(resp.Body)
sc.Buffer(make([]byte, 64*1024), 1<<20)
for sc.Scan() {
    data, ok := strings.CutPrefix(sc.Text(), "data: ")
    if !ok {
        continue // event:, ping bodies, blank separators
    }
    var ev event
    if err := json.Unmarshal([]byte(data), &ev); err != nil {
        return nil, "", err
    }
    switch ev.Type {
    case "content_block_start":
        content = append(content, *ev.ContentBlock)
        partial = append(partial, strings.Builder{})
    case "content_block_delta":
        switch ev.Delta.Type {
        case "text_delta":
            os.Stdout.WriteString(ev.Delta.Text)
            content[ev.Index].Text += ev.Delta.Text
        case "input_json_delta":
            partial[ev.Index].WriteString(ev.Delta.PartialJSON)
        }
    case "content_block_stop":
        if content[ev.Index].Type == "tool_use" {
            if s := partial[ev.Index].String(); s != "" {
                content[ev.Index].Input = json.RawMessage(s)
            }
        }
    case "message_delta":
        stop = ev.Delta.StopReason
    }
}
```

The `sc.Buffer` call matters. The default scanner token limit is 64 KB and a single `data:` line can be longer than that if a tool argument is large, so the limit goes up to a megabyte. Bash had no such limit and no such safety either.

One struct does for every event. The payloads share a shape, `type` and `index` at the top and a `delta` or a `content_block` underneath, so a single `event` type with all the fields and the unused ones left zero is simpler than a type per event:

```go
type event struct {
    Type         string `json:"type"`
    Index        int    `json:"index"`
    ContentBlock *block `json:"content_block"`
    Delta        struct {
        Type        string `json:"type"`
        Text        string `json:"text"`
        PartialJSON string `json:"partial_json"`
        StopReason  string `json:"stop_reason"`
    } `json:"delta"`
}
```

## Echoing the assistant turn

Tool use is a conversation. After the first stream ends with `stop_reason: "tool_use"`, the second request has to carry the user message, the assistant's whole turn including the `tool_use` block with its assembled input, and then a user message holding the `tool_result`. The `block` type that `content_block_start` decodes into is also the type that gets marshalled back out, which is where `omitzero` earns its place:

```go
type block struct {
    Type      string          `json:"type"`
    Text      string          `json:"text,omitzero"`
    ID        string          `json:"id,omitzero"`
    Name      string          `json:"name,omitzero"`
    Input     json.RawMessage `json:"input,omitzero"`
    ToolUseID string          `json:"tool_use_id,omitzero"`
    Content   string          `json:"content,omitzero"`
}
```

A text block marshals as `type` and `text`, a tool use as `type`, `id`, `name` and `input`, and a tool result as `type`, `tool_use_id` and `content`, all from the one struct. `Input` is a `json.RawMessage` because the fragments arrive as text and there is no reason to decode them into a map only to encode them again. The tool itself reads the timezone out of it with a one-field anonymous struct.

The loop in `main` is then the agentic loop in its smallest form: send, print, and if the stop reason was `tool_use`, run every tool block, append two messages, go round again.

## The one thing the standard library does not have

Timezones. `time.LoadLocation("Europe/London")` reads the system's zoneinfo, and a `scratch` image has no system. Go ships its own copy of the database at `$GOROOT/lib/time/zoneinfo.zip` and `LoadLocation` falls back to it, so the Dockerfile copies that one file into the final image, next to the CA bundle:

```dockerfile
FROM golang:1 AS build
WORKDIR /src
COPY client.go ./
RUN go mod init client && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /client client.go

FROM scratch
COPY --from=build /client /app/client
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /usr/local/go/lib/time/zoneinfo.zip
ENTRYPOINT ["/app/client"]
```

The alternative is `import _ "time/tzdata"`, which embeds the database in the binary and adds about 450 KB to it. For a leaderboard that ranks on size, the zip file in the image is the honest choice because the harness measures the artefact, and either way the dependency is paid for somewhere.

## What it measures as

| | Go | bash and curl |
|---|---|---|
| Artefact | 5.8 MB | 3.7 KB |
| Runtime | 0 | 23.7 MB |
| Ship | 5.8 MB | 23.7 MB |
| Cold start, median of 100 | 1.7 ms | 7.0 ms |
| Turnaround, median of 100 | 1.2 ms | 19.1 ms |
| Client total, median of 100 | 4.8 ms | 35.3 ms |
| Peak RSS | 6 MB | 11 MB |
| Lines of code | 156 | 75 |

go1.27.2, linux/arm64, Apple M4 Max under Docker. The binary is built with `-s -w` and `-trimpath`, and nothing else.

The 5.8 MB binary is the number people will argue about. Most of it is `net/http` and its dependency on `crypto/tls`, and the rest is `encoding/json`, which since 1.27 is the v2 implementation underneath. You cannot get a Go HTTPS client much below five megabytes without leaving the standard library, and the series rules say a language with HTTP in its standard library uses it. Next week's .NET client, published with Native AOT, comes in at 5.4 MB for the binary and then pays 2.4 MB for the C library it links, which is the comparison I most wanted to see when I started this.

The cold start is where Go earns its place. Under two milliseconds from spawn to the first request on the wire, with no runtime to initialise and no shared libraries to load. The turnaround, which is the time between the first reply finishing and the second request arriving, is just over a millisecond: parse the tail of the stream, look up a timezone, build the second body, send it down the connection `net/http` kept open. I assumed most of that millisecond was `time.LoadLocation` opening the zoneinfo zip, and measured it: a variant that reads the TZif file straight from `/usr/share/zoneinfo` came out the same. What is left is the runtime. Every read on the socket is a wake-up through the network poller and a goroutine switch, where Rust and Zig make a blocking syscall and carry on, and at this scale that is the whole difference. Under five milliseconds of client time across the whole exchange, with nothing tuned and nothing worth tuning, is the number the others have to beat, and two of them do. The six megabytes of peak memory is the Go runtime's heap arena rather than anything the client allocated, and it varies by a couple of megabytes between runs.

## How to run it

```sh
git clone https://github.com/AndyCross/claude-hot-path
cd claude-hot-path
./bench/bench.sh go
```

Docker and Python 3, no key. The Rust client is next, which is the first language in the series without HTTP in its standard library, and so the first one that has to pick a dependency and pay for it on the leaderboard.
