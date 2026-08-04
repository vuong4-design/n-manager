package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func prepareUpstreamBridgeTestResponse(content string, nativeToolUses []AgentValueEntry) preparedToolBridgeResponse {
	allowed := map[string]struct{}{
		"bash": {}, "edit": {}, "read": {}, "write": {}, "question": {}, "WebSearch": {},
	}
	return prepareToolBridgeResponse(content, nativeToolUses, allowed, nil)
}

func TestPrepareToolBridgeResponse_NoneSentinelIntercepted(t *testing.T) {
	// Model emits {"name":"none","arguments":{}} as a "no tool" sentinel.
	// The proxy must intercept it so it never reaches the OpenAI client
	// as a real tool_call (which would error "unavailable tool 'none'").
	content := `{"name": "none", "arguments": {}}`
	prepared := prepareUpstreamBridgeTestResponse(content, nil)

	if prepared.HasCalls {
		t.Errorf("none sentinel should not produce tool calls, got %d", len(prepared.ToolCalls))
	}
	for _, tc := range prepared.ToolCalls {
		if strings.EqualFold(tc.Function.Name, "none") {
			t.Errorf("none sentinel leaked into tool calls: %+v", tc)
		}
	}
}

func TestPrepareToolBridgeResponse_NoneSentinelWithResult(t *testing.T) {
	// If the model puts a "result" in the none sentinel, treat it as final answer text.
	content := `{"name": "none", "arguments": {"result": "No tool needed — here is the answer."}}`
	prepared := prepareUpstreamBridgeTestResponse(content, nil)

	if prepared.HasCalls {
		t.Errorf("none sentinel should not produce tool calls, got %d", len(prepared.ToolCalls))
	}
	if prepared.DoneText != "No tool needed — here is the answer." {
		t.Errorf("expected DoneText to capture result, got %q", prepared.DoneText)
	}
}

func TestPrepareToolBridgeResponse_DoneSentinelStillIntercepted(t *testing.T) {
	// Ensure existing done/__done__ interception still works.
	content := `{"name": "done", "arguments": {"result": "final answer"}}`
	prepared := prepareUpstreamBridgeTestResponse(content, nil)

	if prepared.HasCalls {
		t.Errorf("done sentinel should not produce tool calls, got %d", len(prepared.ToolCalls))
	}
	if prepared.DoneText != "final answer" {
		t.Errorf("expected DoneText='final answer', got %q", prepared.DoneText)
	}
}

func TestPrepareToolBridgeResponse_RealToolCallNotIntercepted(t *testing.T) {
	// A real tool call must pass through untouched.
	content := `{"name": "bash", "arguments": {"command": "ls"}}`
	prepared := prepareUpstreamBridgeTestResponse(content, nil)

	if !prepared.HasCalls || len(prepared.ToolCalls) != 1 {
		t.Fatalf("expected 1 real tool call, got hasCalls=%v len=%d", prepared.HasCalls, len(prepared.ToolCalls))
	}
	if prepared.ToolCalls[0].Function.Name != "bash" {
		t.Errorf("expected name=bash, got %q", prepared.ToolCalls[0].Function.Name)
	}
}

func TestPrepareToolBridgeResponse_EmptyNoneSentinelNoEcho(t *testing.T) {
	// {"name":"none","arguments":{}} should not echo "{}" as DoneText.
	content := `{"name": "none", "arguments": {}}`
	prepared := prepareUpstreamBridgeTestResponse(content, nil)

	if prepared.DoneText != "" {
		t.Errorf("empty none sentinel should not produce DoneText, got %q", prepared.DoneText)
	}
}

func TestStripRawDoneJSONLeakage_NoneSentinel(t *testing.T) {
	text := `Here is the answer. {"name": "none", "arguments": {}}`
	stripped := stripRawDoneJSONLeakage(text)
	if strings.Contains(stripped, "none") {
		t.Errorf("none sentinel JSON should be stripped, got %q", stripped)
	}
}

func TestStripRawDoneJSONLeakage_DoneSentinel(t *testing.T) {
	text := `Answer. {"name": "done", "arguments": {"result": "x"}}`
	stripped := stripRawDoneJSONLeakage(text)
	if strings.Contains(stripped, "done") {
		t.Errorf("done sentinel JSON should be stripped, got %q", stripped)
	}
}

