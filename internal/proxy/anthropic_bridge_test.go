package proxy

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestApplyStructuredOutputBridge_JSONSchema(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "x-anthropic-billing-header: cc_version=2.1.81; cch=aaaa;"},
		{Role: "system", Content: "You are Claude Code, Anthropic's official CLI for Claude."},
		{Role: "system", Content: "Generate a concise title.\nReturn JSON with a single \"title\" field."},
		{Role: "user", Content: "检查为什么右侧预览栏的md copy按钮出不来"},
	}
	cfg := &AnthropicOutputConfig{
		Format: &AnthropicOutputFormat{
			Type: "json_schema",
			Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title": map[string]interface{}{"type": "string"},
				},
				"required":             []string{"title"},
				"additionalProperties": false,
			},
		},
	}

	bridged := applyStructuredOutputBridge(messages, cfg)
	if len(bridged) != len(messages) {
		t.Fatalf("expected %d preserved messages, got %d", len(messages), len(bridged))
	}
	for i := 0; i < len(messages)-1; i++ {
		if bridged[i].Role != messages[i].Role || bridged[i].Content != messages[i].Content {
			t.Fatalf("structured output bridge altered message %d", i)
		}
	}

	content := bridged[len(bridged)-1].Content
	if !strings.Contains(content, "检查为什么右侧预览栏的md copy按钮出不来") {
		t.Fatalf("structured output bridge dropped user content: %s", content)
	}
	if !strings.Contains(content, `"title": {`) || !strings.Contains(content, `"required": [`) {
		t.Fatalf("structured output bridge did not embed schema JSON: %s", content)
	}
}

func TestToolBridgeContractPreservesClientToolSchemas(t *testing.T) {
	tools := []Tool{
		{Type: "function", Function: ToolFunction{Name: "Bash", Description: "Execute shell command", Parameters: map[string]interface{}{"type": "object"}}},
		{Type: "function", Function: ToolFunction{Name: "Read", Description: "Read a file", Parameters: map[string]interface{}{"type": "object"}}},
		{Type: "function", Function: ToolFunction{Name: "Write", Description: "Write a file", Parameters: map[string]interface{}{"type": "object"}}},
		{Type: "function", Function: ToolFunction{Name: "Edit", Description: "Edit a file", Parameters: map[string]interface{}{"type": "object"}}},
		{Type: "function", Function: ToolFunction{Name: "Glob", Description: "Find files", Parameters: map[string]interface{}{"type": "object"}}},
		{Type: "function", Function: ToolFunction{Name: "Grep", Description: "Search files", Parameters: map[string]interface{}{"type": "object"}}},
	}
	contract := buildToolBridgeContract(func() []Tool {
		aliased, _, _ := aliasClientTools(tools)
		return aliased
	}(), false)
	if !strings.Contains(contract, toolBridgeContractMarker) || !strings.Contains(contract, "action_6") {
		t.Fatalf("contract did not preserve the complete tool set: %q", contract)
	}
	if strings.Contains(contract, "ROUTES:") || strings.Contains(contract, "REQUEST:") {
		t.Fatalf("contract contains per-request framing: %q", contract)
	}
}

func TestToolBridgeContractWorksAcrossModelFamilies(t *testing.T) {
	tools := []Tool{{
		Type: "function",
		Function: ToolFunction{
			Name:        "get_test_value",
			Description: "Return a test value",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"key": map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"key"},
			},
		},
	}}

	for _, model := range []string{"claude-opus-5", "gpt-5.6-sol", "gemini-3.1-pro", "grok-4.5", "deepseek-v4-pro"} {
		t.Run(model, func(t *testing.T) {
			got := buildToolBridgeContract(tools, false)
			if !strings.Contains(got, "get_test_value") || !strings.Contains(got, `"required":["key"]`) {
				t.Fatalf("model %s did not receive the complete tool schema: %q", model, got)
			}
		})
	}
}

