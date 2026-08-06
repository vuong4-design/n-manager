package proxy

import (
	"strings"
	"testing"
)

func TestNeedsFreshThreadRecoveryDetectsPriorTurns(t *testing.T) {
	messages := []ChatMessage{
		{Role: "user", Content: "What is Opus 4.6?"},
		{Role: "assistant", Content: "It is Anthropic's flagship model."},
		{Role: "user", Content: "What about Sonnet?"},
	}

	if !needsFreshThreadRecovery(messages) {
		t.Fatal("expected prior-turn history to require fresh-thread recovery")
	}
}

func TestNeedsFreshThreadRecoverySkipsSingleTurn(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "Be concise."},
		{Role: "user", Content: "What is Opus 4.6?"},
	}

	if needsFreshThreadRecovery(messages) {
		t.Fatal("expected single-turn request to avoid recovery collapse")
	}
}

func TestNeedsFreshThreadRecoveryIgnoresWrapperOnlyUserMessage(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "You are Claude Code."},
		{Role: "user", Content: "<available-deferred-tools>\nRead\nEdit\n</available-deferred-tools>"},
		{Role: "user", Content: "修复登录校验"},
	}

	if needsFreshThreadRecovery(messages) {
		t.Fatal("expected wrapper-only user message to be ignored for recovery collapse")
	}
}

func TestNeedsFreshThreadRecoveryKeepsConsecutiveUserContextInline(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "You are Codex."},
		{Role: "user", Content: "Repository context that must stay verbatim."},
		{Role: "user", Content: "Fix the current failure."},
	}

	if needsFreshThreadRecovery(messages) {
		t.Fatal("consecutive user context without assistant/tool history must not be collapsed")
	}
	got := buildFreshThreadRecoveryMessages(messages)
	if len(got) != len(messages) || got[1].Content != messages[1].Content || got[2].Content != messages[2].Content {
		t.Fatalf("first-turn context was not preserved inline: %#v", got)
	}
}

func TestFreshThreadRecoveryIncludesToolChainEndingInToolResult(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "Use repository evidence."},
		{Role: "user", Content: "Read README.md and summarize it."},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: ToolCallFunction{
				Name:      "Read",
				Arguments: `{"path":"README.md"}`,
			},
		}}},
		{Role: "tool", Name: "Read", ToolCallID: "call_1", Content: "README file contents"},
	}

	if !needsFreshThreadRecovery(messages) {
		t.Fatal("tool history ending in a tool result must require fresh-thread recovery")
	}
	got := buildFreshThreadRecoveryMessages(messages)
	if len(got) != 1 {
		t.Fatalf("expected 1 collapsed message, got %d", len(got))
	}
	for _, want := range []string{
		"Use repository evidence.",
		"Latest user message:\nRead README.md and summarize it.",
		`Tool call Read: {"path":"README.md"}`,
		"Tool (Read): README file contents",
	} {
		if !strings.Contains(got[0].Content, want) {
			t.Fatalf("expected collapsed tool chain to contain %q, got %q", want, got[0].Content)
		}
	}
	body := got[0].Content
	userIndex := strings.Index(body, "User: Read README.md and summarize it.")
	callIndex := strings.Index(body, `Tool call Read: {"path":"README.md"}`)
	resultIndex := strings.Index(body, "Tool (Read): README file contents")
	latestIndex := strings.Index(body, "Latest user message:\nRead README.md and summarize it.")
	if !(userIndex >= 0 && userIndex < callIndex && callIndex < resultIndex && resultIndex < latestIndex) {
		t.Fatalf("collapsed tool chain order is wrong: %q", body)
	}
}

func TestCountNonSystemMessagesIgnoresWrapperOnlyUserMessage(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "You are Claude Code."},
		{Role: "user", Content: "<available-deferred-tools>\nRead\nEdit\n</available-deferred-tools>"},
		{Role: "user", Content: "修复登录校验"},
	}

	if got := countNonSystemMessages(messages); got != 1 {
		t.Fatalf("expected wrapper-only user message to be excluded from raw count, got %d", got)
	}
}

func TestBuildFreshThreadRecoveryMessagesCollapsesHistory(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "Answer in Chinese."},
		{Role: "user", Content: "opus4.6什么时候推出的"},
		{Role: "assistant", Content: "Claude Opus 4.6 在 2026 年 2 月推出。"},
		{Role: "user", Content: "sonnet有什么优势"},
	}

	got := buildFreshThreadRecoveryMessages(messages)
	if len(got) != 1 {
		t.Fatalf("expected 1 collapsed message, got %d", len(got))
	}
	if got[0].Role != "user" {
		t.Fatalf("expected collapsed role=user, got %q", got[0].Role)
	}

	body := got[0].Content
	for _, want := range []string{
		"System instructions:",
		"Answer in Chinese.",
		"Conversation context:",
		"User: opus4.6什么时候推出的",
		"Assistant: Claude Opus 4.6 在 2026 年 2 月推出。",
		"Latest user message:\nsonnet有什么优势",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected collapsed prompt to contain %q, got %q", want, body)
		}
	}
}

func TestBuildFreshThreadRecoveryMessagesPreservesCompleteSystemInstructions(t *testing.T) {
	const tailMarker = "SYSTEM-INSTRUCTIONS-TAIL-MARKER"
	messages := []ChatMessage{
		{Role: "system", Content: strings.Repeat("x", 1600) + tailMarker},
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "follow-up"},
	}

	got := buildFreshThreadRecoveryMessages(messages)
	if len(got) != 1 {
		t.Fatalf("expected 1 collapsed message, got %d", len(got))
	}
	if !strings.Contains(got[0].Content, tailMarker) {
		t.Fatalf("system instruction tail was truncated: %q", got[0].Content)
	}
}

