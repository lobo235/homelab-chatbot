// Package database provides SQLite storage for users, sessions, conversations,
// and server ownership.
package database

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver.
)

// DB wraps a SQLite database connection with typed query methods.
type DB struct {
	db  *sql.DB
	log *slog.Logger
}

// User represents a chatbot user account.
type User struct {
	ID            int64     `json:"id"`
	Username      string    `json:"username"`
	PasswordHash  string    `json:"-"`
	Role          string    `json:"role"`
	VerbosityMode string    `json:"verbosity_mode"`
	Active        bool      `json:"active"`
	MaxServers    int       `json:"max_servers"`
	MaxTokens     int       `json:"max_tokens"`
	CreatedAt     time.Time `json:"created_at"`
}

// Session represents an authenticated user session.
type Session struct {
	ID        int64     `json:"id"`
	TokenHash string    `json:"-"`
	UserID    int64     `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// Conversation represents a chat session with messages.
type Conversation struct {
	ID             int64     `json:"id"`
	UserID         int64     `json:"user_id"`
	Title          string    `json:"title"`
	InputTokens    int64     `json:"input_tokens"`
	ContextSummary string    `json:"context_summary,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Message represents a single chat message within a conversation.
type Message struct {
	ID             int64     `json:"id"`
	ConversationID int64     `json:"conversation_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

// AsyncOp represents an in-flight async operation (download, backup, etc.).
type AsyncOp struct {
	ID             int64     `json:"id"`
	ConversationID int64     `json:"conversation_id"`
	UserID         int64     `json:"user_id"`
	ToolName       string    `json:"tool_name"`
	OperationID    string    `json:"operation_id"`
	ServerName     string    `json:"server_name"`
	Description    string    `json:"description"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// ServerOwnership records which user owns a provisioned server.
type ServerOwnership struct {
	ID         int64     `json:"id"`
	ServerName string    `json:"server_name"`
	OwnerID    int64     `json:"owner_user_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// Open creates or opens a SQLite database at the given directory and runs migrations.
func Open(dataDir string, log *slog.Logger) (*DB, error) {
	if dataDir != ":memory:" {
		if err := os.MkdirAll(dataDir, 0750); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}

	dsn := ":memory:"
	if dataDir != ":memory:" {
		dbPath := filepath.Join(dataDir, "chatbot.db")
		dsn = dbPath + "?_journal_mode=WAL&_busy_timeout=5000"

		// Ensure DB file has restrictive permissions (0600) — contains user
		// credentials, session hashes, and conversation history.
		if err := ensureFilePermissions(dbPath, 0600); err != nil {
			return nil, fmt.Errorf("set db permissions: %w", err)
		}
	}

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Set connection limits for SQLite.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	// Enable foreign key enforcement (required for ON DELETE CASCADE).
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	db := &DB{db: sqlDB, log: log}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			verbosity_mode TEXT NOT NULL DEFAULT '',
			active INTEGER NOT NULL DEFAULT 1,
			max_servers INTEGER NOT NULL DEFAULT 5,
			max_tokens INTEGER NOT NULL DEFAULT 500000,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token_hash TEXT UNIQUE NOT NULL,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS conversations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			title TEXT NOT NULL DEFAULT '',
			input_tokens INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS server_ownership (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_name TEXT UNIQUE NOT NULL,
			owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS async_operations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			tool_name TEXT NOT NULL,
			operation_id TEXT NOT NULL,
			server_name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_async_ops_conversation ON async_operations(conversation_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_async_ops_status ON async_operations(status)`,
		`CREATE INDEX IF NOT EXISTS idx_async_ops_user_status ON async_operations(user_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_token_hash ON sessions(token_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_user_id ON conversations(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation_id ON messages(conversation_id)`,
		`CREATE INDEX IF NOT EXISTS idx_server_ownership_owner ON server_ownership(owner_user_id)`,
		// Bump existing users from old 200k default to new 500k default.
		`UPDATE users SET max_tokens = 500000 WHERE max_tokens = 200000`,
		// Seed existing Minecraft servers as owned by the admin bootstrap account (user ID 1).
		// Uses a subquery so the insert is a no-op when user ID 1 doesn't exist (e.g. in tests).
		// Add context_summary column for rolling conversation summarization.
		`ALTER TABLE conversations ADD COLUMN context_summary TEXT NOT NULL DEFAULT ''`,
		`INSERT OR IGNORE INTO server_ownership (server_name, owner_user_id) SELECT 'mc-atm10', 1 WHERE EXISTS (SELECT 1 FROM users WHERE id = 1)`,
		`INSERT OR IGNORE INTO server_ownership (server_name, owner_user_id) SELECT 'mc-atm9', 1 WHERE EXISTS (SELECT 1 FROM users WHERE id = 1)`,
		`INSERT OR IGNORE INTO server_ownership (server_name, owner_user_id) SELECT 'mc-atm9-tts', 1 WHERE EXISTS (SELECT 1 FROM users WHERE id = 1)`,
		`INSERT OR IGNORE INTO server_ownership (server_name, owner_user_id) SELECT 'mc-bedrock3', 1 WHERE EXISTS (SELECT 1 FROM users WHERE id = 1)`,
		`INSERT OR IGNORE INTO server_ownership (server_name, owner_user_id) SELECT 'mc-vanilla1', 1 WHERE EXISTS (SELECT 1 FROM users WHERE id = 1)`,
		`INSERT OR IGNORE INTO server_ownership (server_name, owner_user_id) SELECT 'mc-vanilla10', 1 WHERE EXISTS (SELECT 1 FROM users WHERE id = 1)`,
		`INSERT OR IGNORE INTO server_ownership (server_name, owner_user_id) SELECT 'mc-vanilla11', 1 WHERE EXISTS (SELECT 1 FROM users WHERE id = 1)`,
		`INSERT OR IGNORE INTO server_ownership (server_name, owner_user_id) SELECT 'mc-vanilla12', 1 WHERE EXISTS (SELECT 1 FROM users WHERE id = 1)`,
		`INSERT OR IGNORE INTO server_ownership (server_name, owner_user_id) SELECT 'mc-vanilla13', 1 WHERE EXISTS (SELECT 1 FROM users WHERE id = 1)`,
		`INSERT OR IGNORE INTO server_ownership (server_name, owner_user_id) SELECT 'mc-vanilla14', 1 WHERE EXISTS (SELECT 1 FROM users WHERE id = 1)`,
		`INSERT OR IGNORE INTO server_ownership (server_name, owner_user_id) SELECT 'mc-vanilla15', 1 WHERE EXISTS (SELECT 1 FROM users WHERE id = 1)`,
	}

	for _, m := range migrations {
		if _, err := d.db.Exec(m); err != nil {
			// Ignore duplicate column errors from ALTER TABLE migrations
			// that have already been applied.
			if strings.Contains(err.Error(), "duplicate column") {
				continue
			}
			return fmt.Errorf("exec migration: %w", err)
		}
	}
	return nil
}

// --- User operations ---

// CreateUser creates a new user account.
func (d *DB) CreateUser(username, passwordHash, role string) (*User, error) {
	verbosity := "kid"
	if role == "admin" {
		verbosity = "operator"
	}

	res, err := d.db.Exec(
		`INSERT INTO users (username, password_hash, role, verbosity_mode) VALUES (?, ?, ?, ?)`,
		username, passwordHash, role, verbosity,
	)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	id, _ := res.LastInsertId()
	return d.GetUserByID(id)
}

// GetUserByID retrieves a user by ID.
func (d *DB) GetUserByID(id int64) (*User, error) {
	u := &User{}
	err := d.db.QueryRow(
		`SELECT id, username, password_hash, role, verbosity_mode, active, max_servers, max_tokens, created_at FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.VerbosityMode, &u.Active, &u.MaxServers, &u.MaxTokens, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return u, nil
}

// GetUserByUsername retrieves a user by username.
func (d *DB) GetUserByUsername(username string) (*User, error) {
	u := &User{}
	err := d.db.QueryRow(
		`SELECT id, username, password_hash, role, verbosity_mode, active, max_servers, max_tokens, created_at FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.VerbosityMode, &u.Active, &u.MaxServers, &u.MaxTokens, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get user by username: %w", err)
	}
	return u, nil
}

// ListUsers returns all users.
func (d *DB) ListUsers() ([]*User, error) {
	rows, err := d.db.Query(
		`SELECT id, username, password_hash, role, verbosity_mode, active, max_servers, max_tokens, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.VerbosityMode, &u.Active, &u.MaxServers, &u.MaxTokens, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// UpdateUser updates a user's fields. Only non-zero-value fields are updated.
func (d *DB) UpdateUser(id int64, username, passwordHash, role, verbosityMode string, active *bool) error {
	u, err := d.GetUserByID(id)
	if err != nil {
		return err
	}
	if username != "" {
		u.Username = username
	}
	if passwordHash != "" {
		u.PasswordHash = passwordHash
	}
	if role != "" {
		u.Role = role
	}
	if verbosityMode != "" {
		u.VerbosityMode = verbosityMode
	}
	activeVal := u.Active
	if active != nil {
		activeVal = *active
	}

	_, err = d.db.Exec(
		`UPDATE users SET username=?, password_hash=?, role=?, verbosity_mode=?, active=? WHERE id=?`,
		u.Username, u.PasswordHash, u.Role, u.VerbosityMode, activeVal, id,
	)
	return err
}

// UpdateUserLimits sets per-user resource limits.
func (d *DB) UpdateUserLimits(id int64, maxServers, maxTokens int) error {
	_, err := d.db.Exec(`UPDATE users SET max_servers=?, max_tokens=? WHERE id=?`, maxServers, maxTokens, id)
	return err
}

// DeleteUser removes a user by ID.
func (d *DB) DeleteUser(id int64) error {
	_, err := d.db.Exec(`DELETE FROM users WHERE id=?`, id)
	return err
}

// --- Session operations ---

// GenerateSessionToken creates a random 32-byte hex session token and returns
// both the raw token (to set as cookie) and its SHA-256 hash (to store in DB).
func GenerateSessionToken() (token string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	token = hex.EncodeToString(b)
	h := sha256.Sum256([]byte(token))
	hash = hex.EncodeToString(h[:])
	return token, hash, nil
}

// HashSessionToken returns the SHA-256 hex hash of a session token.
func HashSessionToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// CreateSession stores a new session in the database.
func (d *DB) CreateSession(tokenHash string, userID int64, ttl time.Duration) error {
	expiresAt := time.Now().Add(ttl)
	_, err := d.db.Exec(
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		tokenHash, userID, expiresAt,
	)
	return err
}

// GetSessionByToken looks up a session by its token hash. Returns nil if expired or not found.
func (d *DB) GetSessionByToken(tokenHash string) (*Session, error) {
	s := &Session{}
	err := d.db.QueryRow(
		`SELECT id, token_hash, user_id, expires_at, created_at FROM sessions WHERE token_hash = ? AND expires_at > datetime('now')`,
		tokenHash,
	).Scan(&s.ID, &s.TokenHash, &s.UserID, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// TouchSession extends a session's expiry by the given TTL.
func (d *DB) TouchSession(tokenHash string, ttl time.Duration) error {
	expiresAt := time.Now().Add(ttl)
	_, err := d.db.Exec(`UPDATE sessions SET expires_at=? WHERE token_hash=?`, expiresAt, tokenHash)
	return err
}

// DeleteSession removes a session by its token hash.
func (d *DB) DeleteSession(tokenHash string) error {
	_, err := d.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenHash)
	return err
}

// CleanExpiredSessions removes all expired sessions.
func (d *DB) CleanExpiredSessions() error {
	_, err := d.db.Exec(`DELETE FROM sessions WHERE expires_at <= datetime('now')`)
	return err
}

// --- Conversation operations ---

// CreateConversation creates a new conversation for a user.
func (d *DB) CreateConversation(userID int64, title string) (*Conversation, error) {
	res, err := d.db.Exec(
		`INSERT INTO conversations (user_id, title) VALUES (?, ?)`,
		userID, title,
	)
	if err != nil {
		return nil, fmt.Errorf("create conversation: %w", err)
	}
	id, _ := res.LastInsertId()
	return d.GetConversation(id)
}

// GetConversation retrieves a conversation by ID.
func (d *DB) GetConversation(id int64) (*Conversation, error) {
	c := &Conversation{}
	err := d.db.QueryRow(
		`SELECT id, user_id, title, input_tokens, context_summary, created_at, updated_at FROM conversations WHERE id = ?`, id,
	).Scan(&c.ID, &c.UserID, &c.Title, &c.InputTokens, &c.ContextSummary, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	return c, nil
}

// ListConversations returns all conversations for a user, newest first.
func (d *DB) ListConversations(userID int64) ([]*Conversation, error) {
	rows, err := d.db.Query(
		`SELECT id, user_id, title, input_tokens, context_summary, created_at, updated_at FROM conversations WHERE user_id = ? ORDER BY updated_at DESC`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	var convs []*Conversation
	for rows.Next() {
		c := &Conversation{}
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.InputTokens, &c.ContextSummary, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		convs = append(convs, c)
	}
	return convs, rows.Err()
}

// DeleteConversation removes a conversation and its messages.
func (d *DB) DeleteConversation(id int64) error {
	_, err := d.db.Exec(`DELETE FROM conversations WHERE id=?`, id)
	return err
}

// GetContextSummary returns the rolling context summary for a conversation.
func (d *DB) GetContextSummary(convID int64) (string, error) {
	var summary string
	err := d.db.QueryRow(`SELECT context_summary FROM conversations WHERE id = ?`, convID).Scan(&summary)
	if err != nil {
		return "", fmt.Errorf("get context summary: %w", err)
	}
	return summary, nil
}

// SetContextSummary updates the rolling context summary for a conversation.
func (d *DB) SetContextSummary(convID int64, summary string) error {
	_, err := d.db.Exec(
		`UPDATE conversations SET context_summary = ?, updated_at = datetime('now') WHERE id = ?`,
		summary, convID,
	)
	return err
}

// UpdateConversationTokens adds tokens to a conversation's input_tokens count.
func (d *DB) UpdateConversationTokens(id int64, additionalTokens int64) error {
	_, err := d.db.Exec(
		`UPDATE conversations SET input_tokens = input_tokens + ?, updated_at = datetime('now') WHERE id = ?`,
		additionalTokens, id,
	)
	return err
}

// --- Message operations ---

// AddMessage adds a message to a conversation.
func (d *DB) AddMessage(conversationID int64, role, content string) (*Message, error) {
	res, err := d.db.Exec(
		`INSERT INTO messages (conversation_id, role, content) VALUES (?, ?, ?)`,
		conversationID, role, content,
	)
	if err != nil {
		return nil, fmt.Errorf("add message: %w", err)
	}

	// Update conversation's updated_at.
	_, _ = d.db.Exec(`UPDATE conversations SET updated_at = datetime('now') WHERE id = ?`, conversationID)

	id, _ := res.LastInsertId()
	m := &Message{}
	err = d.db.QueryRow(
		`SELECT id, conversation_id, role, content, created_at FROM messages WHERE id = ?`, id,
	).Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// GetMessages returns all messages in a conversation, in order.
func (d *DB) GetMessages(conversationID int64) ([]*Message, error) {
	rows, err := d.db.Query(
		`SELECT id, conversation_id, role, content, created_at FROM messages WHERE conversation_id = ? ORDER BY id`, conversationID,
	)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	var msgs []*Message
	for rows.Next() {
		m := &Message{}
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// GetLastMessage returns the most recent message in a conversation.
func (d *DB) GetLastMessage(conversationID int64) (*Message, error) {
	m := &Message{}
	err := d.db.QueryRow(
		`SELECT id, conversation_id, role, content, created_at FROM messages WHERE conversation_id = ? ORDER BY id DESC LIMIT 1`,
		conversationID,
	).Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// --- Server ownership operations ---

// CreateServerOwnership records that a user owns a server.
func (d *DB) CreateServerOwnership(serverName string, ownerID int64) error {
	_, err := d.db.Exec(
		`INSERT INTO server_ownership (server_name, owner_user_id) VALUES (?, ?)`,
		serverName, ownerID,
	)
	return err
}

// GetServerOwner returns the owner user ID for a server, or 0 if not found.
func (d *DB) GetServerOwner(serverName string) (int64, error) {
	var ownerID int64
	err := d.db.QueryRow(
		`SELECT owner_user_id FROM server_ownership WHERE server_name = ?`, serverName,
	).Scan(&ownerID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return ownerID, err
}

// ListServersByOwner returns all servers owned by a user.
func (d *DB) ListServersByOwner(ownerID int64) ([]*ServerOwnership, error) {
	rows, err := d.db.Query(
		`SELECT id, server_name, owner_user_id, created_at FROM server_ownership WHERE owner_user_id = ?`, ownerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []*ServerOwnership
	for rows.Next() {
		s := &ServerOwnership{}
		if err := rows.Scan(&s.ID, &s.ServerName, &s.OwnerID, &s.CreatedAt); err != nil {
			return nil, err
		}
		servers = append(servers, s)
	}
	return servers, rows.Err()
}

// ListAllServers returns all server ownership records.
func (d *DB) ListAllServers() ([]*ServerOwnership, error) {
	rows, err := d.db.Query(
		`SELECT id, server_name, owner_user_id, created_at FROM server_ownership ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []*ServerOwnership
	for rows.Next() {
		s := &ServerOwnership{}
		if err := rows.Scan(&s.ID, &s.ServerName, &s.OwnerID, &s.CreatedAt); err != nil {
			return nil, err
		}
		servers = append(servers, s)
	}
	return servers, rows.Err()
}

// CountServersByOwner returns the number of servers owned by a user.
func (d *DB) CountServersByOwner(ownerID int64) (int, error) {
	var count int
	err := d.db.QueryRow(
		`SELECT COUNT(*) FROM server_ownership WHERE owner_user_id = ?`, ownerID,
	).Scan(&count)
	return count, err
}

// DeleteServerOwnership removes a server ownership record.
func (d *DB) DeleteServerOwnership(serverName string) error {
	_, err := d.db.Exec(`DELETE FROM server_ownership WHERE server_name=?`, serverName)
	return err
}

// --- Usage tracking ---

// TokenUsage represents token usage for a user.
type TokenUsage struct {
	UserID      int64  `json:"user_id"`
	Username    string `json:"username"`
	TotalTokens int64  `json:"total_tokens"`
}

// GetTokenUsage returns token usage per user.
func (d *DB) GetTokenUsage() ([]*TokenUsage, error) {
	rows, err := d.db.Query(`
		SELECT u.id, u.username, COALESCE(SUM(c.input_tokens), 0) as total_tokens
		FROM users u
		LEFT JOIN conversations c ON c.user_id = u.id
		GROUP BY u.id
		ORDER BY total_tokens DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var usage []*TokenUsage
	for rows.Next() {
		u := &TokenUsage{}
		if err := rows.Scan(&u.UserID, &u.Username, &u.TotalTokens); err != nil {
			return nil, err
		}
		usage = append(usage, u)
	}
	return usage, rows.Err()
}

// --- Async operation tracking ---

// CreateAsyncOp records a new pending async operation.
func (d *DB) CreateAsyncOp(convID, userID int64, toolName, opID, serverName, description string) error {
	_, err := d.db.Exec(
		`INSERT INTO async_operations (conversation_id, user_id, tool_name, operation_id, server_name, description) VALUES (?, ?, ?, ?, ?, ?)`,
		convID, userID, toolName, opID, serverName, description,
	)
	return err
}

// ListPendingOps returns all pending async operations for a conversation.
func (d *DB) ListPendingOps(convID int64) ([]*AsyncOp, error) {
	rows, err := d.db.Query(
		`SELECT id, conversation_id, user_id, tool_name, operation_id, server_name, description, status, created_at
		 FROM async_operations WHERE conversation_id = ? AND status = 'pending' ORDER BY id`, convID,
	)
	if err != nil {
		return nil, fmt.Errorf("list pending ops: %w", err)
	}
	defer rows.Close()

	var ops []*AsyncOp
	for rows.Next() {
		op := &AsyncOp{}
		if err := rows.Scan(&op.ID, &op.ConversationID, &op.UserID, &op.ToolName, &op.OperationID, &op.ServerName, &op.Description, &op.Status, &op.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan async op: %w", err)
		}
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

// UpdateAsyncOpStatus updates the status of an async operation by its operation ID.
func (d *DB) UpdateAsyncOpStatus(opID string, status string) error {
	_, err := d.db.Exec(`UPDATE async_operations SET status = ? WHERE operation_id = ? AND status = 'pending'`, status, opID)
	return err
}

// CleanOldOps removes async operations older than 24 hours.
func (d *DB) CleanOldOps() error {
	_, err := d.db.Exec(`DELETE FROM async_operations WHERE created_at < datetime('now', '-24 hours')`)
	return err
}

// ListAllPendingOps returns all pending async operations across all users. Used by the background poller.
func (d *DB) ListAllPendingOps() ([]AsyncOp, error) {
	rows, err := d.db.Query(
		`SELECT id, conversation_id, user_id, tool_name, operation_id, server_name, description, status, created_at
		 FROM async_operations WHERE status = 'pending' ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list all pending ops: %w", err)
	}
	defer rows.Close()

	var ops []AsyncOp
	for rows.Next() {
		var op AsyncOp
		if err := rows.Scan(&op.ID, &op.ConversationID, &op.UserID, &op.ToolName, &op.OperationID, &op.ServerName, &op.Description, &op.Status, &op.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan async op: %w", err)
		}
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

// ListPendingOpsByUser returns all pending async operations for a specific user. Used by the notification endpoint.
func (d *DB) ListPendingOpsByUser(userID int64) ([]AsyncOp, error) {
	rows, err := d.db.Query(
		`SELECT id, conversation_id, user_id, tool_name, operation_id, server_name, description, status, created_at
		 FROM async_operations WHERE user_id = ? AND status = 'pending' ORDER BY id`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list pending ops by user: %w", err)
	}
	defer rows.Close()

	var ops []AsyncOp
	for rows.Next() {
		var op AsyncOp
		if err := rows.Scan(&op.ID, &op.ConversationID, &op.UserID, &op.ToolName, &op.OperationID, &op.ServerName, &op.Description, &op.Status, &op.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan async op: %w", err)
		}
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

// CountRecentAutoContinuations counts auto-continuation messages in a conversation since the given time.
func (d *DB) CountRecentAutoContinuations(convID int64, since time.Time) (int, error) {
	var count int
	err := d.db.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE conversation_id = ? AND role = 'user' AND content LIKE '[System]%' AND created_at >= ?`,
		convID, since,
	).Scan(&count)
	return count, err
}

// ensureFilePermissions creates the file if it doesn't exist and sets
// its permissions to the given mode. If the file already exists, it
// only adjusts permissions.
func ensureFilePermissions(path string, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	f.Close()
	return os.Chmod(path, mode)
}
