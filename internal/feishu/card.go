package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// Element IDs used in the CardKit streaming card (schema 2.0).
	cardElementReply = "reply_md"
	cardElementSteps = "steps_md"
	cardElementHint  = "hint_md"

	finalCardStepBodyLimit     = 700
	compactFinalReplyLimit     = 900
	compactFinalStepLimit      = 6
	compactFinalStepBodyLimit  = 320
	finalCardJSONSoftLimit     = 12000
	streamingReplyPreviewLimit = 3200
	cardOperationRetryCount    = 3
)

// cardState is the in-memory model used to render the streaming reply card.
type cardState struct {
	Title       string
	Status      string // "thinking" | "tool" | "done" | "stopped" | "error"
	StatusLabel string
	Steps       []cardStep
	Reply       string
	Hint        string
	Model       string
	Elapsed     time.Duration
	Updated     time.Time
}

type cardStep struct {
	Kind  string // "thought" | "tool" | "result" | "error"
	Title string
	Body  string
	Tool  string
	Done  bool
}

// renderStreamingCardV2 builds a CardKit schema 2.0 JSON with streaming_mode
// enabled. It creates fixed element_id slots so streaming updates can target
// individual elements without re-sending the whole card.
func renderStreamingCardV2(state cardState) (string, error) {
	card := map[string]any{
		"schema": "2.0",
		"header": map[string]any{
			"title": map[string]any{
				"tag":     "plain_text",
				"content": cardHeaderTitle(state),
			},
			"template": cardHeaderTemplate(state.Status),
		},
		"config": map[string]any{
			"streaming_mode": true,
			"update_multi":   true,
			"summary":        map[string]any{"content": "[生成中...]"},
			"streaming_config": map[string]any{
				"print_frequency_ms": map[string]any{"default": 70},
				"print_step":         map[string]any{"default": 2},
				"print_strategy":     "fast",
			},
		},
		"body": map[string]any{
			"elements": buildStreamingElements(state),
		},
	}
	payload, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("marshal card v2: %w", err)
	}
	return string(payload), nil
}

// renderFinalCardV2 builds the same card but with streaming_mode=false for the
// final state. Also updates the summary for chat preview.
func renderFinalCardV2(state cardState) (string, error) {
	summary := state.StatusLabel
	if summary == "" {
		summary = "完成"
	}
	card := map[string]any{
		"schema": "2.0",
		"header": map[string]any{
			"title": map[string]any{
				"tag":     "plain_text",
				"content": cardHeaderTitle(state),
			},
			"template": cardHeaderTemplate(state.Status),
		},
		"config": map[string]any{
			"streaming_mode": false,
			"update_multi":   true,
			"summary":        map[string]any{"content": summary},
		},
		"body": map[string]any{
			"elements": buildFinalElements(state),
		},
	}
	payload, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("marshal final card v2: %w", err)
	}
	return string(payload), nil
}

func buildStreamingElements(state cardState) []map[string]any {
	elements := []map[string]any{}
	// Always include all element_ids so streamUpdateCardContent can target them
	// later even if they were empty at card creation time.
	elements = append(elements, map[string]any{
		"tag":        "markdown",
		"content":    cardSubtitle(state),
		"element_id": "subtitle_md",
	})
	stepsContent := renderStepsMarkdown(state.Steps)
	elements = append(elements, map[string]any{
		"tag":        "markdown",
		"content":    stepsContent,
		"element_id": cardElementSteps,
	})
	for i, step := range state.Steps {
		if stepElement := renderStreamingStepElement(step, i); stepElement != nil {
			elements = append(elements, stepElement)
		}
	}
	elements = append(elements, map[string]any{
		"tag":        "markdown",
		"content":    streamingReplyContent(state.Reply),
		"element_id": cardElementReply,
	})
	elements = append(elements, map[string]any{
		"tag":        "markdown",
		"content":    strings.TrimSpace(state.Hint),
		"element_id": cardElementHint,
	})
	return elements
}

func renderStreamingStepElement(step cardStep, index int) map[string]any {
	if step.Kind != "tool" || !step.Done {
		return nil
	}
	return renderStepElement(step, index)
}

func cardStructureSignature(state cardState) string {
	var b strings.Builder
	for _, step := range state.Steps {
		if step.Kind != "tool" || !step.Done {
			continue
		}
		b.WriteString(step.Kind)
		b.WriteString("\x00")
		b.WriteString(step.Title)
		b.WriteString("\x00")
		b.WriteString(step.Tool)
		b.WriteString("\x00")
		b.WriteString(step.Body)
		b.WriteString("\x1e")
	}
	return b.String()
}

func buildFinalElements(state cardState) []map[string]any {
	elements := []map[string]any{}
	elements = append(elements, map[string]any{
		"tag":        "markdown",
		"content":    cardSubtitle(state),
		"element_id": "subtitle_md",
	})
	for i, step := range state.Steps {
		stepElement := renderStepElement(step, i)
		if stepElement != nil {
			elements = append(elements, stepElement)
		}
	}
	replyContent := strings.TrimSpace(state.Reply)
	if replyContent == "" {
		replyContent = " "
	}
	elements = append(elements, map[string]any{
		"tag":        "markdown",
		"content":    replyContent,
		"element_id": cardElementReply,
	})
	if hint := strings.TrimSpace(state.Hint); hint != "" {
		elements = append(elements, map[string]any{
			"tag":        "markdown",
			"content":    hint,
			"element_id": cardElementHint,
		})
	}
	return elements
}

