// Package ollama holds what the plugin knows about the Ollama cloud API
// itself: endpoints, error mapping, egress (proxies and HTTP clients) and the
// balance and usage data it reports.
package ollama

import "strings"

// ProviderID is the CPA provider key and the auth file type.
const ProviderID = "ollama"

// Inference endpoints. Ollama serves the OpenAI-compatible API under
// https://ollama.com/v1 and the Anthropic-compatible Messages API at
// https://ollama.com/v1/messages (the Anthropic client base URL is the
// origin itself), so all three share one origin.
const (
	EndpointChat      = "/v1/chat/completions"
	EndpointResponses = "/v1/responses"
	EndpointMessages  = "/v1/messages"
)

// Native Ollama endpoints the plugin reads.
const (
	// EndpointTags lists the cloud models; it needs no API key.
	EndpointTags = "/api/tags"
	// EndpointShow describes one model (context length, parameters,
	// thinking levels); it needs no API key.
	EndpointShow = "/api/show"
	// EndpointBalance reports the account's included and purchased credits.
	EndpointBalance = "/api/balance"
	// EndpointUsage reports the account's usage over 24h, 7d or 30d.
	EndpointUsage = "/api/usage"
)

// EndpointForFormat maps a CPA executor format to the Ollama endpoint.
func EndpointForFormat(format string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "openai", "chat-completions", "chat_completions", "openai-chat-completions":
		return EndpointChat, true
	case "openai-response", "openai-responses", "responses":
		return EndpointResponses, true
	case "claude", "anthropic":
		return EndpointMessages, true
	default:
		return "", false
	}
}
