// Package agent is the smallest useful Claude tool loop over an MCP session:
// list the server's tools, offer them to the model, run each tool call it
// asks for, send the results back, and stop when it answers.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Model is the model the loop asks for.
const Model = anthropic.ModelClaudeSonnet5_5

const system = `You answer questions about the blog stepinto.dev using its tools.
Find posts with search_posts or recent_posts, read one with get_post when the
summary is not enough, and link every post you mention by its url.`

// Call is one tool call the model made and what the server answered.
type Call struct {
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	Result  string          `json:"result"`
	IsError bool            `json:"is_error"`
}

// Answer is the model's last text and the tool calls on the way to it.
type Answer struct {
	Text  string `json:"text"`
	Calls []Call `json:"calls"`
}

// Ask puts the question to the model with the session's tools until it stops
// asking for them, at most maxTurns times. opts go to the Anthropic client:
// option.WithBaseURL points it at a stand-in.
func Ask(ctx context.Context, session *mcp.ClientSession, question string, maxTurns int, opts ...option.RequestOption) (*Answer, error) {
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	var tools []anthropic.ToolUnionParam
	for _, t := range listed.Tools {
		schema, _ := t.InputSchema.(map[string]any)
		// A tool with no arguments has no properties, and a zero schema is
		// left out of the request altogether, which the API rejects.
		props := schema["properties"]
		if props == nil {
			props = map[string]any{}
		}
		in := anthropic.ToolInputSchemaParam{Properties: props}
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				in.Required = append(in.Required, fmt.Sprint(r))
			}
		}
		tools = append(tools, anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
			Name: t.Name, Description: anthropic.String(t.Description), InputSchema: in,
		}})
	}

	client := anthropic.NewClient(opts...)
	messages := []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(question))}
	answer := &Answer{}
	for turn := 0; turn < maxTurns; turn++ {
		msg, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     Model,
			MaxTokens: 2048,
			System:    []anthropic.TextBlockParam{{Text: system}},
			Messages:  messages,
			Tools:     tools,
		})
		if err != nil {
			return answer, err
		}
		messages = append(messages, msg.ToParam())

		var results []anthropic.ContentBlockParamUnion
		var text []string
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				text = append(text, block.Text)
			case "tool_use":
				call := Call{Name: block.Name, Input: block.Input}
				res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: block.Name, Arguments: block.Input})
				if err != nil {
					call.Result, call.IsError = err.Error(), true
				} else {
					call.Result, call.IsError = contentText(res), res.IsError
				}
				answer.Calls = append(answer.Calls, call)
				results = append(results, anthropic.NewToolResultBlock(block.ID, call.Result, call.IsError))
			}
		}
		answer.Text = strings.Join(text, "\n")
		if len(results) == 0 {
			return answer, nil
		}
		messages = append(messages, anthropic.NewUserMessage(results...))
	}
	return answer, fmt.Errorf("no answer after %d turns", maxTurns)
}

func contentText(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}