func renderStepElement(step cardStep, index int) map[string]any {
	title := strings.TrimSpace(step.Title)
	if title == "" {
		title = step.Kind
	}
	icon := stepIcon(step)
	body := strings.TrimSpace(step.Body)
	if body != "" {
		body = summarizeText(body, finalCardStepBodyLimit)
	}
	if step.Kind == "tool" && body != "" {
		return map[string]any{
			"tag":              "collapsible_panel",
			"element_id":       fmt.Sprintf("tool_panel_%02d", index%100),
			"expanded":         false,
			"background_color": "grey",
			"header": map[string]any{
				"title": map[string]any{
					"tag":     "markdown",
					"content": fmt.Sprintf("%s **%s**", icon, title),
				},
				"padding": "4px 0px 4px 8px",
			},
			"elements": []any{
				map[string]any{
					"tag":     "markdown",
					"content": body,
				},
			},
			"padding":          "4px 0px 4px 0px",
			"vertical_spacing": "small",
		}
	}
	content := fmt.Sprintf("%s **%s**", icon, title)
	if body != "" {
		content += "\n" + body
	}
	return map[string]any{
		"tag":        "markdown",
		"content":    content,
		"element_id": fmt.Sprintf("step_md_%02d", index%100),
	}
}

func stepIcon(step cardStep) string {
	icon := "🧠"
	switch step.Kind {
	case "tool":
		icon = "🛠️"
		if step.Done {
			icon = "✅"
		}
	case "result":
		icon = "✅"
	case "error":
		icon = "⚠️"
	}
	return icon
}

// renderStepsMarkdown renders a compact step summary while the card streams.
func renderStepsMarkdown(steps []cardStep) string {
	if len(steps) == 0 {
		return ""
	}
	var b strings.Builder
	for _, step := range steps {
		if step.Kind == "tool" {
			continue
		}
		icon := stepIcon(step)
		title := strings.TrimSpace(step.Title)
		if title == "" {
			title = step.Kind
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("%s **%s**", icon, title))
		if body := strings.TrimSpace(step.Body); body != "" {
			if step.Kind == "error" {
				b.WriteString("\n" + summarizeText(body, 240))
			}
		}
	}
	return b.String()
}

// renderCardV1 builds a schema 1.0 interactive card (legacy format) used as a
// fallback when CardKit is unavailable. This format works with the plain
// `im +messages-reply --msg-type interactive` endpoint.
func renderCardV1(state cardState) (string, error) {
	elements := []any{}
	if subtitle := cardSubtitle(state); subtitle != "" {
		elements = append(elements, map[string]any{
			"tag": "note",
			"elements": []any{
				map[string]any{"tag": "lark_md", "content": subtitle},
			},
		})
	}
	stepsContent := renderStepsMarkdown(state.Steps)
	if stepsContent != "" {
		elements = append(elements, map[string]any{
			"tag":  "div",
			"text": map[string]any{"tag": "lark_md", "content": stepsContent},
		})
	}
	if reply := strings.TrimSpace(state.Reply); reply != "" {
		elements = append(elements, map[string]any{"tag": "hr"})
		elements = append(elements, map[string]any{
			"tag":  "div",
			"text": map[string]any{"tag": "lark_md", "content": reply},
		})
	}
	if hint := strings.TrimSpace(state.Hint); hint != "" {
		elements = append(elements, map[string]any{
			"tag": "note",
			"elements": []any{
				map[string]any{"tag": "lark_md", "content": hint},
			},
		})
	}
	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
			"update_multi":     true,
		},
		"header": map[string]any{
			"template": cardHeaderTemplate(state.Status),
			"title": map[string]any{
				"tag":     "plain_text",
				"content": cardHeaderTitle(state),
			},
		},
		"elements": elements,
	}
	payload, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("marshal card v1: %w", err)
	}
	return string(payload), nil
}

func cardHeaderTitle(state cardState) string {
	title := strings.TrimSpace(state.Title)
	if label := strings.TrimSpace(state.StatusLabel); label != "" {
		if title == "" {
			return label
		}
		return title + " " + label
	}
	return title
}

func cardHeaderTemplate(status string) string {
	switch status {
	case "done":
		return "green"
	case "stopped":
		return "grey"
	case "preempted":
		return "yellow"
	case "error":
		return "red"
	case "tool":
		return "indigo"
	default:
		return "blue"
	}
}

func cardSubtitle(state cardState) string {
	parts := []string{}
	if model := strings.TrimSpace(state.Model); model != "" {
		parts = append(parts, "模型 `"+model+"`")
	}
	if state.Elapsed > 0 {
		parts = append(parts, "耗时 "+formatCardDuration(state.Elapsed))
	}
	return strings.Join(parts, " · ")
}

func formatCardDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}

