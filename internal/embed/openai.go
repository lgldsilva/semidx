package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
		client:  &http.Client{Timeout: 5 * time.Minute},
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
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

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
	return &ModelInfo{Name: model, Dims: InferDims(model)}, nil
}

func (c *OpenAIClient) ListModels(ctx context.Context) ([]string, error) {
	return []string{"text-embedding-3-small", "text-embedding-3-large", "text-embedding-ada-002", "gemini-embedding-2", "gemini-embedding-001"}, nil
}