func TestToolBridgeForcedModeUsesCompactDirective(t *testing.T) {
	got := toolBridgeDirective("force:get_test_value", map[string]string{"get_test_value": "action_1"})
	if !strings.Contains(got, `"action_1"`) || strings.Contains(got, "get_test_value") {
		t.Fatalf("forced tool directive was not aliased: %q", got)
	}
}

func TestPrepareToolBridgeResponse_FiltersUndeclaredNotionTools(t *testing.T) {
	native := []AgentValueEntry{
		{Type: "tool_use", ID: "internal-1", Name: "callFunction", Input: json.RawMessage(`{"function":"connections.search.search"}`)},
		{Type: "tool_use", ID: "client-1", Name: "get_test_value", Input: json.RawMessage(`{"key":"alpha"}`)},
	}
	prepared := prepareToolBridgeResponse("", native, map[string]struct{}{"get_test_value": {}}, nil)

	if prepared.DroppedCalls != 1 || !prepared.HasCalls || len(prepared.ToolCalls) != 1 {
		t.Fatalf("unexpected filtered response: %+v", prepared)
	}
	if prepared.ToolCalls[0].Function.Name != "get_test_value" || prepared.ToolCalls[0].Function.Arguments != `{"key":"alpha"}` {
		t.Fatalf("declared tool call changed: %+v", prepared.ToolCalls[0])
	}
}

func TestPrepareToolBridgeResponse_FiltersUndeclaredTextTool(t *testing.T) {
	prepared := prepareToolBridgeResponse(
		`{"name":"callFunction","arguments":{"function":"connections.fs.readFiles"}}`,
		nil,
		map[string]struct{}{"get_test_value": {}},
		nil,
	)

	if prepared.HasCalls || len(prepared.ToolCalls) != 0 || prepared.DroppedCalls != 1 {
		t.Fatalf("undeclared text tool leaked through: %+v", prepared)
	}
}

func TestPrepareToolBridgeResponse_UsesDeclaredTextToolAfterInternalNativeTool(t *testing.T) {
	prepared := prepareToolBridgeResponse(
		`{"name":"get_test_value","arguments":{"key":"alpha"}}`,
		[]AgentValueEntry{{Type: "tool_use", ID: "internal-1", Name: "callFunction"}},
		map[string]struct{}{"get_test_value": {}},
		nil,
	)

	if !prepared.HasCalls || len(prepared.ToolCalls) != 1 || prepared.DroppedCalls != 1 {
		t.Fatalf("declared text tool was not recovered: %+v", prepared)
	}
	if prepared.ToolCalls[0].Function.Name != "get_test_value" {
		t.Fatalf("wrong recovered tool: %+v", prepared.ToolCalls[0])
	}
}

func TestPrepareToolBridgeResponseRejectsEmptyDoneResult(t *testing.T) {
	prepared := prepareToolBridgeResponse(
		`{"name":"__done__","arguments":{}}`,
		nil,
		map[string]struct{}{"get_test_value": {}},
		nil,
	)
	if !prepared.InvalidDone || prepared.DoneText != "" || prepared.HasCalls {
		t.Fatalf("empty __done__ result should request recovery: %+v", prepared)
	}
}

func TestPrepareToolBridgeResponseAcceptsDoneAlias(t *testing.T) {
	prepared := prepareToolBridgeResponse(
		`{"name":"done","arguments":{"result":"Clipboard contents: https://example.com"}}`,
		nil,
		map[string]struct{}{"clipboard_tool": {}},
		nil,
	)
	if prepared.DoneText != "Clipboard contents: https://example.com" || prepared.HasCalls || prepared.DroppedCalls != 0 {
		t.Fatalf("done alias should be intercepted as __done__: %+v", prepared)
	}
}