// cardWriter coordinates CardKit streaming card updates.
//
// Lifecycle:
//  1. createCardEntity → sendCardEntityMessage  (initial card with streaming_mode=true)
//  2. streamUpdateCardContent                    (per-element streaming text updates)
//  3. Finalize → updateCardSettings(off) + full card update
//
// If CardKit is unavailable (e.g. missing cardkit:card:write scope), it
// transparently falls back to the legacy PATCH /im/v1/messages path.
type cardWriter struct {
	manager     *Manager
	rootMessage string
	logMeta     LogMeta
	flushDelay  time.Duration
	mute        bool

	mu            sync.Mutex
	state         cardState
	cardEntityID  string // CardKit card_id (from POST /cardkit/v1/cards)
	cardMsgID     string // im message_id of the sent card message
	sequence      int    // monotonic counter for CardKit API calls
	dirty         bool
	flushing      bool
	flushCancel   context.CancelFunc
	closed        bool
	startedAt     time.Time
	timer         *time.Timer
	cardBroken    bool
	useCardKit    bool // true = CardKit streaming; false = legacy PATCH fallback
	streamStarted bool // logs the first successful streamUpdateCardContent
	structureSig  string
}

func newCardWriter(m *Manager, rootMessage string, title string, model string, meta LogMeta) *cardWriter {
	return &cardWriter{
		manager:     m,
		rootMessage: rootMessage,
		logMeta:     meta,
		flushDelay:  300 * time.Millisecond,
		startedAt:   time.Now(),
		useCardKit:  true, // will be set to false if CardKit APIs fail
		state: cardState{
			Title:       strings.TrimSpace(title),
			Status:      "thinking",
			StatusLabel: "正在思考",
			Model:       model,
			Updated:     time.Now(),
		},
	}
}

func (c *cardWriter) nextSequence() int {
	c.mu.Lock()
	c.sequence++
	seq := c.sequence
	c.mu.Unlock()
	return seq
}

func (c *cardWriter) SetStatus(status string, label string) {
	c.mu.Lock()
	c.state.Status = status
	c.state.StatusLabel = label
	c.state.Updated = time.Now()
	c.dirty = true
	c.mu.Unlock()
	c.schedule()
}

func (c *cardWriter) SetMute(mute bool) {
	c.mu.Lock()
	c.mute = mute
	c.mu.Unlock()
}

func (c *cardWriter) AppendStep(step cardStep) {
	c.mu.Lock()
	if c.mute && (step.Kind == "thought" || step.Kind == "tool") {
		c.mu.Unlock()
		return
	}
	c.state.Steps = append(c.state.Steps, step)
	c.state.Updated = time.Now()
	c.dirty = true
	c.mu.Unlock()
	c.schedule()
}

func (c *cardWriter) UpdateLastStep(mutator func(*cardStep)) {
	c.mu.Lock()
	if len(c.state.Steps) == 0 {
		c.mu.Unlock()
		return
	}
	step := c.state.Steps[len(c.state.Steps)-1]
	mutator(&step)
	c.state.Steps[len(c.state.Steps)-1] = step
	c.state.Updated = time.Now()
	c.dirty = true
	c.mu.Unlock()
	c.schedule()
}

func (c *cardWriter) RefreshStructure() {
	c.mu.Lock()
	if c.closed || c.cardBroken {
		c.mu.Unlock()
		return
	}
	entityID := c.cardEntityID
	if entityID == "" || !c.useCardKit {
		c.dirty = true
		c.mu.Unlock()
		c.schedule()
		return
	}
	if c.flushing {
		c.dirty = true
		c.mu.Unlock()
		return
	}
	c.flushing = true
	stateCopy := c.state
	stateCopy.Elapsed = time.Since(c.startedAt)
	c.dirty = false
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	c.setActiveFlushCancel(cancel)
	defer func() {
		c.clearActiveFlushCancel(cancel)
		cancel()
	}()
	cardJSON, err := renderStreamingCardV2(stateCopy)
	if err != nil {
		c.markBroken(err)
		return
	}
	if err := c.updateCardEntityWithRetry(ctx, entityID, cardJSON, "工具折叠卡片刷新"); err != nil {
		if ctx.Err() != nil {
			c.finishCanceledFlush()
			return
		}
		c.manager.logf("warn", "Feishu Agent 工具折叠卡片刷新失败："+err.Error(), c.logMeta)
		c.finishCanceledFlush()
		return
	}
	sig := cardStructureSignature(stateCopy)
	c.mu.Lock()
	c.structureSig = sig
	c.flushing = false
	stillDirty := c.dirty
	c.mu.Unlock()
	if stillDirty {
		c.schedule()
	}
}

func (c *cardWriter) SetReply(reply string) {
	c.mu.Lock()
	c.state.Reply = reply
	c.state.Updated = time.Now()
	c.dirty = true
	c.mu.Unlock()
	c.schedule()
}

func (c *cardWriter) SetHint(hint string) {
	c.mu.Lock()
	c.state.Hint = hint
	c.state.Updated = time.Now()
	c.dirty = true
	c.mu.Unlock()
	c.schedule()
}

func (c *cardWriter) IsBroken() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cardBroken
}

func (c *cardWriter) Status() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.Status
}

func (c *cardWriter) schedule() {
	c.mu.Lock()
	if c.closed || c.cardBroken {
		c.mu.Unlock()
		return
	}
	if c.timer != nil {
		c.mu.Unlock()
		return
	}
	c.timer = time.AfterFunc(c.flushDelay, func() {
		c.mu.Lock()
		c.timer = nil
		c.mu.Unlock()
		c.flush(false)
	})
	c.mu.Unlock()
}

