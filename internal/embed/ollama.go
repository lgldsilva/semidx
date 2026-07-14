package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// defaultEmbedTimeout bounds a single embedding HTTP call. It is generous
// because the provider may be a wake-on-demand proxy (e.g. llamacpp-proxy) whose
// first call after idle pays a container cold-start of tens of seconds; a 30s
// ceiling tripped the circuit breaker on every cold host. Override per-deploy
// with SEMIDX_EMBED_TIMEOUT.
const defaultEmbedTimeout = 90 * time.Second

// embedTimeout resolves the embedding HTTP client timeout, honoring
// SEMIDX_EMBED_TIMEOUT (a Go duration like "90s" or a bare seconds count).
// Invalid or non-positive values fall back to the default.
func embedTimeout() time.Duration {
	v := strings.TrimSpace(os.Getenv("SEMIDX_EMBED_TIMEOUT"))
	if v == "" {
		return defaultEmbedTimeout
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return defaultEmbedTimeout
}

// OllamaClient talks to a local Ollama server's native embedding API.
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

// NewOllamaClient returns a client for the Ollama server at baseURL.
func NewOllamaClient(baseURL string) *OllamaClient {
	return &OllamaClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: embedTimeout(),
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama show failed: %s - %s", resp.Status, string(body))
	}

	var info modelInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	dims := embeddingDimsFromShow(info.ModelInfo)
	if dims <= 0 {
		dims = InferDims(model)
	}
	if dims <= 0 {
		// Never guess a default: a wrong dims value silently creates a mismatched
		// chunk table. Erroring here lets ChainEmbedder.tryEach fall through to
		// the next provider instead.
		return nil, &UnknownModelError{
			Provider: "ollama", Model: model,
			Reason: "no *.embedding_length in /api/show and not in the known-model catalog",
		}
	}

	return &ModelInfo{Name: model, Dims: dims}, nil
}

// embeddingDimsFromShow extracts the embedding dimension from an /api/show
// model_info map, whose key is architecture-prefixed (e.g.
// "bert.embedding_length"). Returns 0 when no positive value is present.
func embeddingDimsFromShow(modelInfo map[string]interface{}) int {
	for key, val := range modelInfo {
		if !strings.HasSuffix(key, ".embedding_length") {
			continue
		}
		if f, ok := val.(float64); ok && f > 0 {
			return int(f)
		}
	}
	return 0
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
	defer func() { _ = resp.Body.Close() }()

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
	defer func() { _ = resp.Body.Close() }()

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