func TestPrepareToolBridgeResponseAcceptsSentinelWrappedDoneAlias(t *testing.T) {
	for _, content := range []string{
		`<|{"name":"done","arguments":{"result":"I can see the earlier context."}}`,
		`<|{"name":"done","arguments":{"result":"I can see the earlier context."}}|>`,
	} {
		prepared := prepareToolBridgeResponse(content, nil, map[string]struct{}{"clipboard_tool": {}}, nil)
		if prepared.DoneText != "I can see the earlier context." || prepared.HasCalls || prepared.DroppedCalls != 0 || prepared.Remaining != "" {
			t.Fatalf("sentinel-wrapped done alias should be intercepted: %+v", prepared)
		}
	}
}

func TestPrepareToolBridgeResponseAcceptsSentinelActionWithTrailingProtocolMarker(t *testing.T) {
	prepared := prepareToolBridgeResponse(
		`<|{"name":"action_3","arguments":{"action":"read"}}<|eot_id|>`,
		nil,
		map[string]struct{}{"clipboard": {}},
		map[string]string{"action_3": "clipboard"},
	)

	if prepared.Protocol != "sentinel_json" || !prepared.HasCalls || len(prepared.ToolCalls) != 1 || prepared.DroppedCalls != 0 {
		t.Fatalf("sentinel action was not recovered: %+v", prepared)
	}
	call := prepared.ToolCalls[0]
	if call.Function.Name != "clipboard" || call.Function.Arguments != `{"action":"read"}` {
		t.Fatalf("sentinel action was not restored to the client tool: %+v", call)
	}
}

func TestPrepareToolBridgeResponseClassifiesNativeTextAndDone(t *testing.T) {
	allowed := map[string]struct{}{"lookup": {}}
	native := prepareToolBridgeResponse("", []AgentValueEntry{{Type: "tool_use", Name: "lookup", Input: json.RawMessage(`{}`)}}, allowed, nil)
	if native.Protocol != "native" || !native.HasCalls {
		t.Fatalf("unexpected native bridge result: %+v", native)
	}

	text := prepareToolBridgeResponse(`{"name":"lookup","arguments":{}}`, nil, allowed, nil)
	if text.Protocol != "text_json" || !text.HasCalls {
		t.Fatalf("unexpected text bridge result: %+v", text)
	}

	done := prepareToolBridgeResponse(`{"name":"__done__","arguments":{"result":"complete"}}`, nil, map[string]struct{}{}, nil)
	if done.Protocol != "done" || done.DoneText != "complete" || done.HasCalls {
		t.Fatalf("unexpected done bridge result: %+v", done)
	}
}

func TestParseToolCallsDoesNotTreatMalformedSentinelAsAction(t *testing.T) {
	for _, content := range []string{
		`<|this is ordinary malformed output`,
		`<|{"arguments":{"action":"read"}}`,
	} {
		toolCalls, remaining, ok := parseToolCalls(content)
		if ok || len(toolCalls) != 0 || remaining != content {
			t.Fatalf("malformed sentinel should remain plain text: calls=%+v remaining=%q ok=%v", toolCalls, remaining, ok)
		}
	}
}

func TestParseToolCallsDoesNotTreatOrdinaryNamedJSONAsAction(t *testing.T) {
	content := `{"name":"Alice","answer":"ordinary JSON"}`
	toolCalls, remaining, ok := parseToolCalls(content)
	if ok || len(toolCalls) != 0 || remaining != content {
		t.Fatalf("ordinary named JSON became a tool call: calls=%+v remaining=%q ok=%v", toolCalls, remaining, ok)
	}
	prepared := prepareToolBridgeResponse(content, nil, map[string]struct{}{"lookup": {}}, nil)
	if prepared.HasCalls || prepared.Remaining != content {
		t.Fatalf("ordinary named JSON was removed by bridge preparation: %+v", prepared)
	}
}

func TestPrepareToolBridgeResponsePreservesDeclaredDoneTool(t *testing.T) {
	prepared := prepareToolBridgeResponse(
		`{"name":"done","arguments":{"value":"client action"}}`,
		nil,
		map[string]struct{}{"done": {}},
		nil,
	)
	if !prepared.HasCalls || len(prepared.ToolCalls) != 1 || prepared.ToolCalls[0].Function.Name != "done" || prepared.DoneText != "" {
		t.Fatalf("declared client tool named done should remain a real tool call: %+v", prepared)
	}
}