func (c *cardWriter) flush(final bool) {
	c.mu.Lock()
	if c.closed && !final {
		c.mu.Unlock()
		return
	}
	if c.cardBroken {
		c.mu.Unlock()
		return
	}
	if !c.dirty && !final {
		c.mu.Unlock()
		return
	}
	if c.flushing {
		c.dirty = true
		c.mu.Unlock()
		return
	}
	c.flushing = true
	stateCopy := c.state
	stateCopy.Elapsed = time.Since(c.startedAt)
	entityIDSnap := c.cardEntityID
	msgIDSnap := c.cardMsgID
	useCardKitSnap := c.useCardKit
	c.dirty = false
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	c.setActiveFlushCancel(cancel)
	defer func() {
		c.clearActiveFlushCancel(cancel)
		cancel()
	}()

	// If no card entity yet, create one and send the initial card.
	if entityIDSnap == "" && useCardKitSnap {
		entityID, msgID, err := c.createAndSendStreamingCardWithRetry(ctx, stateCopy)
		if err != nil {
			if ctx.Err() != nil {
				c.finishCanceledFlush()
				return
			}
			c.mu.Lock()
			c.useCardKit = false
			c.mu.Unlock()
			c.manager.logf("warn", "Feishu Agent CardKit 创建重试失败，降级到 legacy card："+err.Error(), c.logMeta)
			c.flushLegacy(ctx, stateCopy, final, msgIDSnap)
			return
		} else {
			sig := cardStructureSignature(stateCopy)
			c.mu.Lock()
			c.cardEntityID = entityID
			c.cardMsgID = msgID
			c.structureSig = sig
			c.flushing = false
			stillDirty := c.dirty
			c.mu.Unlock()
			c.manager.logf("info", "Feishu Agent CardKit 流式卡片已创建 card_id="+entityID+" msg_id="+msgID, c.logMeta)
			if stillDirty {
				c.schedule()
			}
			return
		}
	}

	// Legacy fallback: PATCH the whole card message.
	if !useCardKitSnap {
		c.manager.logf("info", "Feishu Agent 使用 legacy PATCH 路径", c.logMeta)
		c.flushLegacy(ctx, stateCopy, final, msgIDSnap)
		return
	}

	// CardKit streaming: update elements individually.
	// Steps and hint change infrequently; reply changes on every token.
	// Sequence must be strictly increasing across all calls.
	currentSig := cardStructureSignature(stateCopy)
	c.mu.Lock()
	lastSig := c.structureSig
	c.mu.Unlock()
	if currentSig != lastSig {
		cardJSON, renderErr := renderStreamingCardV2(stateCopy)
		if renderErr != nil {
			c.markBroken(renderErr)
			return
		}
		if updateErr := c.updateCardEntityWithRetry(ctx, entityIDSnap, cardJSON, "工具折叠卡片刷新"); updateErr != nil {
			if ctx.Err() != nil {
				c.finishCanceledFlush()
				return
			}
			c.manager.logf("warn", "Feishu Agent 工具折叠卡片刷新失败："+updateErr.Error(), c.logMeta)
			c.finishCanceledFlush()
			return
		}
		c.mu.Lock()
		c.structureSig = currentSig
		c.flushing = false
		stillDirty := c.dirty
		c.mu.Unlock()
		if stillDirty {
			c.schedule()
		}
		return
	}
	seq := c.nextSequence()

	// Update steps if there are any
	stepsContent := renderStepsMarkdown(stateCopy.Steps)
	if stepsContent != "" {
		_ = c.manager.streamUpdateCardContent(ctx, entityIDSnap, cardElementSteps, stepsContent, seq)
		seq = c.nextSequence()
	}

	// Update reply (the hot path — always updated)
	replyContent := streamingReplyContent(stateCopy.Reply)
	if err := c.manager.streamUpdateCardContent(ctx, entityIDSnap, cardElementReply, replyContent, seq); err != nil {
		if ctx.Err() != nil {
			c.finishCanceledFlush()
			return
		}
		c.manager.logf("warn", "Feishu Agent streamUpdateCardContent 失败 seq="+fmt.Sprint(seq)+"，尝试全卡更新："+err.Error(), c.logMeta)
		// Streaming text update failed — try a full card update as fallback
		seq2 := c.nextSequence()
		cardJSON, renderErr := renderStreamingCardV2(stateCopy)
		if renderErr != nil {
			c.markBroken(err)
			return
		}
		if updateErr := c.manager.updateCardEntity(ctx, entityIDSnap, cardJSON, seq2); updateErr != nil {
			if ctx.Err() != nil {
				c.finishCanceledFlush()
				return
			}
			c.markBroken(updateErr)
			return
		}
	} else if !c.streamStarted {
		c.streamStarted = true
		c.manager.logf("info", "Feishu Agent CardKit 流式更新已启动 seq="+fmt.Sprint(seq), c.logMeta)
	}

	// Update hint if present
	if hint := strings.TrimSpace(stateCopy.Hint); hint != "" {
		seq3 := c.nextSequence()
		_ = c.manager.streamUpdateCardContent(ctx, entityIDSnap, cardElementHint, hint, seq3)
	}
	c.mu.Lock()
	c.flushing = false
	stillDirty := c.dirty
	c.mu.Unlock()
	if stillDirty {
		c.schedule()
	}
}

