package proxy

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestConvertOpenAIChatCompletionRequestPreservesReasoningEffortAndResponseFormat(t *testing.T) {
	var req OpenAIChatCompletionRequest
	err := json.Unmarshal([]byte(`{
		"model":"gpt-5.4",
		"messages":[{"role":"user","content":"hello"}],
		"reasoning_effort":"high",
		"response_format":{"type":"json_object"}
	}`), &req)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	anthReq, err := convertOpenAIChatCompletionRequest(&req)
	if err != nil {
		t.Fatalf("convertOpenAIChatCompletionRequest() error = %v", err)
	}
	if anthReq.OutputConfig == nil || anthReq.OutputConfig.Effort != "high" {
		t.Fatalf("output_config effort = %#v", anthReq.OutputConfig)
	}
	if anthReq.OutputConfig.Format == nil || anthReq.OutputConfig.Format.Type != "json_schema" {
		t.Fatalf("output_config format = %#v", anthReq.OutputConfig)
	}

	req.ResponseFormat = nil
	anthReq, err = convertOpenAIChatCompletionRequest(&req)
	if err != nil {
		t.Fatalf("convertOpenAIChatCompletionRequest() without response_format error = %v", err)
	}
	if anthReq.OutputConfig == nil || anthReq.OutputConfig.Effort != "high" || anthReq.OutputConfig.Format != nil {
		t.Fatalf("output_config without response_format = %#v", anthReq.OutputConfig)
	}
}

func TestConvertOpenAIResponsesRequestPreservesReasoningEffortAndTextFormat(t *testing.T) {
	var req OpenAIResponsesRequest
	err := json.Unmarshal([]byte(`{
		"model":"gpt-5.4",
		"input":"hello",
		"reasoning":{"effort":"high"},
		"text":{"format":{"type":"json_object"}}
	}`), &req)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	anthReq, err := convertOpenAIResponsesRequest(&req)
	if err != nil {
		t.Fatalf("convertOpenAIResponsesRequest() error = %v", err)
	}
	if anthReq.OutputConfig == nil || anthReq.OutputConfig.Effort != "high" {
		t.Fatalf("output_config effort = %#v", anthReq.OutputConfig)
	}
	if anthReq.OutputConfig.Format == nil || anthReq.OutputConfig.Format.Type != "json_schema" {
		t.Fatalf("output_config format = %#v", anthReq.OutputConfig)
	}

	req.Text = nil
	anthReq, err = convertOpenAIResponsesRequest(&req)
	if err != nil {
		t.Fatalf("convertOpenAIResponsesRequest() without text format error = %v", err)
	}
	if anthReq.OutputConfig == nil || anthReq.OutputConfig.Effort != "high" || anthReq.OutputConfig.Format != nil {
		t.Fatalf("output_config without text format = %#v", anthReq.OutputConfig)
	}
}

func TestConvertOpenAIResponsesRequestPreservesDeveloperInstructionsAndTaskIdentity(t *testing.T) {
	req := &OpenAIResponsesRequest{
		Model:          "kimi-k3",
		PromptCacheKey: "codex-task-123",
		Input: []interface{}{
			map[string]interface{}{
				"type": "message",
				"role": "developer",
				"content": []interface{}{
					map[string]interface{}{"type": "input_text", "text": "完整保留这段开发者指令。"},
				},
			},
			map[string]interface{}{
				"type": "message",
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "input_text", "text": "回答当前问题。"},
				},
			},
		},
	}

	anthReq, err := convertOpenAIResponsesRequest(req)
	if err != nil {
		t.Fatalf("convertOpenAIResponsesRequest() error = %v", err)
	}
	if anthReq.System != "完整保留这段开发者指令。" {
		t.Fatalf("system = %#v", anthReq.System)
	}
	if anthReq.Metadata["prompt_cache_key"] != "codex-task-123" {
		t.Fatalf("metadata = %#v", anthReq.Metadata)
	}
	if len(anthReq.Messages) != 1 || anthReq.Messages[0].Role != "user" {
		t.Fatalf("messages = %#v", anthReq.Messages)
	}
}

