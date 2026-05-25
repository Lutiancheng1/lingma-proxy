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
			"elements": buildFinalElements(state),
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
	replyContent := strings.TrimSpace(state.Reply)
	if replyContent == "" {
		replyContent = " "
	}
	elements = append(elements, map[string]any{
		"tag":        "markdown",
		"content":    replyContent,
		"element_id": cardElementReply,
	})
	elements = append(elements, map[string]any{
		"tag":        "markdown",
		"content":    strings.TrimSpace(state.Hint),
		"element_id": cardElementHint,
	})
	return elements
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
	if step.Kind == "tool" && strings.TrimSpace(step.Body) != "" {
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
					"content": strings.TrimSpace(step.Body),
				},
			},
			"padding":          "4px 0px 4px 0px",
			"vertical_spacing": "small",
		}
	}
	content := fmt.Sprintf("%s **%s**", icon, title)
	if body := strings.TrimSpace(step.Body); body != "" {
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
	for i, step := range steps {
		if i > 0 {
			b.WriteString("\n")
		}
		icon := stepIcon(step)
		title := strings.TrimSpace(step.Title)
		if title == "" {
			title = step.Kind
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
	closed        bool
	startedAt     time.Time
	timer         *time.Timer
	cardBroken    bool
	useCardKit    bool // true = CardKit streaming; false = legacy PATCH fallback
	streamStarted bool // logs the first successful streamUpdateCardContent
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
	defer cancel()

	// If no card entity yet, create one and send the initial card.
	if entityIDSnap == "" && useCardKitSnap {
		entityID, msgID, err := c.manager.createAndSendStreamingCard(ctx, c.rootMessage, stateCopy)
		if err != nil {
			c.mu.Lock()
			c.useCardKit = false
			c.mu.Unlock()
			c.manager.logf("warn", "Feishu bridge CardKit 创建失败，降级到 legacy PATCH："+err.Error(), c.logMeta)
			c.flushLegacy(ctx, stateCopy, final, msgIDSnap)
			return
		} else {
			c.mu.Lock()
			c.cardEntityID = entityID
			c.cardMsgID = msgID
			c.flushing = false
			stillDirty := c.dirty
			c.mu.Unlock()
			c.manager.logf("info", "Feishu bridge CardKit 流式卡片已创建 card_id="+entityID+" msg_id="+msgID, c.logMeta)
			if stillDirty {
				c.schedule()
			}
			return
		}
	}

	// Legacy fallback: PATCH the whole card message.
	if !useCardKitSnap {
		c.manager.logf("info", "Feishu bridge 使用 legacy PATCH 路径", c.logMeta)
		c.flushLegacy(ctx, stateCopy, final, msgIDSnap)
		return
	}

	// CardKit streaming: update elements individually.
	// Steps and hint change infrequently; reply changes on every token.
	// Sequence must be strictly increasing across all calls.
	seq := c.nextSequence()

	// Update steps if there are any
	stepsContent := renderStepsMarkdown(stateCopy.Steps)
	if stepsContent != "" {
		_ = c.manager.streamUpdateCardContent(ctx, entityIDSnap, cardElementSteps, stepsContent, seq)
		seq = c.nextSequence()
	}

	// Update reply (the hot path — always updated)
	replyContent := strings.TrimSpace(stateCopy.Reply)
	if replyContent == "" {
		replyContent = " "
	}
	if err := c.manager.streamUpdateCardContent(ctx, entityIDSnap, cardElementReply, replyContent, seq); err != nil {
		c.manager.logf("warn", "Feishu bridge streamUpdateCardContent 失败 seq="+fmt.Sprint(seq)+"，尝试全卡更新："+err.Error(), c.logMeta)
		// Streaming text update failed — try a full card update as fallback
		seq2 := c.nextSequence()
		cardJSON, renderErr := renderStreamingCardV2(stateCopy)
		if renderErr != nil {
			c.markBroken(err)
			return
		}
		if updateErr := c.manager.updateCardEntity(ctx, entityIDSnap, cardJSON, seq2); updateErr != nil {
			c.markBroken(updateErr)
			return
		}
	} else if !c.streamStarted {
		c.streamStarted = true
		c.manager.logf("info", "Feishu bridge CardKit 流式更新已启动 seq="+fmt.Sprint(seq), c.logMeta)
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
		newID, sendErr := c.manager.sendCardReply(ctx, c.rootMessage, cardJSON)
		if sendErr != nil {
			c.markBroken(sendErr)
			return
		}
		c.manager.logf("info", "Feishu bridge legacy 首次发送卡片 msg_id="+newID, c.logMeta)
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
	if patchErr := c.manager.patchCardMessage(ctx, msgIDSnap, cardJSON); patchErr != nil {
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

func (c *cardWriter) markBroken(err error) {
	c.mu.Lock()
	c.cardBroken = true
	c.flushing = false
	c.mu.Unlock()
	if c.manager != nil {
		c.manager.logf("warn", "Feishu bridge 卡片更新失败，将退回 markdown："+err.Error(), c.logMeta)
	}
}

// Finalize writes the final card state. If CardKit was used, it disables
// streaming_mode and does a final full update. Falls back to markdown if
// the card is broken.
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
	broken := c.cardBroken
	useCardKit := c.useCardKit
	entityID := c.cardEntityID
	rootMsg := c.rootMessage
	stateCopy := c.state
	c.mu.Unlock()

	if broken || (entityID == "" && c.cardMsgID == "" && strings.TrimSpace(replyText) == "" && len(stateCopy.Steps) == 0) {
		c.manager.logf("info", "Feishu bridge Finalize: 降级到 markdown（broken="+fmt.Sprint(broken)+"）", c.logMeta)
		c.fallbackMarkdown(rootMsg, replyText)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if useCardKit && entityID != "" {
		c.manager.logf("info", "Feishu bridge Finalize: CardKit 全卡更新 card_id="+entityID, c.logMeta)
		// Final full update with streaming_mode=false
		cardJSON, err := renderFinalCardV2(stateCopy)
		if err != nil {
			c.fallbackMarkdown(rootMsg, replyText)
			return
		}
		seq := c.nextSequence()
		if err := c.manager.updateCardEntity(ctx, entityID, cardJSON, seq); err != nil {
			c.manager.logf("warn", "Feishu bridge 最终卡片更新失败："+err.Error(), c.logMeta)
			// Don't fallback to markdown — the streaming card already has content visible to the user. Sending markdown would create a duplicate message.
		}
		return
	}

	// Legacy path — must use schema 1.0 format for PATCH /im/v1/messages
	c.manager.logf("info", "Feishu bridge Finalize: legacy PATCH 路径 msg_id="+c.cardMsgID, c.logMeta)
	cardJSON, err := renderCardV1(stateCopy)
	if err != nil {
		c.fallbackMarkdown(rootMsg, replyText)
		return
	}
	msgID := c.cardMsgID
	if msgID == "" {
		if _, sendErr := c.manager.sendCardReply(ctx, rootMsg, cardJSON); sendErr != nil {
			c.fallbackMarkdown(rootMsg, replyText)
		}
		return
	}
	if patchErr := c.manager.patchCardMessage(ctx, msgID, cardJSON); patchErr != nil {
		c.fallbackMarkdown(rootMsg, replyText)
	}
}

func (c *cardWriter) fallbackMarkdown(rootMsg string, reply string) {
	if strings.TrimSpace(reply) == "" {
		return
	}
	if err := c.manager.replyToMessage(context.Background(), rootMsg, reply); err != nil && c.manager != nil {
		c.manager.logf("warn", "Feishu bridge markdown 兜底回复失败："+err.Error(), c.logMeta)
	}
}
