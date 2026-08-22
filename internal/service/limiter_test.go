package service

import "testing"

func TestOutputLimiterStopSingleDelta(t *testing.T) {
	l := newOutputLimiter(0, []string{"STOP"})
	got := l.Push("hello STOP world")
	if got != "hello " {
		t.Fatalf("emit = %q, want %q", got, "hello ")
	}
	if !l.triggered() || l.finish != "stop" || l.stopSeq != "STOP" {
		t.Fatalf("finish=%q stopSeq=%q", l.finish, l.stopSeq)
	}
}

func TestOutputLimiterStopAcrossDeltas(t *testing.T) {
	l := newOutputLimiter(0, []string{"STOP"})
	var out string
	out += l.Push("hello ") // holdback 3 -> emits "hel", pending "lo "
	out += l.Push("STO")    // work "lo STO" no full stop -> emit "lo ", pending "STO"
	out += l.Push("P rest") // work "STOP rest" -> stop at 0 -> emit ""
	out += l.Flush()
	if out != "hello " {
		t.Fatalf("assembled = %q, want %q", out, "hello ")
	}
	if !l.triggered() || l.finish != "stop" {
		t.Fatalf("not stopped: finish=%q", l.finish)
	}
}

func TestOutputLimiterMaxTokens(t *testing.T) {
	// maxTokens=2 -> limitRunes=6
	l := newOutputLimiter(2, nil)
	got := l.Push("abcdefghij")
	if got != "abcdef" {
		t.Fatalf("emit = %q, want %q", got, "abcdef")
	}
	if !l.triggered() || l.openAIFinish() != "length" {
		t.Fatalf("finish=%q", l.finish)
	}
	if l.Push("more") != "" {
		t.Fatal("expected no more output after limit")
	}
}

func TestOutputLimiterDisabledPassThrough(t *testing.T) {
	l := newOutputLimiter(0, nil)
	if l.enabled() {
		t.Fatal("should be disabled")
	}
	if got := l.apply("anything at all"); got != "anything at all" {
		t.Fatalf("apply changed text: %q", got)
	}
}

func TestOutputLimiterApplyNonStream(t *testing.T) {
	l := newOutputLimiter(0, []string{"\n\n"})
	got := l.apply("line one\n\nline two")
	if got != "line one" {
		t.Fatalf("apply = %q, want %q", got, "line one")
	}
}
