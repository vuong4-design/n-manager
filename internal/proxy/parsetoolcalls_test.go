package proxy

import (
	"strings"
	"testing"
)

func TestParseToolCalls_PrettyPrintedEmbeddedJSON(t *testing.T) {
	// Model emits prose preamble + a pretty-printed (multi-line) tool-call
	// envelope. Methods 1-1.5 (XML/fence) and 3 (line-by-line) all miss this
	// because no single line is complete JSON and the whole content isn't pure
	// JSON. The balanced-brace extractor (Method 1.7) must catch it.
	content := "I'll edit the file now.\n" +
		"{\n" +
		"  \"name\": \"edit\",\n" +
		"  \"arguments\": {\n" +
		"    \"filePath\": \"C:\\\\Users\\\\Admin\\\\.cursor\\\\rules.mdc\",\n" +
		"    \"oldString\": \"old\",\n" +
		"    \"newString\": \"new\"\n" +
		"  }\n" +
		"}\n"

	calls, remaining, hasCalls := parseToolCalls(content)
	if !hasCalls || len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got hasCalls=%v len=%d", hasCalls, len(calls))
	}
	if calls[0].Function.Name != "edit" {
		t.Errorf("expected name=edit, got %q", calls[0].Function.Name)
	}
	if remaining != "" && strings.Contains(remaining, "\"name\"") {
		t.Errorf("remaining still contains the JSON envelope: %q", remaining)
	}
}

func TestParseToolCalls_SingleLineEmbeddedJSON(t *testing.T) {
	content := "Sure, here is the edit.\n" +
		`{"name": "edit", "arguments": {"filePath": "/tmp/a.txt", "oldString": "x", "newString": "y"}}` + "\n"

	calls, remaining, hasCalls := parseToolCalls(content)
	if !hasCalls || len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got hasCalls=%v len=%d", hasCalls, len(calls))
	}
	if calls[0].Function.Name != "edit" {
		t.Errorf("expected name=edit, got %q", calls[0].Function.Name)
	}
	if strings.Contains(remaining, "\"name\"") {
		t.Errorf("remaining should not contain tool envelope: %q", remaining)
	}
}

func TestParseToolCalls_PureJSON(t *testing.T) {
	content := `{"name": "bash", "arguments": {"command": "ls"}}`
	calls, _, hasCalls := parseToolCalls(content)
	if !hasCalls || len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got hasCalls=%v len=%d", hasCalls, len(calls))
	}
	if calls[0].Function.Name != "bash" {
		t.Errorf("expected name=bash, got %q", calls[0].Function.Name)
	}
}

func TestParseToolCalls_MultipleEmbeddedJSON(t *testing.T) {
	content := "Running two tools.\n" +
		"{\n  \"name\": \"bash\",\n  \"arguments\": {\"command\": \"pwd\"}\n}\n" +
		"{\n  \"name\": \"read\",\n  \"arguments\": {\"filePath\": \"/tmp/x\"}\n}\n"

	calls, _, hasCalls := parseToolCalls(content)
	if !hasCalls || len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got hasCalls=%v len=%d", hasCalls, len(calls))
	}
	if calls[0].Function.Name != "bash" {
		t.Errorf("call[0] name=%q, want bash", calls[0].Function.Name)
	}
	if calls[1].Function.Name != "read" {
		t.Errorf("call[1] name=%q, want read", calls[1].Function.Name)
	}
}

func TestParseToolCalls_BracesInStringLiteral(t *testing.T) {
	// Ensure braces inside string values don't fool the balanced-brace scanner.
	content := `{"name": "write", "arguments": {"filePath": "/x", "content": "a { b } c { d }"}}`
	calls, _, hasCalls := parseToolCalls(content)
	if !hasCalls || len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got hasCalls=%v len=%d", hasCalls, len(calls))
	}
	if calls[0].Function.Name != "write" {
		t.Errorf("expected name=write, got %q", calls[0].Function.Name)
	}
}

func TestParseToolCalls_NoToolCall(t *testing.T) {
	content := "This is just a normal answer with no tool call.\nHere is some prose."
	calls, remaining, hasCalls := parseToolCalls(content)
	if hasCalls || len(calls) != 0 {
		t.Fatalf("expected no tool calls, got hasCalls=%v len=%d", hasCalls, len(calls))
	}
	if remaining != content {
		t.Errorf("remaining should equal content when no calls found")
	}
}
