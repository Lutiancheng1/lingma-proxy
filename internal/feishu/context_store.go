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
		`CREATE INDEX IF NOT EXISTS idx_tool_memories_conversation ON tool_memories(conversation_key, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_memories_tool_name ON tool_memories(tool_name, created_at)`,
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
		`CREATE TABLE IF NOT EXISTS scheduled_tasks (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			name TEXT NOT NULL,
			prompt TEXT NOT NULL,
			schedule_kind TEXT NOT NULL,
			at TEXT,
			every_seconds INTEGER DEFAULT 0,
			timezone TEXT,
			model TEXT,
			enabled INTEGER DEFAULT 1,
			delete_after_run INTEGER DEFAULT 0,
			next_run_at TEXT,
			last_run_at TEXT,
			last_status TEXT,
			last_error TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_due ON scheduled_tasks(enabled, next_run_at)`,
		`CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_chat ON scheduled_tasks(chat_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS scheduled_task_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT,
			status TEXT,
			output TEXT,
			error TEXT
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	// FTS5 virtual table + triggers for tool_memories full-text search
	ftsStmts := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS tool_memories_fts USING fts5(
			id, tool_name, summary, full_result,
			content='tool_memories',
			content_rowid='rowid'
		)`,
		`CREATE TRIGGER IF NOT EXISTS tool_memories_ai AFTER INSERT ON tool_memories BEGIN
			INSERT INTO tool_memories_fts(rowid, id, tool_name, summary, full_result)
			VALUES (new.rowid, new.id, new.tool_name, new.summary, new.full_result);
		END`,
		`CREATE TRIGGER IF NOT EXISTS tool_memories_ad AFTER DELETE ON tool_memories BEGIN
			INSERT INTO tool_memories_fts(tool_memories_fts, rowid, id, tool_name, summary, full_result)
			VALUES ('delete', old.rowid, old.id, old.tool_name, old.summary, old.full_result);
		END`,
		`CREATE TRIGGER IF NOT EXISTS tool_memories_au AFTER UPDATE ON tool_memories BEGIN
			INSERT INTO tool_memories_fts(tool_memories_fts, rowid, id, tool_name, summary, full_result)
			VALUES ('delete', old.rowid, old.id, old.tool_name, old.summary, old.full_result);
			INSERT INTO tool_memories_fts(rowid, id, tool_name, summary, full_result)
			VALUES (new.rowid, new.id, new.tool_name, new.summary, new.full_result);
		END`,
		`CREATE TABLE IF NOT EXISTS feishu_history_cache (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id TEXT NOT NULL,
			query TEXT NOT NULL,
			result TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_feishu_history_cache_lookup ON feishu_history_cache(chat_id, created_at)`,
	}
	for _, stmt := range ftsStmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	// Backfill FTS index from existing tool_memories rows
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO tool_memories_fts(rowid, id, tool_name, summary, full_result)
		 SELECT rowid, id, tool_name, summary, full_result FROM tool_memories`); err != nil {
		return err
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
	summary := smartToolSummary(toolName, fullResult, isError)
	_, err := s.db.ExecContext(ctx, `INSERT INTO tool_memories
		(id, conversation_key, tool_name, args_hash, args_json, summary, full_result, is_error, replayable, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		id, conversationKey, toolName, argsHash, string(argsJSON), summary, fullResult, errFlag, time.Now().Format(time.RFC3339))
	return id, err
}

type ToolMemorySearchResult struct {
	ID         string  `json:"id"`
	ToolName   string  `json:"tool_name"`
	Summary    string  `json:"summary"`
	FullResult string  `json:"full_result,omitempty"`
	CreatedAt  string  `json:"created_at"`
	Rank       float64 `json:"rank"`
}

