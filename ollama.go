package main

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

type OllamaClient struct {
	baseURL string
	client  *http.Client
}

type modelInfoResponse struct {
	Details struct {
		ParameterSize string `json:"parameter_size"`
		Family        string `json:"family"`
	} `json:"details"`
	ModelInfo map[string]interface{} `json:"model_info"`
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

type tagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

func NewOllamaClient(baseURL string) *OllamaClient {
	return &OllamaClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

func (c *OllamaClient) ModelInfo(ctx context.Context, model string) (*ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/show", strings.NewReader(fmt.Sprintf(`{"name":%q}`, model)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama show failed: %s - %s", resp.Status, string(body))
	}

	var info modelInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	dims := 1024 // default for most models
	switch {
	case strings.Contains(model, "nomic-embed-text"):
		dims = 768
	case strings.Contains(model, "bge-m3"), strings.Contains(model, "mxbai-embed-large"), strings.Contains(model, "qwen3-embedding:0.6b"):
		dims = 1024
	}

	return &ModelInfo{
		Name: model,
		Dims: dims,
	}, nil
}

func (c *OllamaClient) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("no inputs provided")
	}

	payload := embedRequest{Model: model, Input: inputs}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed failed: %s - %s", resp.Status, string(b))
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Embeddings, nil
}

func (c *OllamaClient) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	embeddings, err := c.Embed(ctx, model, text)
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return embeddings[0], nil
}

func (c *OllamaClient) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama tags failed: %s", resp.Status)
	}

	var tags tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}

	var models []string
	embeddingKeywords := []string{"embed", "bge", "nomic", "mxbai"}
	for _, m := range tags.Models {
		name := strings.ToLower(m.Name)
		for _, kw := range embeddingKeywords {
			if strings.Contains(name, kw) {
				models = append(models, m.Name)
				break
			}
		}
	}
	return models, nil
}