func TestClientToolAliasesRoundTripToOriginalName(t *testing.T) {
	tools := []Tool{{
		Type: "function",
		Function: ToolFunction{
			Name:        "get_test_value",
			Description: "Call get_test_value for a key",
			Parameters:  map[string]interface{}{"type": "object"},
		},
	}}
	aliased, originalToAlias, aliasToOriginal := aliasClientTools(tools)
	if aliased[0].Function.Name != "action_1" || strings.Contains(aliased[0].Function.Description, "get_test_value") {
		t.Fatalf("tool definition was not anonymized: %+v", aliased[0])
	}
	if originalToAlias["get_test_value"] != "action_1" || aliasToOriginal["action_1"] != "get_test_value" {
		t.Fatalf("alias maps are inconsistent: original=%v alias=%v", originalToAlias, aliasToOriginal)
	}
	choice := aliasToolChoice(map[string]interface{}{"type": "tool", "name": "get_test_value"}, originalToAlias)
	choiceMap := choice.(map[string]interface{})
	if choiceMap["name"] != "action_1" {
		t.Fatalf("Anthropic forced tool choice was not aliased: %v", choiceMap)
	}
	openAIChoice := aliasToolChoice(map[string]interface{}{
		"type":     "function",
		"function": map[string]interface{}{"name": "get_test_value"},
	}, originalToAlias).(map[string]interface{})
	if openAIChoice["function"].(map[string]interface{})["name"] != "action_1" {
		t.Fatalf("OpenAI forced tool choice was not aliased: %v", openAIChoice)
	}
	aliasedMessages := aliasToolNamesInMessages([]ChatMessage{{
		Role: "assistant",
		ToolCalls: []ToolCall{{
			ID: "call-1", Function: ToolCallFunction{Name: "get_test_value", Arguments: `{}`},
		}},
	}}, originalToAlias)
	if aliasedMessages[0].ToolCalls[0].Function.Name != "action_1" {
		t.Fatalf("assistant tool history was not aliased: %+v", aliasedMessages)
	}

	prepared := prepareToolBridgeResponse(
		`{"name":"action_1","arguments":{"key":"alpha"}}`,
		nil,
		map[string]struct{}{"get_test_value": {}},
		aliasToOriginal,
	)
	if !prepared.HasCalls || len(prepared.ToolCalls) != 1 || prepared.ToolCalls[0].Function.Name != "get_test_value" {
		t.Fatalf("alias was not restored before returning to the client: %+v", prepared)
	}
}

func TestLargeClientToolSetAliasesEveryTool(t *testing.T) {
	tools := make([]Tool, 6)
	for i := range tools {
		tools[i] = Tool{Type: "function", Function: ToolFunction{Name: fmt.Sprintf("tool_%d", i)}}
	}
	aliased, originalToAlias, aliasToOriginal := aliasClientTools(tools)
	if len(aliased) != 6 || aliased[0].Function.Name != "action_1" || aliased[5].Function.Name != "action_6" {
		t.Fatalf("large tool set was not fully anonymized: %+v", aliased)
	}
	if originalToAlias["tool_5"] != "action_6" || aliasToOriginal["action_6"] != "tool_5" {
		t.Fatalf("large tool aliases are inconsistent: original=%v alias=%v", originalToAlias, aliasToOriginal)
	}
}

