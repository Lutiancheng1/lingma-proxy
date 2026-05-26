package feishu

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type bridgeStore struct {
	db *sql.DB
}

func newBridgeStore(dataDir string) (*bridgeStore, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "feishu-bridge.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &bridgeStore{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *bridgeStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *bridgeStore) migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS conversations (
			conversation_key TEXT PRIMARY KEY,
			chat_id TEXT,
			root_message_id TEXT,
			model TEXT,
			language TEXT,
			show_thinking INTEGER,
			summary TEXT,
			compact_boundary INTEGER DEFAULT 0,
			snapshot_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS conversation_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_key TEXT NOT NULL,
			turn_index INTEGER NOT NULL,
			sequence_index INTEGER NOT NULL,
			role TEXT,
			message_json TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_conversation_messages_key ON conversation_messages(conversation_key, sequence_index)`,
		`CREATE TABLE IF NOT EXISTS conversation_summaries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_key TEXT NOT NULL,
			summary_json TEXT NOT NULL,
			model TEXT,
			from_index INTEGER DEFAULT 0,
			to_index INTEGER DEFAULT 0,
			pre_tokens INTEGER DEFAULT 0,
			post_tokens INTEGER DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS tool_memories (
			id TEXT PRIMARY KEY,
			conversation_key TEXT,
			tool_name TEXT,
			args_hash TEXT,
			args_json TEXT,
			summary TEXT,
			full_result TEXT,
			is_error INTEGER DEFAULT 0,
			replayable INTEGER DEFAULT 0,
			expires_at TEXT,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS artifacts (
			id TEXT PRIMARY KEY,
			conversation_key TEXT,
			kind TEXT,
			title TEXT,
			uri TEXT,
			summary TEXT,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS skills (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			version TEXT,
			when_to_use TEXT,
			path TEXT NOT NULL,
			source TEXT,
			hash TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			error TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_skills_name ON skills(name)`,
		`CREATE TABLE IF NOT EXISTS skill_invocations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_key TEXT NOT NULL,
			skill_id TEXT NOT NULL,
			skill_name TEXT NOT NULL,
			tool_call_id TEXT,
			created_at TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *bridgeStore) SaveConversationSnapshot(ctx context.Context, key string, snapshot ConversationSnapshot) error {
	if s == nil || s.db == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	now := time.Now().Format(time.RFC3339)
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	showThinking := sql.NullInt64{}
	if snapshot.ShowThinking != nil {
		if *snapshot.ShowThinking {
			showThinking.Int64 = 1
		}
		showThinking.Valid = true
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO conversations
		(conversation_key, chat_id, model, language, show_thinking, summary, compact_boundary, snapshot_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(conversation_key) DO UPDATE SET
			model=excluded.model,
			language=excluded.language,
			show_thinking=excluded.show_thinking,
			summary=excluded.summary,
			compact_boundary=excluded.compact_boundary,
			snapshot_json=excluded.snapshot_json,
			updated_at=excluded.updated_at`,
		key, key, snapshot.ModelOverride, snapshot.Language, showThinking, snapshot.Summary, snapshot.CompactBoundary, string(data), now, now,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_messages WHERE conversation_key = ?`, key); err != nil {
		return err
	}
	for i, msg := range snapshot.History {
		msgData, _ := json.Marshal(msg)
		role, _ := msg["role"].(string)
		if _, err := tx.ExecContext(ctx, `INSERT INTO conversation_messages
			(conversation_key, turn_index, sequence_index, role, message_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, key, i, i, role, string(msgData), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *bridgeStore) SaveSummary(ctx context.Context, key string, model string, summary StructuredSummary, from, to, preTokens, postTokens int) error {
	if s == nil || s.db == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO conversation_summaries
		(conversation_key, summary_json, model, from_index, to_index, pre_tokens, post_tokens, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		key, string(data), model, from, to, preTokens, postTokens, time.Now().Format(time.RFC3339))
	return err
}

func (s *bridgeStore) ClearConversation(ctx context.Context, key string) error {
	if s == nil || s.db == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`DELETE FROM conversations WHERE conversation_key = ?`,
		`DELETE FROM conversation_messages WHERE conversation_key = ?`,
		`DELETE FROM conversation_summaries WHERE conversation_key = ?`,
		`DELETE FROM tool_memories WHERE conversation_key = ?`,
		`DELETE FROM artifacts WHERE conversation_key = ?`,
		`DELETE FROM skill_invocations WHERE conversation_key = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *bridgeStore) SaveToolMemory(ctx context.Context, conversationKey, toolName string, args map[string]any, fullResult string, isError bool) (string, error) {
	if s == nil || s.db == nil {
		return "", nil
	}
	argsJSON, _ := json.Marshal(args)
	hash := sha256.Sum256(argsJSON)
	argsHash := hex.EncodeToString(hash[:8])
	idSeed := fmt.Sprintf("%s:%s:%s:%d", conversationKey, toolName, argsHash, time.Now().UnixNano())
	idHash := sha256.Sum256([]byte(idSeed))
	id := "tool_" + hex.EncodeToString(idHash[:8])
	errFlag := 0
	if isError {
		errFlag = 1
	}
	summary := summarizeText(fullResult, 500)
	_, err := s.db.ExecContext(ctx, `INSERT INTO tool_memories
		(id, conversation_key, tool_name, args_hash, args_json, summary, full_result, is_error, replayable, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		id, conversationKey, toolName, argsHash, string(argsJSON), summary, fullResult, errFlag, time.Now().Format(time.RFC3339))
	return id, err
}

func (s *bridgeStore) UpsertSkill(ctx context.Context, skill BridgeSkill) error {
	if s == nil || s.db == nil || strings.TrimSpace(skill.ID) == "" {
		return nil
	}
	now := time.Now().Format(time.RFC3339)
	enabled := 0
	if skill.Enabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO skills
		(id, name, description, version, when_to_use, path, source, hash, enabled, error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			description=excluded.description,
			version=excluded.version,
			when_to_use=excluded.when_to_use,
			path=excluded.path,
			source=excluded.source,
			hash=excluded.hash,
			enabled=excluded.enabled,
			error=excluded.error,
			updated_at=excluded.updated_at`,
		skill.ID, skill.Name, skill.Description, skill.Version, skill.WhenToUse, skill.Path, skill.Source, skill.Hash, enabled, skill.Error, now, now)
	return err
}

func (s *bridgeStore) DeleteSkill(ctx context.Context, id string) error {
	if s == nil || s.db == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM skills WHERE id = ?`, id)
	return err
}

func (s *bridgeStore) SetSkillEnabled(ctx context.Context, id string, enabled bool) error {
	if s == nil || s.db == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	value := 0
	if enabled {
		value = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE skills SET enabled = ?, updated_at = ? WHERE id = ?`, value, time.Now().Format(time.RFC3339), id)
	return err
}

func (s *bridgeStore) LoadSkills(ctx context.Context) ([]BridgeSkill, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, description, version, when_to_use, path, source, hash, enabled, error, created_at, updated_at FROM skills ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BridgeSkill
	for rows.Next() {
		var skill BridgeSkill
		var enabled int
		if err := rows.Scan(&skill.ID, &skill.Name, &skill.Description, &skill.Version, &skill.WhenToUse, &skill.Path, &skill.Source, &skill.Hash, &enabled, &skill.Error, &skill.CreatedAt, &skill.UpdatedAt); err != nil {
			return nil, err
		}
		skill.Enabled = enabled != 0
		out = append(out, skill)
	}
	return out, rows.Err()
}

func (s *bridgeStore) SaveSkillInvocation(ctx context.Context, conversationKey, skillID, skillName, toolCallID string) error {
	if s == nil || s.db == nil || strings.TrimSpace(conversationKey) == "" || strings.TrimSpace(skillID) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO skill_invocations
		(conversation_key, skill_id, skill_name, tool_call_id, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		conversationKey, skillID, skillName, toolCallID, time.Now().Format(time.RFC3339))
	return err
}
