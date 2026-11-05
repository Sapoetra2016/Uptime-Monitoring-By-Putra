package ai

import "strings"

// Provider is a named, pre-configured OpenAI-compatible chat endpoint. The
// admin picks one in Settings → AI; the UI prefills BaseURL and Model so they
// only have to paste an API key. "custom" lets the admin type any endpoint.
type Provider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	BaseURL      string `json:"baseUrl"`
	DefaultModel string `json:"defaultModel"`
}

// Providers is the built-in catalog. Every entry exposes an OpenAI-compatible
// /chat/completions endpoint, so the existing Client works unchanged.
var Providers = []Provider{
	{ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1", DefaultModel: "gpt-4o-mini"},
	{ID: "xai", Name: "xAI (Grok)", BaseURL: "https://api.x.ai/v1", DefaultModel: "grok-2-latest"},
	{ID: "groq", Name: "Groq (Llama)", BaseURL: "https://api.groq.com/openai/v1", DefaultModel: "llama-3.3-70b-versatile"},
	{ID: "qwen", Name: "Qwen (DashScope)", BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", DefaultModel: "qwen-flash"},
	{ID: "gemini", Name: "Google Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", DefaultModel: "gemini-2.0-flash"},
	{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com/v1", DefaultModel: "deepseek-chat"},
	{ID: "mistral", Name: "Mistral", BaseURL: "https://api.mistral.ai/v1", DefaultModel: "mistral-small-latest"},
	{ID: "openrouter", Name: "OpenRouter (Meta Llama, dll.)", BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "meta-llama/llama-3.3-70b-instruct:free"},
	{ID: "custom", Name: "Custom (URL sendiri)", BaseURL: "", DefaultModel: ""},
}

// ProviderByID returns the preset with the given id, or nil.
func ProviderByID(id string) *Provider {
	for i := range Providers {
		if Providers[i].ID == id {
			return &Providers[i]
		}
	}
	return nil
}

// ResolveEndpoint picks the effective base URL + model for a set of AI
// settings. Explicit values win; otherwise the provider preset supplies them.
func ResolveEndpoint(providerID, baseURL, model string) (string, string) {
	if p := ProviderByID(providerID); p != nil {
		if strings.TrimSpace(baseURL) == "" {
			baseURL = p.BaseURL
		}
		if strings.TrimSpace(model) == "" {
			model = p.DefaultModel
		}
	}
	return strings.TrimSpace(baseURL), strings.TrimSpace(model)
}