// flushLegacy handles the old PATCH /im/v1/messages path when CardKit is
// unavailable (e.g. missing cardkit:card:write scope).
func (c *cardWriter) flushLegacy(ctx context.Context, stateCopy cardState, final bool, msgIDSnap string) {
	cardJSON, err := renderCardV1(stateCopy)
	if err != nil {
		c.markBroken(err)
		return
	}
	if msgIDSnap == "" {
		newID, sendErr := c.sendLegacyCardWithRetry(ctx, cardJSON)
		if sendErr != nil {
			if ctx.Err() != nil {
				c.finishCanceledFlush()
				return
			}
			c.markBroken(sendErr)
			return
		}
		c.manager.logf("info", "Feishu Agent legacy 首次发送卡片 msg_id="+newID, c.logMeta)
		c.mu.Lock()
		c.cardMsgID = newID
		c.flushing = false
		stillDirty := c.dirty
		c.mu.Unlock()
		if stillDirty {
			c.schedule()
		}
		return
	}
	if patchErr := c.patchLegacyCardWithRetry(ctx, msgIDSnap, cardJSON); patchErr != nil {
		if ctx.Err() != nil {
			c.finishCanceledFlush()
			return
		}
		c.markBroken(patchErr)
		return
	}
	c.mu.Lock()
	c.flushing = false
	stillDirty := c.dirty
	c.mu.Unlock()
	if stillDirty {
		c.schedule()
	}
}

func (c *cardWriter) createAndSendStreamingCardWithRetry(ctx context.Context, state cardState) (string, string, error) {
	var lastErr error
	for attempt := 1; attempt <= cardOperationRetryCount; attempt++ {
		entityID, msgID, err := c.manager.createAndSendStreamingCard(ctx, c.rootMessage, state)
		if err == nil {
			return entityID, msgID, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return "", "", err
		}
		c.manager.logf("warn", fmt.Sprintf("Feishu Agent CardKit 创建失败，第 %d/%d 次：%v", attempt, cardOperationRetryCount, err), c.logMeta)
		if attempt < cardOperationRetryCount {
			sleepBeforeCardRetry(ctx, attempt)
		}
	}
	return "", "", lastErr
}

func (c *cardWriter) createAndSendFinalCardWithRetry(ctx context.Context, state cardState) (string, string, error) {
	cardJSON, err := renderFinalCardV2(state)
	if err != nil {
		return "", "", err
	}
	var lastErr error
	for attempt := 1; attempt <= cardOperationRetryCount; attempt++ {
		entityID, createErr := c.manager.createCardEntity(ctx, cardJSON)
		if createErr != nil {
			lastErr = fmt.Errorf("create final card entity: %w", createErr)
		} else {
			msgID, sendErr := c.manager.sendCardEntityMessage(ctx, c.rootMessage, entityID)
			if sendErr == nil {
				return entityID, msgID, nil
			}
			lastErr = fmt.Errorf("send final card entity message: %w", sendErr)
		}
		if ctx.Err() != nil {
			return "", "", lastErr
		}
		c.manager.logf("warn", fmt.Sprintf("Feishu Agent 续卡发送失败，第 %d/%d 次：%v", attempt, cardOperationRetryCount, lastErr), c.logMeta)
		if attempt < cardOperationRetryCount {
			sleepBeforeCardRetry(ctx, attempt)
		}
	}
	return "", "", lastErr
}

func (c *cardWriter) sendLegacyCardWithRetry(ctx context.Context, cardJSON string) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= cardOperationRetryCount; attempt++ {
		msgID, err := c.manager.sendCardReply(ctx, c.rootMessage, cardJSON)
		if err == nil {
			return msgID, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return "", err
		}
		c.manager.logf("warn", fmt.Sprintf("Feishu Agent legacy card 发送失败，第 %d/%d 次：%v", attempt, cardOperationRetryCount, err), c.logMeta)
		if attempt < cardOperationRetryCount {
			sleepBeforeCardRetry(ctx, attempt)
		}
	}
	return "", lastErr
}

func (c *cardWriter) patchLegacyCardWithRetry(ctx context.Context, msgID string, cardJSON string) error {
	return c.retryCardOperation(ctx, "legacy card 更新", func() error {
		return c.manager.patchCardMessage(ctx, msgID, cardJSON)
	})
}

func (c *cardWriter) updateCardEntityWithRetry(ctx context.Context, entityID string, cardJSON string, label string) error {
	return c.retryCardOperation(ctx, label, func() error {
		return c.manager.updateCardEntity(ctx, entityID, cardJSON, c.nextSequence())
	})
}

func (c *cardWriter) updateCardSettingsWithRetry(ctx context.Context, entityID string, summary string) error {
	return c.retryCardOperation(ctx, "CardKit 关闭流式", func() error {
		return c.manager.updateCardSettings(ctx, entityID, summary, c.nextSequence())
	})
}

func (c *cardWriter) streamUpdateCardContentWithRetry(ctx context.Context, entityID string, elementID string, content string, label string) error {
	return c.retryCardOperation(ctx, label, func() error {
		return c.manager.streamUpdateCardContent(ctx, entityID, elementID, content, c.nextSequence())
	})
}

func (c *cardWriter) retryCardOperation(ctx context.Context, label string, run func() error) error {
	var lastErr error
	for attempt := 1; attempt <= cardOperationRetryCount; attempt++ {
		err := run()
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return err
		}
		c.manager.logf("warn", fmt.Sprintf("Feishu Agent %s失败，第 %d/%d 次：%v", label, attempt, cardOperationRetryCount, err), c.logMeta)
		if attempt < cardOperationRetryCount {
			sleepBeforeCardRetry(ctx, attempt)
		}
	}
	return lastErr
}