func TestBuildToolBridgeRecoveryMessagesSkipsIdentityDriftAssistantText(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "Answer in Chinese."},
		{Role: "user", Content: "修改 internal/web/dist/assets/index-DlVudHMF.js"},
		{Role: "assistant", Content: "我是 Notion AI，无法访问你的本地文件系统。把下面这段话直接发给你的编码助手（Cursor / Claude Code）。"},
		{Role: "tool", Name: "Grep", Content: "Found 1 file\ninternal/web/dist/assets/index-DlVudHMF.js"},
		{Role: "user", Content: "你来动手"},
	}

	got := buildToolBridgeRecoveryMessages(messages)
	if len(got) != 1 {
		t.Fatalf("expected 1 collapsed message, got %d", len(got))
	}

	body := got[0].Content
	if strings.Contains(body, "我是 Notion AI") || strings.Contains(body, "编码助手") {
		t.Fatalf("tool recovery should drop identity-drift assistant text, got %q", body)
	}
	for _, want := range []string{
		"System instructions:",
		"Answer in Chinese.",
		"Conversation context:",
		"User: 修改 internal/web/dist/assets/index-DlVudHMF.js",
		"Tool (Grep): Found 1 file\ninternal/web/dist/assets/index-DlVudHMF.js",
		"Latest user message:\n你来动手",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected tool recovery prompt to contain %q, got %q", want, body)
		}
	}
}

func TestPrepareFreshThreadAttemptMessagesKeepsToolContractHistoryAliased(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "You are Claude Code."},
		{Role: "user", Content: "inspect the file"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Function: ToolCallFunction{Name: "Read", Arguments: `{"path":"a.go"}`}}}},
		{Role: "tool", ToolCallID: "call-1", Name: "Read", Content: "package main"},
		{Role: "user", Content: "continue"},
	}
	got, recovered := prepareFreshThreadAttemptMessages(messages, nil, true, map[string]string{"Read": "action_1"}, true)
	if !recovered || len(got) != 1 {
		t.Fatalf("recovered=%v messages=%d", recovered, len(got))
	}
	for _, want := range []string{"System instructions:", "Tool (action_1): package main", "Latest user message:\ncontinue"} {
		if !strings.Contains(got[0].Content, want) {
			t.Fatalf("fresh recovery lost %q: %s", want, got[0].Content)
		}
	}
}

func TestPrepareFreshThreadAttemptMessagesUsesSanitizedToolRecovery(t *testing.T) {
	original := []ChatMessage{{Role: "user", Content: "original"}}
	recovery := []ChatMessage{
		{Role: "user", Content: "sanitized"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Function: ToolCallFunction{Name: "Read", Arguments: `{}`}}}},
		{Role: "tool", ToolCallID: "call-1", Name: "Read", Content: "ok"},
	}
	got, recovered := prepareFreshThreadAttemptMessages(original, recovery, true, map[string]string{"Read": "action_1"}, true)
	if !recovered || len(got) != len(recovery) {
		t.Fatalf("recovered=%v messages=%d", recovered, len(got))
	}
	if got[0].Content != "sanitized" || got[1].ToolCalls[0].Function.Name != "action_1" || got[2].Name != "action_1" {
		t.Fatalf("sanitized recovery or aliases changed: %#v", got)
	}
}

func TestPrepareFreshThreadAttemptMessagesLeavesContinuationUncollapsed(t *testing.T) {
	messages := []ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "answer"},
		{Role: "user", Content: "next"},
	}
	got, recovered := prepareFreshThreadAttemptMessages(messages, nil, false, nil, false)
	if recovered || len(got) != len(messages) {
		t.Fatalf("continuation recovered=%v messages=%d", recovered, len(got))
	}
}

func TestFreshThreadRecoveryRetainsMoreThanLegacyFourKilobytes(t *testing.T) {
	const marker = "RECENT-HISTORY-BEYOND-FOUR-KILOBYTES"
	messages := []ChatMessage{{Role: "system", Content: "system"}, {Role: "user", Content: "first"}}
	for i := 0; i < 5; i++ {
		content := strings.Repeat(string(rune('a'+i)), 1000)
		if i == 0 {
			content += marker
		}
		messages = append(messages, ChatMessage{Role: "assistant", Content: content})
	}
	messages = append(messages, ChatMessage{Role: "user", Content: "latest"})
	got := buildFreshThreadRecoveryMessages(messages)
	if len(got) != 1 || !strings.Contains(got[0].Content, marker) {
		t.Fatalf("expanded recovery history did not retain marker: %#v", got)
	}
}

func TestBuildAliasedToolBridgeRecoveryMessagesAliasesBeforeCollapse(t *testing.T) {
	messages := []ChatMessage{
		{Role: "user", Content: "inspect"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Function: ToolCallFunction{Name: "Read", Arguments: `{}`}}}},
		{Role: "tool", ToolCallID: "call-1", Name: "Read", Content: "result"},
		{Role: "user", Content: "continue"},
	}
	got := buildAliasedToolBridgeRecoveryMessages(messages, true, map[string]string{"Read": "action_1"})
	if len(got) != 1 || !strings.Contains(got[0].Content, "Tool (action_1): result") {
		t.Fatalf("tool alias was not applied before recovery collapse: %#v", got)
	}
	if strings.Contains(got[0].Content, "Tool (Read):") {
		t.Fatalf("original tool name leaked into collapsed recovery: %s", got[0].Content)
	}
}