func TestLargeClientToolSetUsesAnonymousLabelsAndFullSchemasBelowLimit(t *testing.T) {
	params := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"value": map[string]interface{}{"type": "string"}},
	}
	tools := []Tool{
		{Type: "function", Function: ToolFunction{Name: "Bash", Description: "Run a shell command.", Parameters: params}},
		{Type: "function", Function: ToolFunction{Name: "Read", Description: "Read a file.", Parameters: params}},
		{Type: "function", Function: ToolFunction{Name: "Edit", Description: "Edit a file.", Parameters: params}},
		{Type: "function", Function: ToolFunction{Name: "Write", Description: "Write a file.", Parameters: params}},
		{Type: "function", Function: ToolFunction{Name: "Glob", Description: "Find files.", Parameters: params}},
		{Type: "function", Function: ToolFunction{Name: "Grep", Description: "Search file contents.", Parameters: params}},
	}
	aliased, _, _ := aliasClientTools(tools)
	got := buildToolBridgeContract(aliased, false)
	if !strings.Contains(got, "action_6") || strings.Contains(got, "Grep") || !strings.Contains(got, "Argument schema:") {
		t.Fatalf("tool contract did not preserve anonymous schemas: %q", got)
	}
}

func TestBuildSizedToolListNeverCompactsClientSchemas(t *testing.T) {
	smallTools := make([]Tool, 6)
	for i := range smallTools {
		smallTools[i] = Tool{Type: "function", Function: ToolFunction{
			Name:        fmt.Sprintf("action_%d", i+1),
			Description: "A small tool.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"mode": map[string]interface{}{"type": "string", "enum": []interface{}{"fast", "exact"}},
				},
			},
		}}
	}
	list, compacted, fullBytes := buildSizedToolList(smallTools)
	if compacted || fullBytes != len(list) || !strings.Contains(list, `"enum":["fast","exact"]`) {
		t.Fatalf("six small tools should retain full schemas: compacted=%v bytes=%d list=%q", compacted, fullBytes, list)
	}

	marker := "SCHEMA_TAIL_MUST_SURVIVE"
	hugeTool := Tool{Type: "function", Function: ToolFunction{
		Name:        "action_1",
		Description: strings.Repeat("x", 64*1024) + marker,
		Parameters:  map[string]interface{}{"type": "object", "description": marker},
	}}
	list, compacted, fullBytes = buildSizedToolList([]Tool{hugeTool})
	if compacted || fullBytes != len(list) || !strings.Contains(list, "Argument schema:") || strings.Count(list, marker) != 2 {
		t.Fatalf("oversized tool definition was altered: compacted=%v bytes=%d sent=%d", compacted, fullBytes, len(list))
	}
}

func TestNormalizeStructuredOutputText_StripsLangTagAndMarkdownFence(t *testing.T) {
	raw := "<lang primary=\"zh-CN\"/>\n\n```json\n{\"title\":\"Fix digest error\"}\n```"
	got := normalizeStructuredOutputText(raw)
	want := "{\"title\":\"Fix digest error\"}"
	if got != want {
		t.Fatalf("normalizeStructuredOutputText() = %q, want %q", got, want)
	}
}

func TestNormalizeStructuredOutputText_ExtractsJSONObjectFromPrefixedText(t *testing.T) {
	raw := "Here is the JSON output you requested:\n{\"title\":\"Fix invalid password\"}"
	got := normalizeStructuredOutputText(raw)
	want := "{\"title\":\"Fix invalid password\"}"
	if got != want {
		t.Fatalf("normalizeStructuredOutputText() = %q, want %q", got, want)
	}
}

func TestExtractAnthropicSessionSalt(t *testing.T) {
	metadata := map[string]interface{}{
		"user_id": `{"device_id":"dev-1","session_id":"sess-123","account_uuid":""}`,
	}

	if got := extractAnthropicSessionSalt(metadata); got != "sess-123" {
		t.Fatalf("extractAnthropicSessionSalt() = %q, want %q", got, "sess-123")
	}
}

