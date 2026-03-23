// Package chat handles Claude API interactions with MCP tool support and SSE streaming.
package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lobo235/homelab-chatbot/internal/mcp"
)

// systemPrompt defines Claude's behavior for the chatbot.
const systemPrompt = `You are a helpful Minecraft server management assistant for a homelab environment. You help kids and operators create, manage, and monitor Minecraft servers running on a Nomad cluster.

You have access to MCP tools that let you:
- Create and destroy Minecraft servers (provision infrastructure, DNS, etc.)
- Check server status and health
- Send RCON commands (op/deop players, whitelist management, etc.)
- Back up and restore server worlds
- View server logs
- Deploy generic workloads to the Nomad cluster

Guidelines:
- Be friendly and helpful, especially to kids who may be new to server management
- Always confirm destructive actions (server deletion, world restore) before proceeding
- When a server is created successfully, always include the connection address prominently
- Format connection addresses as: Connect at: <hostname>
- Decline requests unrelated to Minecraft server or homelab workload management
- Never reveal API keys, passwords, or infrastructure details in responses

For kid mode users: Use simple, friendly language. Avoid technical jargon. Show progress in natural language.
For operator mode users: Be verbose. Show HCL specs, full tool details, and technical status.`

// Service manages Claude API interactions.
type Service struct {
	apiKey     string
	model      string
	mcpProcess *mcp.Process
	log        *slog.Logger
}

// NewService creates a new chat service.
func NewService(apiKey, model string, mcpProcess *mcp.Process, log *slog.Logger) *Service {
	return &Service{
		apiKey:     apiKey,
		model:      model,
		mcpProcess: mcpProcess,
		log:        log,
	}
}

// SSEEvent represents a server-sent event to the frontend.
type SSEEvent struct {
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message,omitempty"`
	Status  string `json:"status,omitempty"`
}

// ToolUseBlock represents a tool call from Claude's response.
type ToolUseBlock struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// StreamResult contains the full result of a single streaming API call.
type StreamResult struct {
	Text        string
	ToolUses    []ToolUseBlock
	StopReason  string
	InputTokens int64
}

// Request is the request body for POST /api/chat.
type Request struct {
	Message        string `json:"message"`
	ConversationID int64  `json:"conversation_id,omitempty"`
	VerbosityMode  string `json:"verbosity_mode,omitempty"`
}

// MCPClient wraps the stdio communication with the MCP server subprocess.
// It uses JSON-RPC 2.0 over stdin/stdout.
type MCPClient struct {
	stdin   io.Writer
	stdout  *bufio.Reader
	mu      sync.Mutex
	nextID  int64
	log     *slog.Logger
	tools   []MCPTool
	toolsMu sync.RWMutex
}

// MCPTool represents an MCP tool definition.
type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// JSONRPC request/response types.
type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewMCPClient creates a new MCP client from a running process.
func NewMCPClient(proc *mcp.Process, log *slog.Logger) *MCPClient {
	return &MCPClient{
		stdin:  proc.Stdin(),
		stdout: bufio.NewReader(proc.Stdout()),
		log:    log,
	}
}

// Initialize performs the MCP initialization handshake.
func (c *MCPClient) Initialize(ctx context.Context) error {
	resp, err := c.call(ctx, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "homelab-chatbot",
			"version": "1.0.0",
		},
	})
	if err != nil {
		return fmt.Errorf("MCP initialize: %w", err)
	}

	c.log.Info("MCP server initialized", "result", string(resp))

	// Send initialized notification (no response expected).
	return c.notify("notifications/initialized", nil)
}

// ListTools fetches the available tools from the MCP server.
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPTool, error) {
	resp, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	var result struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse tools: %w", err)
	}

	c.toolsMu.Lock()
	c.tools = result.Tools
	c.toolsMu.Unlock()

	c.log.Info("MCP tools loaded", "count", len(result.Tools))
	return result.Tools, nil
}

// GetTools returns the cached tool list.
func (c *MCPClient) GetTools() []MCPTool {
	c.toolsMu.RLock()
	defer c.toolsMu.RUnlock()
	return c.tools
}

// CallTool invokes an MCP tool and returns the result.
func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	resp, err := c.call(ctx, "tools/call", map[string]interface{}{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", fmt.Errorf("call tool %s: %w", name, err)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("parse tool result: %w", err)
	}

	var sb strings.Builder
	for _, c := range result.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}

	if result.IsError {
		return sb.String(), fmt.Errorf("tool error: %s", sb.String())
	}

	return sb.String(), nil
}

