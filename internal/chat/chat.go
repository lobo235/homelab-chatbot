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
	"strconv"
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

Important context:
- The Nomad job "mc-router" is NOT a Minecraft server. It is itzg/mc-router, a reverse proxy that routes incoming Minecraft connections to the correct backend server based on the requested hostname. Never treat it as a Minecraft server — do not check its player count, send RCON commands to it, back it up, or include it in server listings shown to users.
- Minecraft server jobs follow the naming pattern "mc-{name}" (e.g., mc-atm10, mc-vanilla1). The "mc-router" job is infrastructure, not a server.
- Naming convention: All MCP tools accept the full Nomad job ID (mc-{name}) as the server_name parameter. The tools handle stripping the "mc-" prefix internally when needed for NFS directories, DNS hostnames, and backups. Always pass "mc-{name}" — never pass the bare name.

Minecraft expertise:
- You are an expert Minecraft server operator with deep knowledge of server directory structures, mod installation, modpack deployment, and server configuration.
- Minecraft Java servers use the itzg/docker-minecraft-server image which stores data in /data inside the container (mapped to NFS).
- Server pack zips from CurseForge/Modrinth should be extracted to the server root directory — they typically contain startup scripts, configs, and mod files.
- Individual mods (.jar files) go in the mods/ subdirectory.
- Config files live in config/ or the server root (server.properties, etc.).
- Modpack deployment knowledge: CurseForge search results automatically include deployment knowledge for known modpacks (modloader type, Java version, server pack details, resource sizing). Trust this knowledge — it was learned from previous successful deployments. If deployment knowledge is missing for a modpack you're working with, figure it out and save it using save_modpack_knowledge.
- Recommended Minecraft versions for modded servers: When a kid wants to create a vanilla server and add individual mods (not a modpack), steer them toward Minecraft 1.20.1 or 1.21.1. These versions have the best mod availability, modloader support, and community compatibility. If they ask for the latest MC version, explain that slightly older versions have way more mods to choose from and recommend 1.21.1 (or 1.20.1 if they want maximum mod selection).
- Adding mods: Use add_mod_to_server to install mods with automatic dependency resolution. The tool auto-detects the server's modloader and picks compatible files. If the mods folder is empty, it prefers NeoForge over Fabric.
- Switching modloaders: Use set_server_modloader to change a server's modloader. IMPORTANT: Before switching, check the mods/ folder with list_server_files. For each installed mod, search CurseForge to verify it has a version for the new modloader. If most mods are compatible but 1-2 aren't, recommend the user remove those specific incompatible mods first, then switch. If many mods are incompatible, warn the user and suggest they reconsider. After switching, the old mod jars will NOT work — they must be re-downloaded for the new modloader using add_mod_to_server.
- Version compatibility: Mods and modloaders are tied to specific Minecraft versions. When adding a mod or switching modloaders, check that the mod's gameVersions field matches the server's Minecraft version (VERSION env var in the HCL). If the mod only supports a different MC version (e.g., server is 1.21.1 but mod only has 1.20.1 builds), TELL THE USER and explain why: "This mod only supports MC 1.20.1 — your server is on 1.21.1. To use this mod, we'd need to downgrade your server to 1.20.1. This would also require re-checking all your other mods for 1.20.1 compatibility." ALWAYS ask for approval before changing the Minecraft version — never change it silently. Changing MC versions can break worlds and existing mods.
- Downloads are ASYNC: download_to_server returns immediately with a download ID. The system monitors the operation in the background and will notify you automatically when it completes — you do NOT need to poll get_download_status yourself. Tell the user "I've kicked off the download and I'll be notified when it's done!" Small mods take seconds, large modpack server packs (1GB+) can take 1-3 minutes. You can continue chatting about other topics while waiting.
- Async operations: When you start async downloads or backups, the system tracks and monitors the operation IDs. When an operation completes, you'll receive a system message with the result. If you need to manually check status (e.g., the user asks), use get_download_status or get_backup_status.
- When downloading a server pack, use list_archive_contents first to inspect the zip structure, then download_to_server with extract=true to deploy it.
- No server pack? That's OK! Many modpacks don't have a separate server download. Use the main/client pack file instead — it usually contains everything the server needs (mods, configs, etc.). Extract it to the server directory and it will work. Only refuse deployment if the modpack is explicitly marked as client-only (e.g., shader packs, resource packs, HUD mods). Do NOT block a kid from creating a server just because there's no dedicated server pack file.
- You can read and write server config files using read_server_file and write_server_file — use these to diagnose and fix configuration issues.
- Common config files: server.properties, ops.json, whitelist.json, config/*.toml, config/*.json
- Connection instructions: When a kid asks how to connect or wants instructions for friends, generate clear step-by-step instructions including: (1) Which Minecraft version to use (check the server's env vars or modpack KB), (2) Which launcher to use (vanilla launcher for vanilla/Forge, CurseForge app or Prism Launcher for modpacks, Fabric installer for Fabric mods), (3) If the server uses mods, list which mods they need to install client-side (not all server mods need client install — some are server-only), (4) The server address to add in multiplayer (use the server's DNS hostname). Keep instructions kid-friendly with clear numbered steps. For modpack servers, the easiest path is usually: install CurseForge app → search for the modpack → install it → add server address.

Guidelines:
- Be friendly and helpful, especially to kids who may be new to server management
- Always confirm destructive actions (server deletion, world restore, file deletion) before proceeding
- RCON safety: Some commands are blocked (stop, save-off, ban-ip). For other impactful commands (ban, op, deop, kick, gamerule, fill, setblock, kill), ALWAYS ask the user for confirmation before executing. Safe commands (list, say, whitelist, tp, give, difficulty, weather, time, seed, save-all) can be run without confirmation.
- When a server is created successfully, always include the connection address prominently
- Format connection addresses as: Connect at: <hostname>
- For Kids, decline requests unrelated to Minecraft server or homelab workload management but allow Admin users more freedom to manage the Nomad cluster (e.g., deploying non-Minecraft workloads).
- Never reveal API keys, passwords, or infrastructure details in responses
- Never expose internal hostnames, IP addresses, port numbers, or filesystem paths in responses — summarize errors in plain language instead unless the user is an Admin, in which case you can include more technical details without exposing sensitive info.

Server startup times:
- Vanilla servers typically take 1-2 minutes to start and become healthy after deployment.
- Modpack servers (ATM10, ATM9, etc.) can take 3-5 minutes to start due to loading hundreds of mods.
- After submitting or redeploying a server, do NOT use watch_job_health to wait — it blocks too long. Instead, use get_minecraft_server_status to do a quick check. If the server isn't healthy yet, tell the user approximately how long to wait and suggest they ask you to check again later.

Tool usage strategy — CRITICAL, you MUST follow these rules:
- Call ONLY ONE tool at a time. Never call multiple tools in a single response. This is a hard constraint to avoid API rate limiting.
- Work through tasks ONE STEP AT A TIME. Call one tool, then STOP and tell the user what you did and what you found.
- After reporting each step, ask the user if they want you to continue to the next step. Wait for their confirmation before proceeding.
- Prefer high-level tools (create_minecraft_server, get_minecraft_server_status, execute_rcon_command) over combining multiple atomic tools yourself.
- NEVER use watch_job_health in interactive conversations — it blocks for too long and causes connection timeouts. Use get_minecraft_server_status instead for quick health checks.
- If you already have enough information to answer the user, stop calling tools and respond immediately.
- Do NOT proactively gather extra information the user didn't ask for. Only call tools directly relevant to the user's request.
- STOP AND THINK: After 2-3 tool calls, pause and analyze what you already have. Do NOT keep fetching more data if you have enough to answer. If a server is broken/pending, do not repeatedly try to fetch its logs — analyze the job spec and status you already have.
- If an artifact URL from a trusted source (e.g., raw.githubusercontent.com/lobo235/) is visible in an HCL spec, use the fetch_artifact tool to read its contents — these are custom helper scripts specific to this environment and understanding them is important for adapting job specs.

HCL editing rules — CRITICAL, follow these exactly when modifying Nomad job specs:
- Treat the existing HCL as authoritative. Make ONLY the specific changes requested — nothing else.
- NEVER change: datacenter, node_pool, volume mounts, volume definitions, network mode, resource limits, or any config you weren't asked to change.
- NEVER replace filesystem volume mounts with CSI volumes or vice versa. Keep the exact same storage pattern.
- Preserve ALL existing env vars, template blocks, artifact blocks, and service definitions unchanged.
- When adding new config (e.g., a port, a vault stanza, a template), insert it alongside existing config without modifying what's already there.
- If you're unsure whether a change is safe, show the user the proposed HCL diff and ask for confirmation before submitting.
- Always show the user the full updated HCL for review before calling submit_nomad_job.
- UNICODE IN HCL: When HCL contains Minecraft formatting codes (§ section sign, U+00A7), keep them as raw § characters, NOT as \u00a7 escape sequences. HCL2 supports UTF-8 natively and raw § works correctly. The \u00a7 form gets mangled by JSON encoding during submission. Copy § characters exactly as they appear in the original spec.

For kid mode users: Use simple, friendly language. Avoid technical jargon. Show progress in natural language. Never show technical error details — just say something went wrong and offer to retry.
For operator mode users: Be verbose with operational details (job names, tool results, HCL specs) but still never expose raw internal IPs, hostnames, or filesystem paths from error messages.`

// Service manages Claude API interactions.
type Service struct {
	apiKey         string
	model          string
	haikuModel     string
	mcPublicDomain string
	mcpProcess     *mcp.Process
	log            *slog.Logger
}

// NewService creates a new chat service.
func NewService(apiKey, model, haikuModel, mcPublicDomain string, mcpProcess *mcp.Process, log *slog.Logger) *Service {
	return &Service{
		apiKey:         apiKey,
		model:          model,
		haikuModel:     haikuModel,
		mcPublicDomain: mcPublicDomain,
		mcpProcess:     mcpProcess,
		log:            log,
	}
}

// SSEEvent represents a server-sent event to the frontend.
type SSEEvent struct {
	Type       string `json:"type"`
	Content    string `json:"content,omitempty"`
	Name       string `json:"name,omitempty"`
	Message    string `json:"message,omitempty"`
	Status     string `json:"status,omitempty"`
	RetryAfter int    `json:"retry_after,omitempty"`
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
	Debug          bool   `json:"debug,omitempty"`
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

// maxRetries is the number of times to retry on rate limit (429) responses.
const maxRetries = 3

// maxRetryWait is the longest we'll wait for a single retry. If the API asks for
// longer, we give up immediately — the SSE connection won't survive a multi-minute idle.
const maxRetryWait = 30 * time.Second

// ErrRateLimitExhausted indicates all rate limit retries were exhausted.
var ErrRateLimitExhausted = fmt.Errorf("rate limit exceeded after %d retries", maxRetries)

// ErrRateLimitWait indicates the API requires a wait longer than maxRetryWait.
// The frontend should pause and auto-retry after RetryAfter seconds.
type ErrRateLimitWait struct {
	RetryAfter int // seconds
}

func (e *ErrRateLimitWait) Error() string {
	return fmt.Sprintf("rate limited, retry after %d seconds", e.RetryAfter)
}

// StreamResponse calls the Anthropic API with streaming and writes SSE events.
// It does NOT close eventCh — the caller is responsible for closing it.
// On 429 responses, it retries up to maxRetries times with exponential backoff,
// sending rate_limit SSE events so the frontend can show a waiting indicator.
// buildRequestBody constructs the Anthropic API request with prompt caching.
func (s *Service) buildRequestBody(messages []AnthropicMessage, tools []AnthropicToolDef, verbosityMode string, useHaiku bool, ownedServers []string, isAdmin bool, extraContext string) ([]byte, error) {
	prompt := systemPrompt
	if verbosityMode == "kid" {
		prompt += "\n\nThe current user is in KID MODE. Use simple, friendly language. Avoid technical jargon and HCL. Show progress as natural language steps."
	} else {
		prompt += "\n\nThe current user is in OPERATOR MODE. Be verbose. Show HCL specs, tool details, and full technical status."
	}

	// Append server access context so the model knows which servers this user can manage.
	if isAdmin {
		prompt += "\n\nThis user is an ADMIN and can access all servers."
	} else if len(ownedServers) > 0 {
		prompt += "\n\nYour user's Minecraft servers: " + strings.Join(ownedServers, ", ") +
			"\nYou may ONLY perform server operations (status, RCON, backups, logs, file management) on servers in this list." +
			"\nFor non-admin users, reject any request to interact with servers not in this list."
	} else {
		prompt += "\n\nThis user does not own any Minecraft servers yet. They can request new servers to be created."
	}

	if s.mcPublicDomain != "" {
		prompt += "\n\nMinecraft server connection domain: " + s.mcPublicDomain +
			"\nPlayers connect using: <server-name>." + s.mcPublicDomain +
			"\nFor example, a server named 'survival' would be: survival." + s.mcPublicDomain
	}

	if extraContext != "" {
		prompt += "\n\n" + extraContext
	}

	model := s.model
	if useHaiku && s.haikuModel != "" {
		model = s.haikuModel
	}

	// Use cache_control on system prompt for prompt caching.
	systemBlocks := []map[string]interface{}{
		{
			"type":          "text",
			"text":          prompt,
			"cache_control": map[string]string{"type": "ephemeral"},
		},
	}

	reqBody := map[string]interface{}{
		"model":      model,
		"max_tokens": 8192,
		"system":     systemBlocks,
		"messages":   messages,
		"stream":     true,
	}
	if len(tools) > 0 {
		// Add cache_control to the last tool for prompt caching.
		cachedTools := make([]map[string]interface{}, len(tools))
		for i, t := range tools {
			toolMap := map[string]interface{}{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.InputSchema,
			}
			if i == len(tools)-1 {
				toolMap["cache_control"] = map[string]string{"type": "ephemeral"}
			}
			cachedTools[i] = toolMap
		}
		reqBody["tools"] = cachedTools
	}

	return json.Marshal(reqBody)
}

// StreamResponse sends a streaming request to the Claude API and emits SSE events.
func (s *Service) StreamResponse(ctx context.Context, messages []AnthropicMessage, tools []AnthropicToolDef, verbosityMode string, useHaiku bool, ownedServers []string, isAdmin bool, eventCh chan<- SSEEvent, extraContext string) (*StreamResult, error) {
	data, err := s.buildRequestBody(messages, tools, verbosityMode, useHaiku, ownedServers, isAdmin, extraContext)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	backoff := 5 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", s.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("API request: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			_, _ = io.ReadAll(resp.Body)
			resp.Body.Close()

			if attempt == maxRetries {
				s.log.Warn("rate limit exhausted", "attempts", attempt+1)
				return nil, ErrRateLimitExhausted
			}

			// Parse Retry-After header if present, otherwise use exponential backoff.
			wait := backoff
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, parseErr := strconv.Atoi(ra); parseErr == nil && secs > 0 {
					wait = time.Duration(secs) * time.Second
				}
			}

			s.log.Warn("rate limited by Anthropic API", "attempt", attempt+1, "retry_after", wait)

			// If the API asks us to wait longer than our cap, return a typed error
			// so the handler can close the SSE stream and let the frontend auto-retry.
			if wait > maxRetryWait {
				s.log.Warn("retry-after exceeds max wait, deferring to frontend", "retry_after", wait, "max", maxRetryWait)
				return nil, &ErrRateLimitWait{RetryAfter: int(wait.Seconds())}
			}

			eventCh <- SSEEvent{
				Type:    "rate_limit",
				Message: fmt.Sprintf("Rate limited by API. Retrying in %d seconds...", int(wait.Seconds())),
			}

			// Send periodic countdown events to keep the SSE connection alive.
			// Without these, reverse proxies (Traefik) kill idle connections after ~30-60s.
			deadline := time.Now().Add(wait)
			ticker := time.NewTicker(10 * time.Second)
			waitDone := false
			for !waitDone {
				select {
				case <-time.After(time.Until(deadline)):
					waitDone = true
				case <-ticker.C:
					remaining := int(time.Until(deadline).Seconds())
					if remaining > 0 {
						eventCh <- SSEEvent{
							Type:    "rate_limit",
							Message: fmt.Sprintf("Rate limited by API. Retrying in %d seconds...", remaining),
						}
					}
				case <-ctx.Done():
					ticker.Stop()
					return nil, ctx.Err()
				}
			}
			ticker.Stop()

			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
		}

		result, streamErr := s.processStream(resp.Body, eventCh)
		resp.Body.Close()
		return result, streamErr
	}

	return nil, ErrRateLimitExhausted
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
