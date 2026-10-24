// Package agent drives the box on a person's behalf: a scripted solver, a manual tool caller and a
// model-driven loop. All three act as MCP clients of this server's own /mcp, so every step shows up
// in the traffic pane exactly like an outside client's would.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"puzzleboxapp/internal/mcp"
)

const meta = "io.modelcontextprotocol/"

// Client is a minimal 2026-07-28 MCP client: every request carries its whole context.
type Client struct {
	URL    string
	Bearer string
	Name   string // clientInfo.name, so the viewer can tell clients apart
	HTTP   *http.Client
	next   atomic.Int64
}

// RPCError is a JSON-RPC error answer.
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

// Call sends one request and returns its result object.
func (c *Client) Call(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	p := map[string]any{}
	for k, v := range params {
		p[k] = v
	}
	p["_meta"] = map[string]any{
		meta + "protocolVersion":    mcp.Spec,
		meta + "clientInfo":         map[string]any{"name": c.Name, "version": "1.0"},
		meta + "clientCapabilities": map[string]any{"elicitation": map[string]any{}},
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.next.Add(1), "method": method, "params": p})
	req, err := http.NewRequestWithContext(ctx, "POST", c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcp.Spec)
	req.Header.Set("Mcp-Method", method)
	if name, ok := p["name"].(string); ok {
		req.Header.Set("Mcp-Name", name)
	}
	if args, ok := p["arguments"].(map[string]any); ok {
		if id, ok := args["box_id"].(string); ok {
			req.Header.Set("Mcp-Param-BoxId", id)
		}
	}
	if c.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.Bearer)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var env struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&env); err != nil {
		return nil, fmt.Errorf("MCP server answered HTTP %d with no JSON", resp.StatusCode)
	}
	if env.Error != nil {
		return nil, &RPCError{env.Error.Code, env.Error.Message}
	}
	return env.Result, nil
}

// Tool calls one tool. extra carries fields such as inputResponses and requestState for a retry.
func (c *Client) Tool(ctx context.Context, name string, args, extra map[string]any) (map[string]any, error) {
	params := map[string]any{"name": name, "arguments": args}
	for k, v := range extra {
		params[k] = v
	}
	return c.Call(ctx, "tools/call", params)
}

// Text is the first text block of a tool result.
func Text(res map[string]any) string {
	blocks, _ := res["content"].([]any)
	if len(blocks) == 0 {
		return ""
	}
	b, _ := blocks[0].(map[string]any)
	s, _ := b["text"].(string)
	return s
}

// IsError reports whether the tool said no.
func IsError(res map[string]any) bool { b, _ := res["isError"].(bool); return b }

func structured(res map[string]any) map[string]any {
	m, _ := res["structuredContent"].(map[string]any)
	return m
}