func (s *bridgeStore) SearchToolMemory(ctx context.Context, conversationKey string, query string, limit int) ([]ToolMemorySearchResult, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	safeQuery := sanitizeFTSQuery(query)
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.tool_name, m.summary, m.created_at, rank
		 FROM tool_memories_fts fts
		 JOIN tool_memories m ON m.rowid = fts.rowid
		 WHERE tool_memories_fts MATCH ? AND m.conversation_key = ?
		 ORDER BY rank
		 LIMIT ?`, safeQuery, conversationKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []ToolMemorySearchResult
	for rows.Next() {
		var r ToolMemorySearchResult
		if err := rows.Scan(&r.ID, &r.ToolName, &r.Summary, &r.CreatedAt, &r.Rank); err != nil {
			continue
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *bridgeStore) FetchToolMemory(ctx context.Context, id string) (ToolMemorySearchResult, error) {
	if s == nil || s.db == nil {
		return ToolMemorySearchResult{}, fmt.Errorf("store not initialized")
	}
	var r ToolMemorySearchResult
	err := s.db.QueryRowContext(ctx,
		`SELECT id, tool_name, summary, full_result, created_at FROM tool_memories WHERE id = ?`,
		id).Scan(&r.ID, &r.ToolName, &r.Summary, &r.FullResult, &r.CreatedAt)
	return r, err
}

func sanitizeFTSQuery(query string) string {
	query = strings.TrimSpace(query)
	query = strings.NewReplacer(
		`"`, `""`,
		`*`, ``,
		`(`, ``,
		`)`, ``,
		`:`, ` `,
	).Replace(query)
	words := strings.Fields(query)
	if len(words) == 0 {
		return `""`
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = `"` + w + `"`
	}
	return strings.Join(quoted, " OR ")
}

func (s *bridgeStore) SaveFeishuHistoryCache(ctx context.Context, chatID, query, result string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO feishu_history_cache (chat_id, query, result, created_at) VALUES (?, ?, ?, ?)`,
		chatID, query, result, time.Now().Format(time.RFC3339))
	return err
}

func (s *bridgeStore) LoadFeishuHistoryCache(ctx context.Context, chatID, query string, maxAge time.Duration) (string, bool) {
	if s == nil || s.db == nil {
		return "", false
	}
	cutoff := time.Now().Add(-maxAge).Format(time.RFC3339)
	var result string
	err := s.db.QueryRowContext(ctx,
		`SELECT result FROM feishu_history_cache WHERE chat_id = ? AND query = ? AND created_at >= ? ORDER BY created_at DESC LIMIT 1`,
		chatID, query, cutoff).Scan(&result)
	if err != nil {
		return "", false
	}
	return result, true
}

func (s *bridgeStore) CleanupStaleHistoryCache(ctx context.Context, maxAge time.Duration) error {
	if s == nil || s.db == nil {
		return nil
	}
	cutoff := time.Now().Add(-maxAge).Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `DELETE FROM feishu_history_cache WHERE created_at < ?`, cutoff)
	return err
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

func (s *bridgeStore) DeleteAllSkills(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM skills`)
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