func TestConvertOpenAIResponsesRequestExpandsNamespacesAndWebSearch(t *testing.T) {
	var req OpenAIResponsesRequest
	err := json.Unmarshal([]byte(`{
		"model":"kimi-k3",
		"input":"hello",
		"tools":[
			{"type":"function","name":"same_name","description":"direct","parameters":{"type":"object"}},
			{"type":"namespace","name":"alpha","description":"Alpha namespace.","tools":[
				{"type":"function","name":"same_name","description":"Alpha tool.","parameters":{"type":"object","properties":{"alpha":{"type":"string"}}},"defer_loading":true}
			]},
			{"type":"namespace","name":"beta","tools":[
				{"type":"function","name":"same_name","description":"beta tool","parameters":{"type":"object","properties":{"beta":{"type":"boolean"}}}}
			]},
			{"type":"web_search"}
		],
		"tool_choice":{"type":"function","namespace":"alpha","name":"same_name"}
	}`), &req)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !req.Tools[1].Tools[0].DeferLoading {
		t.Fatal("namespace child defer_loading was not parsed")
	}

	anthReq, aliases, err := convertOpenAIResponsesRequestWithToolAliases(&req)
	if err != nil {
		t.Fatalf("convertOpenAIResponsesRequestWithToolAliases() error = %v", err)
	}
	if len(anthReq.Tools) != 4 {
		t.Fatalf("tools = %#v, want direct + two namespaced + WebSearch", anthReq.Tools)
	}
	if anthReq.Tools[0].Name != "same_name" {
		t.Fatalf("direct tool name = %q, want same_name", anthReq.Tools[0].Name)
	}

	aliasByNamespace := make(map[string]string)
	validAlias := regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	for alias, identity := range aliases {
		if identity.Name != "same_name" {
			t.Fatalf("identity for %q = %#v", alias, identity)
		}
		if !validAlias.MatchString(alias) {
			t.Fatalf("alias %q is not a legal tool name", alias)
		}
		aliasByNamespace[identity.Namespace] = alias
	}
	if aliasByNamespace["alpha"] == "" || aliasByNamespace["beta"] == "" {
		t.Fatalf("aliases = %#v, want alpha and beta identities", aliases)
	}
	if aliasByNamespace["alpha"] == aliasByNamespace["beta"] || aliasByNamespace["alpha"] == "same_name" {
		t.Fatalf("aliases collide: %#v", aliasByNamespace)
	}
	alphaSchema, ok := anthReq.Tools[1].InputSchema.(map[string]interface{})
	if !ok || nestedMapValue(alphaSchema, "properties", "alpha") == nil {
		t.Fatalf("alpha inputSchema was not preserved: %#v", anthReq.Tools[1].InputSchema)
	}
	if anthReq.Tools[1].Description != "Alpha namespace.\n\nAlpha tool." {
		t.Fatalf("alpha description = %q", anthReq.Tools[1].Description)
	}
	if anthReq.Tools[3].Name != "WebSearch" {
		t.Fatalf("web_search mapped to %q, want WebSearch", anthReq.Tools[3].Name)
	}
	_, hasWebSearch := filterNativeSearchTools([]Tool{{
		Type: "function",
		Function: ToolFunction{
			Name:       anthReq.Tools[3].Name,
			Parameters: anthReq.Tools[3].InputSchema,
		},
	}})
	if !hasWebSearch {
		t.Fatal("mapped web_search did not enter the existing native WebSearch path")
	}

	toolChoice, ok := anthReq.ToolChoice.(map[string]interface{})
	if !ok || toolChoice["type"] != "tool" || toolChoice["name"] != aliasByNamespace["alpha"] {
		t.Fatalf("tool_choice = %#v, want forced alpha alias", anthReq.ToolChoice)
	}
}

