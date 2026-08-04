package proxy

import (
	"regexp"
	"strings"
)

// noToolSentinelNames are pseudo-tools emitted by models to indicate that no
// client tool should run. A declared client tool with the same name remains a
// real tool; __done__ is always reserved by the bridge.
var noToolSentinelNames = map[string]struct{}{
	"done":     {},
	"__done__": {},
	"none":     {},
	"null":     {},
	"no_tool":  {},
	"noop":     {},
	"skip":     {},
}

func isNoToolSentinelName(name string) bool {
	_, ok := noToolSentinelNames[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func shouldInterceptNoToolSentinel(name string, allowedToolNames map[string]struct{}) bool {
	name = strings.TrimSpace(name)
	if strings.EqualFold(name, "__done__") {
		return true
	}
	if !isNoToolSentinelName(name) {
		return false
	}
	_, declared := allowedToolNames[name]
	if declared {
		return false
	}
	for allowed := range allowedToolNames {
		if strings.EqualFold(allowed, name) {
			return false
		}
	}
	return true
}

var rawDoneJSONRegex = regexp.MustCompile(`(?s)\{\s*"name"\s*:\s*"(?:__done__|done|none|null|no_tool|noop|skip)"\s*,\s*"arguments"\s*:\s*(?:\{[^}]*\}|"[^"]*")\s*\}`)

func stripRawDoneJSONLeakage(text string) string {
	return strings.TrimSpace(rawDoneJSONRegex.ReplaceAllString(text, ""))
}

var incompleteToolCallJSONRegex = regexp.MustCompile(`(?s)\{\s*"name"\s*:\s*".*`)
var incompleteToolCallWrapperJSONRegex = regexp.MustCompile(`(?s)\{\s*"tool_call"\s*:\s*.*`)

func stripIncompleteToolCallJSON(text string) string {
	for _, re := range []*regexp.Regexp{incompleteToolCallJSONRegex, incompleteToolCallWrapperJSONRegex} {
		loc := re.FindStringIndex(text)
		if loc == nil {
			continue
		}
		if !isBraceBalanced(text[loc[0]:]) {
			text = strings.TrimSpace(text[:loc[0]])
		}
	}
	return text
}

func isBraceBalanced(text string) bool {
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

func normalizeToolBridgeResidualText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimSpace(structuredOutputLeadingTagRegex.ReplaceAllString(trimmed, ""))
	trimmed = stripRawDoneJSONLeakage(trimmed)
	trimmed = stripIncompleteToolCallJSON(trimmed)
	return strings.TrimSpace(trimmed)
}

func detectToolBridgeNoToolResponse(text string) bool {
	normalized := normalizeToolBridgeResidualText(text)
	if normalized == "" {
		return false
	}
	lower := strings.ToLower(normalized)

	mentionsNotionIdentity := strings.Contains(lower, "notion ai") ||
		strings.Contains(lower, "notion workspace") ||
		strings.Contains(lower, "notion/api") ||
		strings.Contains(lower, "notion api") ||
		strings.Contains(normalized, "\u6211\u662f Notion AI") ||
		strings.Contains(normalized, "\u6211\u662fnotion ai")
	mentionsLocalFS := strings.Contains(normalized, "\u672c\u5730\u6587\u4ef6\u7cfb\u7edf") ||
		strings.Contains(lower, "local file system") ||
		strings.Contains(lower, "local filesystem") ||
		(strings.Contains(lower, "local ") && strings.Contains(lower, "filesystem"))
	mentionsCodingAssistant := strings.Contains(normalized, "\u7f16\u7801\u52a9\u624b") ||
		strings.Contains(normalized, "Claude Code") ||
		strings.Contains(normalized, "Cursor") ||
		strings.Contains(lower, "coding assistant") ||
		strings.Contains(lower, "cli agent") ||
		strings.Contains(lower, "cli tool") ||
		strings.Contains(lower, "opencode cli") ||
		(strings.Contains(lower, "opencode") && strings.Contains(lower, "cli"))
	mentionsManualHandOff := strings.Contains(normalized, "\u590d\u5236\u7c98\u8d34") ||
		strings.Contains(normalized, "\u624b\u52a8\u6dfb\u52a0") ||
		strings.Contains(normalized, "\u4f60\u53ef\u4ee5\u8fd9\u6837\u505a") ||
		strings.Contains(lower, "copy and paste") ||
		strings.Contains(lower, "manually add") ||
		strings.Contains(lower, "paste the relevant content") ||
		strings.Contains(lower, "paste the content")
	mentionsToolRefusal := strings.Contains(lower, "won't emit") ||
		strings.Contains(lower, "won't fabricate") ||
		strings.Contains(lower, "can't execute") ||
		strings.Contains(lower, "cannot execute") ||
		(strings.Contains(lower, "don't have") && strings.Contains(lower, "tools")) ||
		strings.Contains(lower, "i can't actually") ||
		strings.Contains(lower, "i won't") ||
		(strings.Contains(lower, "not going to") && (strings.Contains(lower, "emit") || strings.Contains(lower, "fabricat"))) ||
		strings.Contains(lower, "aren't functions") ||
		strings.Contains(lower, "aren't tools") ||
		strings.Contains(lower, "isn't a function") ||
		strings.Contains(lower, "isn't a tool") ||
		strings.Contains(lower, "can't drive") ||
		strings.Contains(lower, "cannot drive") ||
		strings.Contains(lower, "shouldn't pretend") ||
		strings.Contains(lower, "should not pretend") ||
		strings.Contains(lower, "fabricating") ||
		strings.Contains(lower, "no real task") ||
		strings.Contains(lower, "no real codebase")
	mentionsMissingLocalTools := strings.Contains(lower, "read") &&
		strings.Contains(lower, "edit") &&
		strings.Contains(lower, "bash")
	mentionsMissingLocalToolsPartial := (strings.Contains(lower, "bash") && strings.Contains(lower, "edit")) ||
		(strings.Contains(lower, "bash") && strings.Contains(lower, "read")) ||
		(strings.Contains(lower, "edit") && strings.Contains(lower, "read")) ||
		(strings.Contains(lower, "filesystem") && strings.Contains(lower, "bash")) ||
		(strings.Contains(lower, "powershell") && strings.Contains(lower, "filesystem"))
	mentionsFakeShell := strings.Contains(lower, "isn't a shell command") ||
		strings.Contains(lower, "is not a shell command") ||
		strings.Contains(lower, "not recognized as a name") ||
		strings.Contains(lower, "not recognised as a name") ||
		strings.Contains(lower, "nothing further to execute") ||
		strings.Contains(lower, "nothing further to do") ||
		strings.Contains(lower, "nothing more to execute") ||
		strings.Contains(lower, "nothing i can execute") ||
		strings.Contains(lower, "nothing i can do") ||
		strings.Contains(lower, "can't do that") ||
		strings.Contains(lower, "cannot do that")

	return (mentionsNotionIdentity && mentionsLocalFS) ||
		(mentionsLocalFS && mentionsCodingAssistant) ||
		(mentionsNotionIdentity && mentionsCodingAssistant) ||
		(mentionsMissingLocalTools && mentionsCodingAssistant && mentionsManualHandOff) ||
		(mentionsNotionIdentity && mentionsToolRefusal) ||
		(mentionsToolRefusal && mentionsMissingLocalTools) ||
		(mentionsToolRefusal && mentionsMissingLocalToolsPartial) ||
		(mentionsLocalFS && mentionsToolRefusal) ||
		mentionsFakeShell
}