func (s *bridgeStore) SaveScheduledTask(ctx context.Context, task ScheduledTask) error {
	if s == nil || s.db == nil || strings.TrimSpace(task.ID) == "" {
		return nil
	}
	now := time.Now().Format(time.RFC3339)
	if strings.TrimSpace(task.CreatedAt) == "" {
		task.CreatedAt = now
	}
	task.UpdatedAt = now
	enabled := 0
	if task.Enabled {
		enabled = 1
	}
	deleteAfterRun := 0
	if task.DeleteAfterRun {
		deleteAfterRun = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO scheduled_tasks
		(id, chat_id, name, prompt, schedule_kind, at, every_seconds, timezone, model, enabled, delete_after_run, next_run_at, last_run_at, last_status, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			chat_id=excluded.chat_id,
			name=excluded.name,
			prompt=excluded.prompt,
			schedule_kind=excluded.schedule_kind,
			at=excluded.at,
			every_seconds=excluded.every_seconds,
			timezone=excluded.timezone,
			model=excluded.model,
			enabled=excluded.enabled,
			delete_after_run=excluded.delete_after_run,
			next_run_at=excluded.next_run_at,
			last_run_at=excluded.last_run_at,
			last_status=excluded.last_status,
			last_error=excluded.last_error,
			updated_at=excluded.updated_at`,
		task.ID, task.ChatID, task.Name, task.Prompt, task.ScheduleKind, task.At, task.EverySeconds, task.Timezone, task.Model,
		enabled, deleteAfterRun, task.NextRunAt, task.LastRunAt, task.LastStatus, task.LastError, task.CreatedAt, task.UpdatedAt)
	return err
}

func (s *bridgeStore) ListScheduledTasks(ctx context.Context, chatID string, includeDisabled bool) ([]ScheduledTask, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	query := `SELECT id, chat_id, name, prompt, schedule_kind, at, every_seconds, timezone, model, enabled, delete_after_run, next_run_at, last_run_at, last_status, last_error, created_at, updated_at
		FROM scheduled_tasks`
	var args []any
	var filters []string
	if strings.TrimSpace(chatID) != "" {
		filters = append(filters, "chat_id = ?")
		args = append(args, strings.TrimSpace(chatID))
	}
	if !includeDisabled {
		filters = append(filters, "enabled = 1")
	}
	if len(filters) > 0 {
		query += " WHERE " + strings.Join(filters, " AND ")
	}
	query += " ORDER BY next_run_at IS NULL, next_run_at, created_at"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScheduledTasks(rows)
}

func (s *bridgeStore) GetScheduledTask(ctx context.Context, id string) (ScheduledTask, error) {
	if s == nil || s.db == nil {
		return ScheduledTask{}, sql.ErrNoRows
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, chat_id, name, prompt, schedule_kind, at, every_seconds, timezone, model, enabled, delete_after_run, next_run_at, last_run_at, last_status, last_error, created_at, updated_at
		FROM scheduled_tasks WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return ScheduledTask{}, err
	}
	defer rows.Close()
	tasks, err := scanScheduledTasks(rows)
	if err != nil {
		return ScheduledTask{}, err
	}
	if len(tasks) == 0 {
		return ScheduledTask{}, sql.ErrNoRows
	}
	return tasks[0], nil
}

func (s *bridgeStore) DeleteScheduledTask(ctx context.Context, id string) error {
	if s == nil || s.db == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE id = ?`, strings.TrimSpace(id))
	return err
}

func (s *bridgeStore) SetScheduledTaskEnabled(ctx context.Context, id string, enabled bool) error {
	if s == nil || s.db == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	value := 0
	if enabled {
		value = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE scheduled_tasks SET enabled = ?, updated_at = ? WHERE id = ?`, value, time.Now().Format(time.RFC3339), strings.TrimSpace(id))
	return err
}

func (s *bridgeStore) DueScheduledTasks(ctx context.Context, now time.Time, limit int) ([]ScheduledTask, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, chat_id, name, prompt, schedule_kind, at, every_seconds, timezone, model, enabled, delete_after_run, next_run_at, last_run_at, last_status, last_error, created_at, updated_at
		FROM scheduled_tasks
		WHERE enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= ?
		ORDER BY next_run_at
		LIMIT ?`, now.Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScheduledTasks(rows)
}

func (s *bridgeStore) FinishScheduledTaskRun(ctx context.Context, task ScheduledTask, status string, output string, errText string, finishedAt time.Time, nextRunAt string, enabled bool) error {
	if s == nil || s.db == nil || strings.TrimSpace(task.ID) == "" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduled_task_runs
		(task_id, started_at, finished_at, status, output, error)
		VALUES (?, ?, ?, ?, ?, ?)`,
		task.ID, task.LastRunAt, finishedAt.Format(time.RFC3339), status, summarizeText(output, 4000), summarizeText(errText, 2000)); err != nil {
		return err
	}
	enabledValue := 0
	if enabled {
		enabledValue = 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_tasks SET
		enabled = ?,
		next_run_at = ?,
		last_run_at = ?,
		last_status = ?,
		last_error = ?,
		updated_at = ?
		WHERE id = ?`,
		enabledValue, nextRunAt, finishedAt.Format(time.RFC3339), status, summarizeText(errText, 1000), finishedAt.Format(time.RFC3339), task.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func scanScheduledTasks(rows *sql.Rows) ([]ScheduledTask, error) {
	var out []ScheduledTask
	for rows.Next() {
		var task ScheduledTask
		var enabled int
		var deleteAfterRun int
		if err := rows.Scan(&task.ID, &task.ChatID, &task.Name, &task.Prompt, &task.ScheduleKind, &task.At, &task.EverySeconds, &task.Timezone, &task.Model, &enabled, &deleteAfterRun, &task.NextRunAt, &task.LastRunAt, &task.LastStatus, &task.LastError, &task.CreatedAt, &task.UpdatedAt); err != nil {
			return nil, err
		}
		task.Enabled = enabled != 0
		task.DeleteAfterRun = deleteAfterRun != 0
		out = append(out, task)
	}
	return out, rows.Err()
}