func TestConvertOpenAIResponsesRequestAcceptsNamespacedFunctionCallHistory(t *testing.T) {
	var req OpenAIResponsesRequest
	err := json.Unmarshal([]byte(`{
		"model":"kimi-k3",
		"tools":[{"type":"namespace","name":"collaboration","description":"Coordinate agents.","tools":[
			{"type":"function","name":"send_message","description":"Send a message.","parameters":{"type":"object","properties":{"target":{"type":"string"}},"required":["target"]},"defer_loading":true}
		]}],
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"notify the agent"}]},
			{"type":"reasoning","id":"rs_123","summary":[{"type":"summary_text","text":"Need to notify the agent."}]},
			{"type":"function_call","call_id":"call_123","name":"send_message","namespace":"collaboration","arguments":"{\"target\":\"/root\"}"},
			{"type":"function_call_output","call_id":"call_123","output":"delivered"}
		]
	}`), &req)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	anthReq, aliases, err := convertOpenAIResponsesRequestWithToolAliases(&req)
	if err != nil {
		t.Fatalf("convertOpenAIResponsesRequestWithToolAliases() error = %v", err)
	}
	if len(anthReq.Messages) != 3 {
		t.Fatalf("messages = %#v, want user -> assistant tool_use -> user tool_result", anthReq.Messages)
	}
	if anthReq.Messages[0].Role != "user" || anthReq.Messages[1].Role != "assistant" || anthReq.Messages[2].Role != "user" {
		t.Fatalf("message roles = %#v", anthReq.Messages)
	}
	toolUseBlocks, ok := anthReq.Messages[1].Content.([]interface{})
	if !ok || len(toolUseBlocks) != 1 {
		t.Fatalf("tool use content = %#v", anthReq.Messages[1].Content)
	}
	toolUse := toolUseBlocks[0].(map[string]interface{})
	alias := stringValue(toolUse["name"])
	if identity := aliases[alias]; identity != (openAIToolIdentity{Namespace: "collaboration", Name: "send_message"}) {
		t.Fatalf("tool use alias %q identity = %#v", alias, identity)
	}
	if toolUse["id"] != "call_123" || string(toolUse["input"].(json.RawMessage)) != `{"target":"/root"}` {
		t.Fatalf("tool use = %#v", toolUse)
	}
	toolResultBlocks := anthReq.Messages[2].Content.([]interface{})
	toolResult := toolResultBlocks[0].(map[string]interface{})
	if toolResult["type"] != "tool_result" || toolResult["tool_use_id"] != "call_123" || toolResult["content"] != "delivered" {
		t.Fatalf("tool result = %#v", toolResult)
	}
}

func TestConvertOpenAIResponsesRequestRejectsUnknownNamespacedFunctionCall(t *testing.T) {
	req := &OpenAIResponsesRequest{
		Model: "kimi-k3",
		Tools: []OpenAITool{{
			Type:  "namespace",
			Name:  "known",
			Tools: []OpenAITool{{Type: "function", Name: "run"}},
		}},
		Input: []interface{}{
			map[string]interface{}{
				"type":      "function_call",
				"call_id":   "call_123",
				"name":      "run",
				"namespace": "unknown",
				"arguments": `{}`,
			},
		},
	}

	_, err := convertOpenAIResponsesRequest(req)
	if err == nil || !strings.Contains(err.Error(), `unknown tool "run" in namespace "unknown"`) {
		t.Fatalf("error = %v, want unknown namespaced function_call rejection", err)
	}
}

func TestConvertOpenAIChatCompletionRequestRejectsNamespaceTools(t *testing.T) {
	req := &OpenAIChatCompletionRequest{
		Model:    "gpt-5.4",
		Messages: []OpenAIChatMessage{{Role: "user", Content: "hello"}},
		Tools: []OpenAITool{{
			Type:  "namespace",
			Name:  "collaboration",
			Tools: []OpenAITool{{Type: "function", Name: "send_message"}},
		}},
	}

	_, err := convertOpenAIChatCompletionRequest(req)
	if err == nil || !strings.Contains(err.Error(), `unsupported tool type "namespace" for Chat Completions`) {
		t.Fatalf("error = %v, want explicit Chat namespace rejection", err)
	}
}

