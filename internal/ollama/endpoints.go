// Package ollama is the Ollama adapter of cpa-provider-core: what the plugin
// knows about the Ollama cloud API itself — endpoints, the model catalog and
// the balance and usage data it reports.
package ollama

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