func (c *MCPClient) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextID++
	id := c.nextID

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

	if _, err := c.stdin.Write(data); err != nil {
		return nil, fmt.Errorf("write to MCP: %w", err)
	}

	// Read response lines until we get a matching ID.
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("read from MCP: %w", err)
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// Check if this is a notification (no id field) — skip it.
		var peek struct {
			ID *int64 `json:"id"`
		}
		if err := json.Unmarshal(line, &peek); err != nil {
			continue
		}
		if peek.ID == nil {
			// This is a notification, skip.
			continue
		}

		var resp jsonrpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}

		if resp.ID == id {
			if resp.Error != nil {
				return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			return resp.Result, nil
		}
	}
}

func (c *MCPClient) notify(method string, params interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	req := struct {
		JSONRPC string      `json:"jsonrpc"`
		Method  string      `json:"method"`
		Params  interface{} `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	_, err = c.stdin.Write(data)
	return err
}

// AnthropicMessage represents a message in the Anthropic API format.
type AnthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

// AnthropicToolDef represents a tool definition for the Anthropic API.
type AnthropicToolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

// StreamResponse calls the Anthropic API with streaming and writes SSE events.
// It does NOT close eventCh — the caller is responsible for closing it.
func (s *Service) StreamResponse(ctx context.Context, messages []AnthropicMessage, tools []AnthropicToolDef, verbosityMode string, eventCh chan<- SSEEvent) (*StreamResult, error) {
	prompt := systemPrompt
	if verbosityMode == "kid" {
		prompt += "\n\nThe current user is in KID MODE. Use simple, friendly language. Avoid technical jargon and HCL. Show progress as natural language steps."
	} else {
		prompt += "\n\nThe current user is in OPERATOR MODE. Be verbose. Show HCL specs, tool details, and full technical status."
	}

	reqBody := map[string]interface{}{
		"model":      s.model,
		"max_tokens": 8192,
		"system":     prompt,
		"messages":   messages,
		"stream":     true,
	}
	if len(tools) > 0 {
		reqBody["tools"] = tools
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	return s.processStream(resp.Body, eventCh)
}

// processStream reads the Anthropic SSE stream and extracts text/tool events.
func (s *Service) processStream(body io.Reader, eventCh chan<- SSEEvent) (*StreamResult, error) {
	scanner := bufio.NewScanner(body)
	// Increase scanner buffer for large streaming responses.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	result := &StreamResult{}
	var fullText strings.Builder
	var currentToolID string
	var currentToolName string
	var currentToolInput strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			ContentBlock struct {
				Type  string          `json:"type"`
				Name  string          `json:"name"`
				Text  string          `json:"text"`
				ID    string          `json:"id"`
				Input json.RawMessage `json:"input"`
			} `json:"content_block"`
			Message struct {
				Usage struct {
					InputTokens int64 `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				InputTokens int64 `json:"input_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "message_start":
			result.InputTokens = event.Message.Usage.InputTokens

		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				currentToolID = event.ContentBlock.ID
				currentToolName = event.ContentBlock.Name
				currentToolInput.Reset()
				eventCh <- SSEEvent{
					Type:    "tool_start",
					Name:    currentToolName,
					Message: fmt.Sprintf("Using tool: %s", currentToolName),
				}
			}

		case "content_block_delta":
			switch event.Delta.Type {
			case "text_delta":
				fullText.WriteString(event.Delta.Text)
				eventCh <- SSEEvent{
					Type:    "token",
					Content: event.Delta.Text,
				}
			case "input_json_delta":
				currentToolInput.WriteString(event.Delta.PartialJSON)
			}

		case "content_block_stop":
			if currentToolName != "" {
				inputJSON := currentToolInput.String()
				if inputJSON == "" {
					inputJSON = "{}"
				}
				result.ToolUses = append(result.ToolUses, ToolUseBlock{
					ID:    currentToolID,
					Name:  currentToolName,
					Input: json.RawMessage(inputJSON),
				})
				currentToolID = ""
				currentToolName = ""
			}

		case "message_delta":
			if event.Delta.StopReason != "" {
				result.StopReason = event.Delta.StopReason
			}
			if event.Usage.InputTokens > 0 {
				result.InputTokens = event.Usage.InputTokens
			}
		}
	}

	result.Text = fullText.String()
	return result, scanner.Err()
}

// MCPToolsToAnthropic converts MCP tool definitions to Anthropic API format.
func MCPToolsToAnthropic(mcpTools []MCPTool) []AnthropicToolDef {
	tools := make([]AnthropicToolDef, len(mcpTools))
	for i, t := range mcpTools {
		tools[i] = AnthropicToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return tools
}

// GetSystemPrompt returns the system prompt for reference.
func GetSystemPrompt() string {
	return systemPrompt
}