func sleepBeforeCardRetry(ctx context.Context, attempt int) {
	delay := time.Duration(attempt) * 600 * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (c *cardWriter) markBroken(err error) {
	c.mu.Lock()
	c.cardBroken = true
	c.flushing = false
	c.mu.Unlock()
	if c.manager != nil {
		c.manager.logf("warn", "Feishu Agent 卡片更新失败："+err.Error(), c.logMeta)
	}
}

func (c *cardWriter) setActiveFlushCancel(cancel context.CancelFunc) {
	c.mu.Lock()
	c.flushCancel = cancel
	c.mu.Unlock()
}

func (c *cardWriter) clearActiveFlushCancel(cancel context.CancelFunc) {
	c.mu.Lock()
	c.flushCancel = nil
	c.mu.Unlock()
}

func (c *cardWriter) cancelActiveFlush() {
	c.mu.Lock()
	cancel := c.flushCancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *cardWriter) finishCanceledFlush() {
	c.mu.Lock()
	c.flushing = false
	c.dirty = false
	c.mu.Unlock()
}

func (c *cardWriter) waitForFlushIdle(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		c.mu.Lock()
		flushing := c.flushing
		c.mu.Unlock()
		if !flushing {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// Finalize writes the final card state. It prefers CardKit and falls back to
// legacy card updates; ordinary markdown is not emitted automatically.
func (c *cardWriter) Finalize(replyText string, hint string) {
	c.mu.Lock()
	c.state.Reply = replyText
	if hint != "" {
		c.state.Hint = hint
	}
	c.state.Elapsed = time.Since(c.startedAt)
	c.state.Updated = time.Now()
	c.dirty = true
	c.closed = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()

	c.cancelActiveFlush()
	if !c.waitForFlushIdle(3 * time.Second) {
		c.manager.logf("warn", "Feishu Agent Finalize: 等待流式卡片更新结束超时，继续执行最终兜底", c.logMeta)
	}

	c.mu.Lock()
	broken := c.cardBroken
	useCardKit := c.useCardKit
	entityID := c.cardEntityID
	stateCopy := c.state
	c.mu.Unlock()

	if broken || (entityID == "" && c.cardMsgID == "" && strings.TrimSpace(replyText) == "" && len(stateCopy.Steps) == 0) {
		if strings.TrimSpace(replyText) == "" && len(stateCopy.Steps) == 0 {
			return
		}
		c.manager.logf("info", "Feishu Agent Finalize: CardKit 不可用，使用 legacy card（broken="+fmt.Sprint(broken)+"）", c.logMeta)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		legacyMsgID := c.cardMsgID
		if useCardKit {
			legacyMsgID = ""
		}
		if err := c.finalizeLegacyCard(ctx, stateCopy, legacyMsgID); err != nil {
			c.manager.logf("warn", "Feishu Agent legacy card 最终兜底失败："+err.Error(), c.logMeta)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), finalCardOperationTimeout(stateCopy))
	defer cancel()

	if useCardKit && entityID == "" {
		c.manager.logf("info", "Feishu Agent Finalize: 流式卡片尚未创建，先创建 CardKit 卡片", c.logMeta)
		newEntityID, newMsgID, createErr := c.createAndSendStreamingCardWithRetry(ctx, stateCopy)
		if createErr != nil {
			c.manager.logf("warn", "Feishu Agent Finalize 创建 CardKit 重试失败，降级 legacy card："+createErr.Error(), c.logMeta)
			useCardKit = false
		} else {
			entityID = newEntityID
			c.mu.Lock()
			c.cardEntityID = newEntityID
			c.cardMsgID = newMsgID
			c.mu.Unlock()
		}
	}

	if useCardKit && entityID != "" {
		c.manager.logf("info", "Feishu Agent Finalize: CardKit 关闭流式 card_id="+entityID, c.logMeta)
		finalStates, err := splitFinalCardStates(stateCopy)
		if err != nil {
			if legacyErr := c.finalizeLegacyCard(ctx, stateCopy, ""); legacyErr != nil {
				c.manager.logf("warn", "Feishu Agent legacy card 最终兜底失败："+legacyErr.Error(), c.logMeta)
			}
			return
		}
		cardJSON, err := renderFinalCardV2(finalStates[0])
		if err != nil {
			if legacyErr := c.finalizeLegacyCard(ctx, stateCopy, ""); legacyErr != nil {
				c.manager.logf("warn", "Feishu Agent legacy card 最终兜底失败："+legacyErr.Error(), c.logMeta)
			}
			return
		}
		if len(finalStates) > 1 {
			c.manager.logf("info", fmt.Sprintf("Feishu Agent Finalize: 最终内容较长，拆分为 %d 张卡片完整发送", len(finalStates)), c.logMeta)
			if err := c.updateCardEntityWithRetry(ctx, entityID, cardJSON, "最终卡片分片更新"); err != nil {
				c.manager.logf("warn", "Feishu Agent 最终卡片分片更新失败，降级 legacy card："+err.Error(), c.logMeta)
				if legacyErr := c.finalizeLegacyCard(ctx, stateCopy, ""); legacyErr != nil {
					c.manager.logf("warn", "Feishu Agent legacy card 最终兜底失败："+legacyErr.Error(), c.logMeta)
				}
				return
			}
			for i := 1; i < len(finalStates); i++ {
				_, msgID, sendErr := c.createAndSendFinalCardWithRetry(ctx, finalStates[i])
				if sendErr != nil {
					c.manager.logf("warn", fmt.Sprintf("Feishu Agent 第 %d 张续卡发送失败，降级 legacy card：%v", i+1, sendErr), c.logMeta)
					if legacyErr := c.finalizeLegacyCard(ctx, finalStates[i], ""); legacyErr != nil {
						c.manager.logf("warn", "Feishu Agent legacy card 续卡兜底失败："+legacyErr.Error(), c.logMeta)
					}
					continue
				}
				c.manager.logf("info", fmt.Sprintf("Feishu Agent 续卡已发送: part=%d/%d msg_id=%s", i+1, len(finalStates), msgID), c.logMeta)
			}
			return
		}
		if err := c.closeStreamingCard(ctx, entityID, stateCopy); err == nil {
			if updateErr := c.updateFinalCardHeader(ctx, entityID, cardJSON, stateCopy); updateErr != nil {
				c.manager.logf("warn", "Feishu Agent 最终卡片状态更新重试失败，降级 legacy card："+updateErr.Error(), c.logMeta)
				if legacyErr := c.finalizeLegacyCard(ctx, stateCopy, ""); legacyErr != nil {
					c.manager.logf("warn", "Feishu Agent legacy card 最终兜底失败："+legacyErr.Error(), c.logMeta)
				}
			}
			return
		} else {
			c.manager.logf("warn", "Feishu Agent 关闭 CardKit 流式失败，尝试全卡更新："+err.Error(), c.logMeta)
		}
		if err := c.updateCardEntityWithRetry(ctx, entityID, cardJSON, "最终卡片更新"); err != nil {
			c.manager.logf("warn", "Feishu Agent 最终卡片更新重试失败，尝试极简卡片："+err.Error(), c.logMeta)
			if compactErr := c.updateCompactFinalCard(ctx, entityID, stateCopy); compactErr != nil {
				c.manager.logf("warn", "Feishu Agent 极简最终卡片更新重试失败，降级 legacy card："+compactErr.Error(), c.logMeta)
				if legacyErr := c.finalizeLegacyCard(ctx, stateCopy, ""); legacyErr != nil {
					c.manager.logf("warn", "Feishu Agent legacy card 最终兜底失败："+legacyErr.Error(), c.logMeta)
				}
			}
		}
		return
	}

	// Legacy path — must use schema 1.0 format for PATCH /im/v1/messages
	c.manager.logf("info", "Feishu Agent Finalize: legacy PATCH 路径 msg_id="+c.cardMsgID, c.logMeta)
	if err := c.finalizeLegacyCard(ctx, stateCopy, c.cardMsgID); err != nil {
		c.manager.logf("warn", "Feishu Agent legacy card 最终更新失败："+err.Error(), c.logMeta)
	}
}

func (c *cardWriter) closeStreamingCard(ctx context.Context, entityID string, state cardState) error {
	if stepsContent := renderStepsMarkdown(state.Steps); stepsContent != "" {
		if err := c.streamUpdateCardContentWithRetry(ctx, entityID, cardElementSteps, stepsContent, "更新最终步骤"); err != nil {
			return fmt.Errorf("update final steps: %w", err)
		}
	}
	replyContent := streamingReplyContent(state.Reply)
	if err := c.streamUpdateCardContentWithRetry(ctx, entityID, cardElementReply, replyContent, "更新最终回复"); err != nil {
		return fmt.Errorf("update final reply: %w", err)
	}
	if hint := strings.TrimSpace(state.Hint); hint != "" {
		if err := c.streamUpdateCardContentWithRetry(ctx, entityID, cardElementHint, hint, "更新最终提示"); err != nil {
			return fmt.Errorf("update final hint: %w", err)
		}
	}
	summary := strings.TrimSpace(state.StatusLabel)
	if summary == "" {
		summary = "完成"
	}
	if err := c.updateCardSettingsWithRetry(ctx, entityID, summary); err != nil {
		return fmt.Errorf("close streaming settings: %w", err)
	}
	return nil
}

func (c *cardWriter) finalizeLegacyCard(ctx context.Context, state cardState, msgID string) error {
	cardJSON, err := renderCardV1(state)
	if err != nil {
		return err
	}
	if strings.TrimSpace(msgID) == "" {
		newID, sendErr := c.sendLegacyCardWithRetry(ctx, cardJSON)
		if sendErr != nil {
			return sendErr
		}
		c.mu.Lock()
		c.cardMsgID = newID
		c.useCardKit = false
		c.mu.Unlock()
		c.manager.logf("info", "Feishu Agent legacy card 最终兜底已发送 msg_id="+newID, c.logMeta)
		return nil
	}
	if err := c.patchLegacyCardWithRetry(ctx, msgID, cardJSON); err != nil {
		return err
	}
	c.manager.logf("info", "Feishu Agent legacy card 最终更新完成 msg_id="+msgID, c.logMeta)
	return nil
}

func (c *cardWriter) updateFinalCardHeader(ctx context.Context, entityID string, cardJSON string, state cardState) error {
	if err := c.updateCardEntityWithRetry(ctx, entityID, cardJSON, "最终卡片状态更新"); err == nil {
		return nil
	}
	compactState := compactFinalCardState(state)
	compactJSON, compactErr := renderFinalCardV2(compactState)
	if compactErr != nil {
		return compactErr
	}
	if compactUpdateErr := c.updateCardEntityWithRetry(ctx, entityID, compactJSON, "极简最终卡片状态更新"); compactUpdateErr != nil {
		return compactUpdateErr
	}
	return nil
}

func (c *cardWriter) updateCompactFinalCard(ctx context.Context, entityID string, state cardState) error {
	compactState := compactFinalCardState(state)
	compactJSON, compactErr := renderFinalCardV2(compactState)
	if compactErr != nil {
		return compactErr
	}
	return c.updateCardEntityWithRetry(ctx, entityID, compactJSON, "极简最终卡片更新")
}

func shouldUseCompactFinalDelivery(cardJSON string, state cardState) bool {
	if len([]byte(cardJSON)) > finalCardJSONSoftLimit {
		return true
	}
	return false
}

func finalCardOperationTimeout(state cardState) time.Duration {
	replyRunes := len([]rune(strings.TrimSpace(state.Reply)))
	if replyRunes == 0 {
		return 30 * time.Second
	}
	// Continuation cards are sent sequentially. Slow Windows/company networks
	// can exceed a fixed 30s timeout, so reserve extra time for long answers.
	estimatedParts := replyRunes/2500 + 1
	timeout := 30*time.Second + time.Duration(estimatedParts)*12*time.Second
	if timeout > 3*time.Minute {
		return 3 * time.Minute
	}
	return timeout
}

func splitFinalCardStates(state cardState) ([]cardState, error) {
	reply := strings.TrimSpace(state.Reply)
	if reply == "" {
		return []cardState{state}, nil
	}
	cardJSON, err := renderFinalCardV2(state)
	if err != nil {
		return nil, err
	}
	if !shouldUseCompactFinalDelivery(cardJSON, state) {
		return []cardState{state}, nil
	}

	runes := []rune(reply)
	parts := []cardState{}
	offset := 0
	for offset < len(runes) {
		base := state
		base.Reply = ""
		base.Hint = ""
		if len(parts) > 0 {
			base.Steps = nil
			base.Title = continuationTitle(state.Title, len(parts)+1)
			base.Status = "done"
			base.StatusLabel = fmt.Sprintf("已完成（续 %d）", len(parts)+1)
		}
		best := largestReplyChunk(base, runes[offset:])
		if best <= 0 {
			// Tool panels or fixed metadata alone exceeded the budget. Keep the
			// answer complete by compacting only tool panels, never reply text.
			base.Steps = compactFinalSteps(base.Steps)
			best = largestReplyChunk(base, runes[offset:])
			if best <= 0 {
				return nil, fmt.Errorf("final card fixed content exceeds budget")
			}
		}
		best = preferTextBoundary(runes[offset:], best)
		part := base
		part.Reply = strings.TrimSpace(string(runes[offset : offset+best]))
		if part.Reply == "" {
			part.Reply = string(runes[offset : offset+best])
		}
		parts = append(parts, part)
		offset += best
		for offset < len(runes) && (runes[offset] == '\n' || runes[offset] == ' ') {
			offset++
		}
	}
	return parts, nil
}

func largestReplyChunk(base cardState, runes []rune) int {
	low, high := 1, len(runes)
	best := 0
	for low <= high {
		mid := (low + high) / 2
		candidate := base
		candidate.Reply = strings.TrimSpace(string(runes[:mid]))
		cardJSON, err := renderFinalCardV2(candidate)
		if err == nil && !shouldUseCompactFinalDelivery(cardJSON, candidate) {
			best = mid
			low = mid + 1
			continue
		}
		high = mid - 1
	}
	return best
}

func preferTextBoundary(runes []rune, limit int) int {
	if limit >= len(runes) {
		return len(runes)
	}
	min := limit * 2 / 3
	for i := limit; i > min; i-- {
		switch runes[i-1] {
		case '\n', '。', '！', '？', ';', '；':
			return i
		}
	}
	return limit
}

func continuationTitle(title string, part int) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Sprintf("续 %d", part)
	}
	return fmt.Sprintf("%s 续 %d", title, part)
}

func streamingReplyContent(reply string) string {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return " "
	}
	runes := []rune(reply)
	if len(runes) <= streamingReplyPreviewLimit {
		return reply
	}
	return strings.TrimSpace(string(runes[:streamingReplyPreviewLimit])) + "\n\n（回复较长，完成后会更新为完整内容。）"
}

func compactFinalCardState(state cardState) cardState {
	state.Steps = compactFinalSteps(state.Steps)
	state.Hint = strings.TrimSpace(firstNonEmptyString(state.Hint, "卡片更新遇到平台限制，Agent 已保留可发送的最长内容；不会补写或编造未发送内容。"))
	reply := strings.TrimSpace(state.Reply)
	if reply != "" {
		state.Reply = summarizeText(reply, compactFinalReplyLimit)
	}
	return state
}

func compactFinalSteps(steps []cardStep) []cardStep {
	if len(steps) == 0 {
		return nil
	}
	start := 0
	if len(steps) > compactFinalStepLimit {
		start = len(steps) - compactFinalStepLimit
	}
	out := make([]cardStep, 0, len(steps)-start)
	for _, step := range steps[start:] {
		step.Body = summarizeText(step.Body, compactFinalStepBodyLimit)
		out = append(out, step)
	}
	return out
}
