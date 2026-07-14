package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIClient implements Embedder for OpenAI-compatible APIs (OpenAI, Gemini's
// OpenAI-compat endpoint, OpenRouter, Ollama Cloud).
type OpenAIClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

type openAIEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// NewOpenAIClient returns a client for an OpenAI-compatible embedding API.
func NewOpenAIClient(baseURL, apiKey string) *OpenAIClient {
	return &OpenAIClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func (c *OpenAIClient) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("no inputs provided")
	}
	payload := openAIEmbedRequest{Model: model, Input: inputs}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai embed failed: %s - %s", resp.Status, string(b))
	}

	var result openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	embeddings := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		embeddings[i] = d.Embedding
	}
	return embeddings, nil
}

func (c *OpenAIClient) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	embeddings, err := c.Embed(ctx, model, text)
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return embeddings[0], nil
}

func (c *OpenAIClient) ModelInfo(ctx context.Context, model string) (*ModelInfo, error) {
	dims := InferDims(model)
	if dims <= 0 {
		// No metadata endpoint reports dims on OpenAI-compatible APIs; guessing a
		// value would create a mismatched chunk table, so fail and let the chain
		// try the next provider.
		return nil, &UnknownModelError{
			Provider: "openai", Model: model,
			Reason: "cannot determine dims; check the model name",
		}
	}
	return &ModelInfo{Name: model, Dims: dims}, nil
}

type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// staticEmbeddingModels is the ListModels fallback when the provider does not
// expose GET /models (or lists nothing embedding-like): a best-effort catalog
// of common models. It cannot reflect provider-specific offerings.
var staticEmbeddingModels = []string{
	"text-embedding-3-small", "text-embedding-3-large", "text-embedding-ada-002",
	"gemini-embedding-2", "gemini-embedding-001",
}

func (c *OpenAIClient) ListModels(ctx context.Context) ([]string, error) {
	if models := c.remoteEmbeddingModels(ctx); len(models) > 0 {
		return models, nil
	}
	return append([]string(nil), staticEmbeddingModels...), nil
}

// remoteEmbeddingModels queries GET {baseURL}/models (OpenAI list format) and
// keeps ids containing "embed". Returns nil on any error so the caller can
// fall back to the static list.
func (c *OpenAIClient) remoteEmbeddingModels(ctx context.Context) []string {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/models", nil)
	if err != nil {
		return nil
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var result openAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil
	}
	var models []string
	for _, m := range result.Data {
		if strings.Contains(strings.ToLower(m.ID), "embed") {
			models = append(models, m.ID)
		}
	}
	return models
}
