package proxy

import (
	"crypto/sha256"
	"encoding/hex"
)

// extractAnthropicSessionSalt is retained for compatibility with the upstream
// bridge tests. The current session manager uses the more general helper.
func extractAnthropicSessionSalt(metadata map[string]interface{}) string {
	return extractConversationSalt(metadata)
}

// computeSessionFingerprintWithSalt is the legacy first-turn fingerprint used
// by upstream callers. New continuation routing uses full canonical message
// keys, but keeping this helper makes stable metadata behavior explicit.
func computeSessionFingerprintWithSalt(messages []ChatMessage, stableSalt string) string {
	h := sha256.New()
	if stableSalt != "" {
		h.Write([]byte("salt:"))
		h.Write([]byte(stableSalt))
		h.Write([]byte{'\n'})
	}
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		content := normalizeSessionSystemContent(message.Content)
		if len(content) > 200 {
			content = content[:200]
		}
		h.Write([]byte(content))
		break
	}
	for _, message := range messages {
		if !isMeaningfulUserMessage(message) {
			continue
		}
		content := normalizeSessionUserContent(message.Content)
		if len(content) > 200 {
			content = content[:200]
		}
		h.Write([]byte(content))
		break
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