func TestIsNoToolSentinelName(t *testing.T) {
	cases := map[string]bool{
		"done":     true,
		"__done__": true,
		"none":     true,
		"None":     true,
		"NONE":     true,
		"null":     true,
		"no_tool":  true,
		"noop":     true,
		"skip":     true,
		"bash":     false,
		"edit":     false,
		"":         false,
		"search":   false,
	}
	for name, want := range cases {
		if got := isNoToolSentinelName(name); got != want {
			t.Errorf("isNoToolSentinelName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestPrepareToolBridgeResponse_NativeNoneIntercepted(t *testing.T) {
	// Same flow via native tool_use entries (NDJSON path).
	entry := AgentValueEntry{
		Type:  "tool_use",
		ID:    "toolu_none1",
		Name:  "none",
		Input: json.RawMessage(`{}`),
	}
	prepared := prepareUpstreamBridgeTestResponse("", []AgentValueEntry{entry})
	if prepared.HasCalls {
		t.Errorf("native none sentinel should not produce tool calls, got %d", len(prepared.ToolCalls))
	}
}

func TestStripIncompleteToolCallJSON_TruncatedQuestionCall(t *testing.T) {
	// Model started emitting a question tool call but truncated mid-value.
	// The partial JSON should be stripped from residual text.
	text := `{"name": "question", "arguments": {"questions": [{"header": "Target component", "question": "Which project/repo and component holds the UI icon that s`
	stripped := stripIncompleteToolCallJSON(text)
	if strings.Contains(stripped, `"name"`) {
		t.Errorf("truncated tool-call JSON should be stripped, got %q", stripped)
	}
}

func TestStripIncompleteToolCallJSON_BalancedJSONPreserved(t *testing.T) {
	// A complete, balanced tool-call JSON should NOT be stripped here
	// (it's handled by parseToolCalls earlier in the pipeline).
	text := `{"name": "bash", "arguments": {"command": "ls"}}`
	stripped := stripIncompleteToolCallJSON(text)
	if stripped != text {
		t.Errorf("balanced JSON should be preserved, got %q", stripped)
	}
}

func TestStripIncompleteToolCallJSON_ProseWithTrailingFragment(t *testing.T) {
	text := `Here is my answer to your question. {"name": "question", "arguments": {"questions": [{"header": "h", "question": "q`
	stripped := stripIncompleteToolCallJSON(text)
	if strings.Contains(stripped, `"name"`) {
		t.Errorf("trailing truncated JSON should be stripped, got %q", stripped)
	}
	if !strings.Contains(stripped, "Here is my answer") {
		t.Errorf("prose before fragment should be preserved, got %q", stripped)
	}
}

func TestStripIncompleteToolCallJSON_NoToolCallPreserved(t *testing.T) {
	text := "Just a normal answer with no JSON at all."
	stripped := stripIncompleteToolCallJSON(text)
	if stripped != text {
		t.Errorf("plain prose should be unchanged, got %q", stripped)
	}
}

func TestIsBraceBalanced(t *testing.T) {
	cases := map[string]bool{
		`{}`:                true,
		`{"a": {}}`:         true,
		`{"a": {"b": "}"}}`: true, // brace in string doesn't count
		`{`:                 false,
		`{"a":`:             false,
		`{"a": "b"`:         false,
		`}`:                 false,
		`{"a": "{}"}`:       true, // braces in string literal
	}
	for input, want := range cases {
		if got := isBraceBalanced(input); got != want {
			t.Errorf("isBraceBalanced(%q) = %v, want %v", input, got, want)
		}
	}
}

// TestResidualTextCleaning_TruncatedEditCall verifies the end-to-end residual
// text cleaning path: when parseToolCalls can't close a truncated {"name":"edit",...}
// fragment, normalizeToolBridgeResidualText strips it so it never reaches the
// client as raw JSON.
func TestResidualTextCleaning_TruncatedEditCall(t *testing.T) {
	// Simulate what the handlers do: prepared.Remaining is the unparseable text.
	truncated := `{"name": "edit", "arguments": {"filePath": "C:\\Users\\Admin\\Downloads\\automata\\youtube.com\\youtube.com-watch-progress-tracker-with-neon-cloud-sync\\schema.sql", "oldString": "create index if not exists watch_progress`

	prepared := prepareUpstreamBridgeTestResponse(truncated, nil)
	if prepared.HasCalls {
		t.Errorf("truncated JSON should not parse into tool calls, got %d", len(prepared.ToolCalls))
	}
	remaining := prepared.Remaining
	cleaned := normalizeToolBridgeResidualText(remaining)
	if strings.Contains(cleaned, `"name"`) || strings.Contains(cleaned, `"arguments"`) {
		t.Errorf("truncated edit JSON should be stripped from residual, got %q", cleaned)
	}
}

func TestResidualTextCleaning_PartialEditCallWithPreamble(t *testing.T) {
	// Model emits some prose then a truncated tool call.
	text := `I'll edit the schema file now.

{"name": "edit", "arguments": {"filePath": "/tmp/schema.sql", "oldString": "create table`

	prepared := prepareUpstreamBridgeTestResponse(text, nil)
	if prepared.HasCalls {
		t.Errorf("truncated JSON should not parse into tool calls, got %d", len(prepared.ToolCalls))
	}
	cleaned := normalizeToolBridgeResidualText(prepared.Remaining)
	if strings.Contains(cleaned, `"name"`) {
		t.Errorf("truncated edit JSON should be stripped from residual, got %q", cleaned)
	}
	if !strings.Contains(cleaned, "I'll edit the schema file now") {
		t.Errorf("prose preamble should be preserved, got %q", cleaned)
	}
}