func TestConvertOpenAIResponsesRequestRejectsNamespaceOnlyToolChoice(t *testing.T) {
	req := &OpenAIResponsesRequest{
		Model: "kimi-k3",
		Input: "hello",
		Tools: []OpenAITool{{
			Type:  "namespace",
			Name:  "alpha",
			Tools: []OpenAITool{{Type: "function", Name: "run"}},
		}},
		ToolChoice: map[string]interface{}{"type": "namespace", "name": "alpha"},
	}

	_, err := convertOpenAIResponsesRequest(req)
	if err == nil || !strings.Contains(err.Error(), "choose a function within the namespace") {
		t.Fatalf("error = %v, want explicit namespace tool_choice rejection", err)
	}
}

func TestBuildOpenAIResponsesResponseRestoresNamespace(t *testing.T) {
	aliases := map[string]openAIToolIdentity{
		"ns_alpha_same": {Namespace: "alpha", Name: "same_name"},
	}
	resp := buildOpenAIResponsesResponse("resp_test", 123, "kimi-k3", &AnthropicResponse{
		Content: []AnthropicContentBlock{
			{Type: "tool_use", ID: "call_namespaced", Name: "ns_alpha_same", Input: json.RawMessage(`{"value":1}`)},
			{Type: "tool_use", ID: "call_direct", Name: "direct_tool", Input: json.RawMessage(`{"value":2}`)},
		},
	}, aliases)

	output, ok := resp["output"].([]map[string]interface{})
	if !ok || len(output) != 2 {
		t.Fatalf("output = %#v", resp["output"])
	}
	if output[0]["name"] != "same_name" || output[0]["namespace"] != "alpha" {
		t.Fatalf("namespaced output = %#v", output[0])
	}
	if output[1]["name"] != "direct_tool" {
		t.Fatalf("direct output = %#v", output[1])
	}
	if _, exists := output[1]["namespace"]; exists {
		t.Fatalf("direct output unexpectedly has namespace: %#v", output[1])
	}
}

func TestOpenAIResponsesStreamTranscoderRestoresNamespace(t *testing.T) {
	rr := httptest.NewRecorder()
	aliases := map[string]openAIToolIdentity{
		"ns_alpha_same": {Namespace: "alpha", Name: "same_name"},
	}
	transcoder := newOpenAIResponsesStreamTranscoder(rr, rr, "resp_test", "kimi-k3", 456, aliases)
	frames := []anthropicSSEFrame{
		{Event: "message_start", Data: json.RawMessage(`{"message":{"usage":{"input_tokens":9}}}`)},
		{Event: "content_block_start", Data: json.RawMessage(`{"index":0,"content_block":{"type":"tool_use","id":"call_1","name":"ns_alpha_same","input":{}}}`)},
		{Event: "content_block_delta", Data: json.RawMessage(`{"index":0,"delta":{"type":"input_json_delta","partial_json":"{\"value\":1}"}}`)},
		{Event: "content_block_stop", Data: json.RawMessage(`{"index":0}`)},
		{Event: "message_delta", Data: json.RawMessage(`{"delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`)},
	}
	for _, frame := range frames {
		if err := transcoder.HandleFrame(frame); err != nil {
			t.Fatalf("HandleFrame(%s) error = %v", frame.Event, err)
		}
	}

	body := rr.Body.String()
	if count := strings.Count(body, `"namespace":"alpha"`); count != 3 {
		t.Fatalf("namespace appears %d times, want added/done/completed events: %s", count, body)
	}
	if strings.Contains(body, `"name":"ns_alpha_same"`) || !strings.Contains(body, `"name":"same_name"`) {
		t.Fatalf("stream did not restore original tool identity: %s", body)
	}
}
