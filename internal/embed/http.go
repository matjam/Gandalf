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

// HTTP embeds text through an OpenAI-compatible embeddings endpoint.
//
// One adapter covers Ollama, llama.cpp's server, LM Studio, and a homelab box,
// because they all speak the same shape. Swapping model or machine is then a
// configuration change rather than a code change.
type HTTP struct {
	// BaseURL is the API root, such as http://localhost:11434/v1.
	BaseURL string

	// ModelName is the embedding model to request.
	ModelName string

	// Dimensions is the vector length the model returns. It is checked against
	// the first response, so a misconfiguration is caught at once rather than
	// producing an index that silently cannot be searched.
	Dimensions int

	// Tokens is the model's context window. It cannot be discovered over this
	// API, so it is configured; too small merely costs extra chunks, while too
	// large loses text to silent truncation at the far end.
	Tokens int

	// APIKey is sent as a bearer token when set. Local endpoints rarely need
	// one.
	APIKey string

	// Client defaults to a client with a sane timeout.
	Client *http.Client
}

// Model identifies the endpoint's model.
func (h HTTP) Model() string { return h.ModelName }

// Dims is the vector length.
func (h HTTP) Dims() int { return h.Dimensions }

// Window is the model's context window in tokens.
func (h HTTP) Window() int {
	if h.Tokens > 0 {
		return h.Tokens
	}
	return 512
}

// embeddingRequest is the OpenAI-compatible request body.
type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embeddingResponse is the part of the response we use.
type embeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Embed returns one vector per input.
func (h HTTP) Embed(ctx context.Context, texts []string) ([]Vector, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	body, err := json.Marshal(embeddingRequest{Model: h.ModelName, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}

	url := strings.TrimSuffix(h.BaseURL, "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if h.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.APIKey)
	}

	res, err := h.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf(
			"embedding endpoint %s is unreachable: %w. Start the model, point -embed-url elsewhere, "+
				"or run with -embed none to work without search", url, err)
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}

	var decoded embeddingResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("decode embedding response (status %d): %w", res.StatusCode, err)
	}
	switch {
	case decoded.Error != nil:
		return nil, fmt.Errorf("embedding endpoint: %s", decoded.Error.Message)
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("embedding endpoint returned %s", res.Status)
	case len(decoded.Data) != len(texts):
		return nil, fmt.Errorf("asked for %d embeddings, got %d", len(texts), len(decoded.Data))
	}

	out := make([]Vector, len(texts))
	for _, d := range decoded.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("embedding endpoint returned index %d, out of range", d.Index)
		}
		if h.Dimensions > 0 && len(d.Embedding) != h.Dimensions {
			return nil, fmt.Errorf(
				"model %q returned %d dimensions, configured for %d; fix the configuration and reindex",
				h.ModelName, len(d.Embedding), h.Dimensions)
		}
		out[d.Index] = Vector(d.Embedding)
	}

	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("embedding endpoint returned nothing for input %d", i)
		}
	}

	return out, nil
}

// client returns the configured client, or one with a timeout that suits a
// local model doing real work.
func (h HTTP) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 2 * time.Minute}
}