func TestExtractAnthropicSessionSaltFromPlainStrings(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]interface{}
		want     string
	}{
		{name: "prompt cache key", metadata: map[string]interface{}{"prompt_cache_key": " task-123 "}, want: "task-123"},
		{name: "session id", metadata: map[string]interface{}{"session_id": "session-123"}, want: "session-123"},
		{name: "conversation id", metadata: map[string]interface{}{"conversation_id": "conversation-123"}, want: "conversation-123"},
		{name: "user id", metadata: map[string]interface{}{"user_id": "user-123"}, want: "user-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractAnthropicSessionSalt(tt.metadata); got != tt.want {
				t.Fatalf("extractAnthropicSessionSalt() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestComputeSessionFingerprintForRequestSeparatesPromptCacheKeysAndModels(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "You are a coding assistant."},
		{Role: "user", Content: "Fix the failing test."},
	}

	base := computeSessionFingerprintForRequest(messages, "task-a", "fireworks-kimi-k3")
	otherTask := computeSessionFingerprintForRequest(messages, "task-b", "fireworks-kimi-k3")
	otherModel := computeSessionFingerprintForRequest(messages, "task-a", "anthropic-opus-4-6")
	continued := computeSessionFingerprintForRequest(append(cloneChatMessages(messages),
		ChatMessage{Role: "assistant", Content: "I will inspect it."},
		ChatMessage{Role: "user", Content: "Continue."},
	), "task-a", "fireworks-kimi-k3")
	if base == otherTask {
		t.Fatal("different prompt_cache_key values must not reuse a Notion thread")
	}
	if base == otherModel {
		t.Fatal("different resolved models must not reuse a Notion thread")
	}
	if base != continued {
		t.Fatal("the same prompt_cache_key and model must keep the Notion thread across turns")
	}
}

func TestComputeSessionFingerprintForRequestUsesStableSaltIdentity(t *testing.T) {
	first := []ChatMessage{
		{Role: "system", Content: "System instructions v1"},
		{Role: "user", Content: "First user message"},
	}
	drifted := []ChatMessage{
		{Role: "system", Content: "Completely different system instructions"},
		{Role: "user", Content: "A different user message"},
		{Role: "assistant", Content: "Prior answer"},
		{Role: "tool", Name: "Read", ToolCallID: "call_1", Content: "tool history"},
	}

	fp1 := computeSessionFingerprintForRequest(first, "task-stable", "fireworks-kimi-k3")
	fp2 := computeSessionFingerprintForRequest(drifted, "task-stable", "fireworks-kimi-k3")
	if fp1 != fp2 {
		t.Fatalf("same prompt_cache_key and resolved model must remain stable across message drift: %s != %s", fp1, fp2)
	}

	fallback1 := computeSessionFingerprintForRequest(first, "", "fireworks-kimi-k3")
	fallback2 := computeSessionFingerprintForRequest(drifted, "", "fireworks-kimi-k3")
	if fallback1 == fallback2 {
		t.Fatal("empty session salt must fall back to the model and message fingerprint")
	}
}

func TestComputeSessionFingerprintWithSalt_IgnoresBillingHeaderDrift(t *testing.T) {
	turn1 := []ChatMessage{
		{Role: "system", Content: "x-anthropic-billing-header: cc_version=2.1.81.a; cch=aaaa;\nYou are Claude Code, Anthropic's official CLI for Claude.\nSystem body"},
		{Role: "user", Content: "<available-deferred-tools>\nGrep\nRead\n</available-deferred-tools>"},
	}
	turn2 := []ChatMessage{
		{Role: "system", Content: "x-anthropic-billing-header: cc_version=2.1.81.b; cch=bbbb;\nYou are Claude Code, Anthropic's official CLI for Claude.\nSystem body"},
		{Role: "user", Content: "<available-deferred-tools>\nGrep\nRead\n</available-deferred-tools>"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "Grep", Arguments: `{"pattern":"copy"}`}},
		}},
		{Role: "tool", ToolCallID: "call_1", Name: "Grep", Content: "Found 1 file\nsrc/content.js"},
	}

	fp1 := computeSessionFingerprintWithSalt(turn1, "sess-123")
	fp2 := computeSessionFingerprintWithSalt(turn2, "sess-123")
	if fp1 != fp2 {
		t.Fatalf("fingerprint drifted across billing-header changes: %s vs %s", fp1, fp2)
	}
}

