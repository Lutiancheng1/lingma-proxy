package service

import "strings"

// outputLimiter enforces max_tokens and stop sequences on the *visible* output
// text, because the remote gateway accepts but ignores these constraints. It is
// stream-aware: Push feeds incremental text and returns the portion safe to
// emit (holding back a short tail so a stop sequence split across deltas is
// still caught); Flush emits the remaining tail at end of stream.
//
// max_tokens uses the same heuristic token estimate as the rest of the service,
// so it is an approximate cap (there is no real tokenizer for the upstream).
type outputLimiter struct {
	limitRunes int // 0 = no max_tokens limit
	stops      []string
	holdback   int // runes to hold back for cross-delta stop detection

	emitted      strings.Builder
	emittedRunes int
	pending      string
	done         bool
	finish       string // "", "length", or "stop"
	stopSeq      string
}

func newOutputLimiter(maxTokens int, stops []string) *outputLimiter {
	l := &outputLimiter{}
	if maxTokens > 0 {
		l.limitRunes = maxTokens * 3 // inverse of estimateTokens ((runes+2)/3)
	}
	maxStop := 0
	for _, s := range stops {
		if s == "" {
			continue
		}
		l.stops = append(l.stops, s)
		if n := len([]rune(s)); n > maxStop {
			maxStop = n
		}
	}
	if maxStop > 1 {
		l.holdback = maxStop - 1
	}
	return l
}

func (l *outputLimiter) enabled() bool {
	return l != nil && (l.limitRunes > 0 || len(l.stops) > 0)
}

// earliestStop returns the byte index and matched sequence of the earliest stop
// occurrence in s, or (-1, "").
func (l *outputLimiter) earliestStop(s string) (int, string) {
	best := -1
	var seq string
	for _, stop := range l.stops {
		if idx := strings.Index(s, stop); idx >= 0 && (best < 0 || idx < best) {
			best = idx
			seq = stop
		}
	}
	return best, seq
}

// capByTokens truncates part so total emitted runes never exceed the limit.
// Returns the (possibly shortened) part and whether the token limit was hit.
func (l *outputLimiter) capByTokens(part string) (string, bool) {
	if l.limitRunes <= 0 {
		return part, false
	}
	remaining := l.limitRunes - l.emittedRunes
	if remaining <= 0 {
		return "", true
	}
	runes := []rune(part)
	if len(runes) <= remaining {
		return part, false
	}
	return string(runes[:remaining]), true
}

// Push feeds a text delta and returns the text safe to emit now.
func (l *outputLimiter) Push(text string) string {
	if l.done || text == "" {
		return ""
	}
	work := l.pending + text
	var candidate string
	willStop := false
	if idx, seq := l.earliestStop(work); idx >= 0 {
		candidate = work[:idx]
		willStop = true
		l.stopSeq = seq
		l.pending = ""
	} else if l.holdback > 0 {
		runes := []rune(work)
		if len(runes) <= l.holdback {
			l.pending = work
			candidate = ""
		} else {
			candidate = string(runes[:len(runes)-l.holdback])
			l.pending = string(runes[len(runes)-l.holdback:])
		}
	} else {
		candidate = work
		l.pending = ""
	}

	capped, tokenHit := l.capByTokens(candidate)
	l.record(capped)
	if tokenHit {
		l.done = true
		l.finish = "length"
		l.pending = ""
	} else if willStop {
		l.done = true
		l.finish = "stop"
		l.pending = ""
	}
	return capped
}

// Flush emits any held-back tail once the upstream stream has ended.
func (l *outputLimiter) Flush() string {
	if l.done || l.pending == "" {
		return ""
	}
	capped, tokenHit := l.capByTokens(l.pending)
	l.pending = ""
	l.record(capped)
	if tokenHit {
		l.done = true
		l.finish = "length"
	}
	return capped
}

func (l *outputLimiter) record(s string) {
	if s == "" {
		return
	}
	l.emitted.WriteString(s)
	l.emittedRunes += len([]rune(s))
}

func (l *outputLimiter) triggered() bool { return l.done }
func (l *outputLimiter) text() string    { return l.emitted.String() }

// apply enforces the limits on a whole (non-streamed) text and records the
// finish reason. Returns the truncated text.
func (l *outputLimiter) apply(full string) string {
	if !l.enabled() {
		return full
	}
	out := l.Push(full)
	out += l.Flush()
	return out
}

// openAIFinish translates the limiter state into an OpenAI finish_reason.
func (l *outputLimiter) openAIFinish() string {
	switch l.finish {
	case "length":
		return "length"
	case "stop":
		return "stop"
	default:
		return ""
	}
}