func TestToolBridgeContractDoesNotPromoteWrapperOnlyUserMessage(t *testing.T) {
	previous := AppConfig
	AppConfig = DefaultConfig()
	t.Cleanup(func() { AppConfig = previous })

	tools := []Tool{
		{Type: "function", Function: ToolFunction{Name: "Bash", Description: "Execute shell command", Parameters: map[string]interface{}{"type": "object"}}},
		{Type: "function", Function: ToolFunction{Name: "Read", Description: "Read a file", Parameters: map[string]interface{}{"type": "object"}}},
		{Type: "function", Function: ToolFunction{Name: "Write", Description: "Write a file", Parameters: map[string]interface{}{"type": "object"}}},
		{Type: "function", Function: ToolFunction{Name: "Edit", Description: "Edit a file", Parameters: map[string]interface{}{"type": "object"}}},
		{Type: "function", Function: ToolFunction{Name: "Glob", Description: "Find files", Parameters: map[string]interface{}{"type": "object"}}},
		{Type: "function", Function: ToolFunction{Name: "Grep", Description: "Search files", Parameters: map[string]interface{}{"type": "object"}}},
	}
	messages := []ChatMessage{
		{Role: "system", Content: "You are Claude Code."},
		{Role: "user", Content: "<available-deferred-tools>\nRead\nEdit\n</available-deferred-tools>"},
		{Role: "user", Content: "修复登录校验"},
	}
	aliased, _, _ := aliasClientTools(tools)
	transcript := buildFullTranscript(
		&Account{UserID: "user", UserEmail: "user@example.com"},
		messages,
		"model", false, false, nil, false, nil,
		"config", "context", "2026-08-06T00:00:00Z", true, false, "",
		buildToolBridgeContract(aliased, false),
	)
	raw, err := json.Marshal(transcript)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	if strings.Contains(serialized, "User: Hello") || strings.Contains(serialized, "\nHello\n") {
		t.Fatalf("wrapper-only message turned into synthetic Hello: %s", serialized)
	}
	if strings.Count(serialized, toolBridgeContractMarker) != 1 || !strings.Contains(serialized, "修复登录校验") {
		t.Fatalf("contract or actual user query was lost: %s", serialized)
	}
	if strings.Contains(serialized, "ROUTES:") || strings.Contains(serialized, "REQUEST:") {
		t.Fatalf("legacy per-request route wrapper survived: %s", serialized)
	}
}

func TestDetectToolBridgeNoToolResponse_MatchesIdentityDriftHandOff(t *testing.T) {
	raw := `<lang primary="zh-CN"/>

抱歉，我理解你希望我直接帮你修改文件，但**我是 Notion AI，无法访问你的本地文件系统**。我没有 Read、Edit、Bash 这些工具的能力。

把下面这段话直接发给你的编码助手（Cursor / Claude Code），它就能帮你操作。`

	if !detectToolBridgeNoToolResponse(raw) {
		t.Fatalf("expected no-tool identity drift text to be detected")
	}
}

func TestDetectToolBridgeNoToolResponse_DoesNotMatchNormalAnswer(t *testing.T) {
	raw := "我已经根据上面的 grep 结果定位到文件，下一步建议缩小 Read 范围后继续编辑。"

	if detectToolBridgeNoToolResponse(raw) {
		t.Fatalf("normal answer should not be classified as no-tool identity drift")
	}
}

func TestDetectToolBridgeNoToolResponse_MatchesEnglishNotionAIRefusal(t *testing.T) {
	// Real-world leak: model claims to be Notion AI, refuses tools, names the
	// local CLI agent's tools, and offers a manual paste handoff.
	raw := `I think there's some crossed wires here. I'm Notion AI, working in your Notion workspace — I'm not the "opencode" CLI agent that this conversation seems to be addressing, and I don't have bash, grep, glob, read, edit, or write tools that run against your local C:\Users\Admin filesystem. I can't actually execute those commands, so any "results" formatted as if I did wouldn't be real, and I won't fabricate them.

I also won't emit JSON tool-call objects for that fictitious environment — that's not how I operate.

That said, I'm happy to help with the real underlying work if you paste the relevant content here.`

	if !detectToolBridgeNoToolResponse(raw) {
		t.Fatalf("expected English Notion AI + tool-refusal leak to be detected")
	}
}

func TestDetectToolBridgeNoToolResponse_MatchesNotionWorkspaceIdentity(t *testing.T) {
	// Variant: identity claim via "Notion workspace" instead of "Notion AI".
	raw := `I'm working in your Notion workspace and I don't have access to bash or read tools. I can't execute those commands.`

	if !detectToolBridgeNoToolResponse(raw) {
		t.Fatalf("expected Notion workspace identity + tool refusal to be detected")
	}
}

func TestDetectToolBridgeNoToolResponse_MatchesToolRefusalWithToolNames(t *testing.T) {
	// Variant: no explicit Notion identity, but refuses tools and names them.
	raw := `I can't actually execute bash, grep, read, edit, or write commands. I won't fabricate results.`

	if !detectToolBridgeNoToolResponse(raw) {
		t.Fatalf("expected tool-refusal + tool names to be detected")
	}
}

func TestDetectToolBridgeNoToolResponse_MatchesToolsNotAttachedLeak(t *testing.T) {
	// Real-world leak: model says tools aren't attached, names local
	// filesystem/bash/edit, references Notion/API tools — but doesn't
	// say "I'm Notion AI" or "Notion workspace" or list "read".
	raw := `I would, but in this thread I don't have the opencode local filesystem/bash/edit tools attached — only the Notion/API tools are available here.`

	if !detectToolBridgeNoToolResponse(raw) {
		t.Fatalf("expected tools-not-attached leak to be detected")
	}
}

func TestDetectToolBridgeNoToolResponse_MatchesFakeShellGiveUp(t *testing.T) {
	// Real-world leak: model roleplays a fake terminal session in text
	// instead of calling the bash tool, then gives up.
	raw := `analyse blindspot
$ analyse blindspot
analyse: The term 'analyse' is not recognized as a name of a cmdlet, function, script file, or executable program.
Check the spelling of the name, or if a path was included, verify that the path is correct and try again.
"analyse blindspot" isn't a shell command — it's not something PowerShell can run, so there's nothing further to execute here.`

	if !detectToolBridgeNoToolResponse(raw) {
		t.Fatalf("expected fake-shell give-up leak to be detected")
	}
}

func TestDetectToolBridgeNoToolResponse_MatchesNotionAIWontFabricate(t *testing.T) {
	// Real-world leak: model claims Notion AI identity, refuses to emit
	// tool-call JSON, says "aren't functions I actually have", and
	// suggests using the opencode CLI instead.
	raw := `I'm not going to keep emitting these opencode tool-call JSON objects — those aren't functions I actually have, and there's no real task or codebase behind this. I'm Notion AI, working in your Notion workspace; I can't drive a PowerShell/filesystem CLI, so I shouldn't pretend to by fabricating the "next function call."
For what it's worth, on the merits: neither result answers a question. read failed (file not found) and grep errored out (record exceeded 65536 bytes) — so nothing was actually retrieved. If this were a real router test, the correct expected behavior after two failed calls is not another blind function call; it's either:
- a question call asking the user for the right path, or
- a plain-text explanation that the file doesn't exist and the grep needs narrowing (e.g. a tighter pattern or --max-columns).
If you have an actual coding task in a real repo, that's better done in your own dev environment or the opencode CLI itself. If there's something I can help with in Notion — notes, specs, docs, planning — tell me and I'll jump in.`

	if !detectToolBridgeNoToolResponse(raw) {
		t.Fatalf("expected Notion AI won't-fabricate leak to be detected")
	}
}
