package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// LLMProvider определяет интерфейс для работы с LLM провайдерами
type LLMProvider interface {
	Generate(ctx context.Context, req LLMRequest) (LLMResponse, error)
	Stream(ctx context.Context, req LLMRequest, onChunk func(string) error) error
}

// LLMRequest содержит параметры запроса к LLM
type LLMRequest struct {
	SystemPrompt string
	UserPrompt   string
	Temperature  float64
	MaxTokens    int
}

// LLMResponse содержит ответ от LLM
type LLMResponse struct {
	Content string
}

// =========================================================
// External OpenAI-compatible Provider
// =========================================================

type ExternalProvider struct {
	baseURL string
	apiKey  string
	model   string
	timeout time.Duration
	client  *http.Client
}

func NewExternalProvider(baseURL, apiKey, model string, timeout time.Duration) LLMProvider {
	return &ExternalProvider{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		timeout: timeout,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

type openAIChatRequest struct {
	Model       string                   `json:"model"`
	Messages    []openAIChatMessage      `json:"messages"`
	Temperature float64                  `json:"temperature"`
	MaxTokens   int                      `json:"max_tokens,omitempty"`
	Stream      bool                     `json:"stream"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type openAIStreamChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}




func (p *ExternalProvider) Generate(ctx context.Context, req LLMRequest) (LLMResponse, error) {
	startTime := time.Now()
	
	messages := []openAIChatMessage{
		{Role: "system", Content: req.SystemPrompt},
		{Role: "user", Content: req.UserPrompt},
	}
	
	payload := openAIChatRequest{
		Model:       p.model,
		Messages:    messages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      false,
	}
	
	body, err := json.Marshal(payload)
	if err != nil {
		return LLMResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}
	
	endpoint := p.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(body)))
	if err != nil {
		return LLMResponse{}, fmt.Errorf("failed to create request: %w", err)
	}
	
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	
	log.Printf("LLM REQUEST: provider=external model=%s endpoint=%s", p.model, endpoint)
	
	resp, err := p.client.Do(httpReq)
	if err != nil {
		duration := time.Since(startTime)
		log.Printf("LLM REQUEST FAILED: provider=external model=%s duration=%v error=%v", p.model, duration, err)
		return LLMResponse{}, fmt.Errorf("external API request failed: %w", err)
	}
	defer resp.Body.Close()
	
	duration := time.Since(startTime)
	
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("LLM REQUEST FAILED: provider=external model=%s status=%d duration=%v body=%s", 
			p.model, resp.StatusCode, duration, string(bodyBytes))
		
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return LLMResponse{}, fmt.Errorf("authentication failed: invalid API key")
		case http.StatusForbidden:
			return LLMResponse{}, fmt.Errorf("access forbidden: check API key permissions")
		case http.StatusTooManyRequests:
			return LLMResponse{}, fmt.Errorf("rate limit exceeded")
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
			return LLMResponse{}, fmt.Errorf("external API error: status %d", resp.StatusCode)
		default:
			return LLMResponse{}, fmt.Errorf("external API error: status %d", resp.StatusCode)
		}
	}
	
	var chatResp openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		log.Printf("LLM RESPONSE PARSE FAILED: provider=external model=%s duration=%v error=%v", p.model, duration, err)
		return LLMResponse{}, fmt.Errorf("failed to parse response: %w", err)
	}
	
	if len(chatResp.Choices) == 0 {
		log.Printf("LLM RESPONSE EMPTY: provider=external model=%s duration=%v", p.model, duration)
		return LLMResponse{}, fmt.Errorf("no choices in response")
	}
	
	content := chatResp.Choices[0].Message.Content
	log.Printf("LLM REQUEST COMPLETED: provider=external model=%s duration=%v tokens=%d length=%d", 
		p.model, duration, chatResp.Usage.TotalTokens, len(content))
	
	return LLMResponse{Content: content}, nil
}

func (p *ExternalProvider) Stream(ctx context.Context, req LLMRequest, onChunk func(string) error) error {
	startTime := time.Now()
	
	messages := []openAIChatMessage{
		{Role: "system", Content: req.SystemPrompt},
		{Role: "user", Content: req.UserPrompt},
	}
	
	payload := openAIChatRequest{
		Model:       p.model,
		Messages:    messages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      true,
	}
	
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	
	endpoint := p.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")
	
	log.Printf("LLM STREAM REQUEST: provider=external model=%s endpoint=%s", p.model, endpoint)
	
	resp, err := p.client.Do(httpReq)
	if err != nil {
		duration := time.Since(startTime)
		log.Printf("LLM STREAM FAILED: provider=external model=%s duration=%v error=%v", p.model, duration, err)
		return fmt.Errorf("external API request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		duration := time.Since(startTime)
		log.Printf("LLM STREAM FAILED: provider=external model=%s status=%d duration=%v body=%s", 
			p.model, resp.StatusCode, duration, string(bodyBytes))
		
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return fmt.Errorf("authentication failed: invalid API key")
		case http.StatusForbidden:
			return fmt.Errorf("access forbidden: check API key permissions")
		case http.StatusTooManyRequests:
			return fmt.Errorf("rate limit exceeded")
		default:
			return fmt.Errorf("external API error: status %d", resp.StatusCode)
		}
	}
	
	// Parse SSE stream
	scanner := bufio.NewScanner(resp.Body)
	var totalChunks int
	
	for scanner.Scan() {
		line := scanner.Text()
		
		// SSE format: data: {...}
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			
			var chunk openAIStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				content := chunk.Choices[0].Delta.Content
				totalChunks++
				if err := onChunk(content); err != nil {
					return err
				}
			}
		}
	}
	
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stream read error: %w", err)
	}
	
	duration := time.Since(startTime)
	log.Printf("LLM STREAM COMPLETED: provider=external model=%s duration=%v chunks=%d", p.model, duration, totalChunks)
	
	return nil
}

// =========================================================
// Ollama Provider (wrapped legacy implementation)
// =========================================================

type OllamaProvider struct {
	url     string
	model   string
	timeout time.Duration
}

func NewOllamaProvider(url, model string, timeout time.Duration) LLMProvider {
	return &OllamaProvider{
		url:     url,
		model:   model,
		timeout: timeout,
	}
}

func (p *OllamaProvider) Generate(ctx context.Context, req LLMRequest) (LLMResponse, error) {
	startTime := time.Now()
	
	// Формируем unified prompt
	var promptBuilder strings.Builder
	promptBuilder.WriteString(req.UserPrompt)
	
	payload := OllamaGenerateRequest{
		Model:     p.model,
		Prompt:    promptBuilder.String(),
		System:    req.SystemPrompt,
		Stream:    false,
		KeepAlive: "10m",
		Options: map[string]any{
			"temperature":    req.Temperature,
			"top_p":          0.90,
			"repeat_penalty": 1.08,
			"num_ctx":        32768,
			"num_predict":    req.MaxTokens,
		},
	}
	
	body, err := json.Marshal(payload)
	if err != nil {
		return LLMResponse{}, err
	}
	
	modelCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	
	httpReq, err := http.NewRequestWithContext(modelCtx, "POST", p.url+"/api/generate", strings.NewReader(string(body)))
	if err != nil {
		return LLMResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	
	log.Printf("LLM REQUEST: provider=ollama model=%s url=%s", p.model, p.url)
	
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		duration := time.Since(startTime)
		log.Printf("LLM REQUEST FAILED: provider=ollama model=%s duration=%v error=%v", p.model, duration, err)
		return LLMResponse{}, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		duration := time.Since(startTime)
		log.Printf("LLM REQUEST FAILED: provider=ollama model=%s status=%d duration=%v body=%s", 
			p.model, resp.StatusCode, duration, string(bodyBytes))
		return LLMResponse{}, fmt.Errorf("ollama error: status %d", resp.StatusCode)
	}
	
	var ollamaResp OllamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return LLMResponse{}, err
	}
	
	answer := cleanLLMAnswer(ollamaResp.Response)
	answer = ensureCompleteAnswer(answer)
	
	duration := time.Since(startTime)
	log.Printf("LLM REQUEST COMPLETED: provider=ollama model=%s duration=%v length=%d", p.model, duration, len(answer))
	
	return LLMResponse{Content: answer}, nil
}

func (p *OllamaProvider) Stream(ctx context.Context, req LLMRequest, onChunk func(string) error) error {
	startTime := time.Now()
	
	var promptBuilder strings.Builder
	promptBuilder.WriteString(req.UserPrompt)
	
	payload := OllamaGenerateRequest{
		Model:     p.model,
		Prompt:    promptBuilder.String(),
		System:    req.SystemPrompt,
		Stream:    true,
		KeepAlive: "10m",
		Options: map[string]any{
			"temperature":    req.Temperature,
			"top_p":          0.90,
			"repeat_penalty": 1.08,
			"num_ctx":        32768,
			"num_predict":    req.MaxTokens,
		},
	}
	
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	
	modelCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	
	httpReq, err := http.NewRequestWithContext(modelCtx, "POST", p.url+"/api/generate", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	
	log.Printf("LLM STREAM REQUEST: provider=ollama model=%s url=%s", p.model, p.url)
	
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		duration := time.Since(startTime)
		log.Printf("LLM STREAM FAILED: provider=ollama model=%s duration=%v error=%v", p.model, duration, err)
		return fmt.Errorf("ollama stream failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		duration := time.Since(startTime)
		log.Printf("LLM STREAM FAILED: provider=ollama model=%s status=%d duration=%v body=%s", 
			p.model, resp.StatusCode, duration, string(bodyBytes))
		return fmt.Errorf("ollama error: status %d", resp.StatusCode)
	}
	
	decoder := json.NewDecoder(resp.Body)
	var totalChunks int
	
	for {
		var chunk OllamaGenerateResponse
		if err := decoder.Decode(&chunk); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		
		if chunk.Response != "" {
			totalChunks++
			if err := onChunk(chunk.Response); err != nil {
				return err
			}
		}
		
		if chunk.Done {
			break
		}
	}
	
	duration := time.Since(startTime)
	log.Printf("LLM STREAM COMPLETED: provider=ollama model=%s duration=%v chunks=%d", p.model, duration, totalChunks)
	
	return nil
}

// =========================================================
// Helper functions
// =========================================================

func cleanLLMAnswer(text string) string {
	text = strings.TrimSpace(text)
	
	// Удаляем пустые строки в начале и конце
	lines := strings.Split(text, "\n")
	start := 0
	end := len(lines)
	
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	
	if start < end {
		lines = lines[start:end]
	}
	
	return strings.Join(lines, "\n")
}

func ensureCompleteAnswer(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	
	// Проверяем, заканчивается ли текст знаком препинания
	lastChar := text[len(text)-1]
	if lastChar == '.' || lastChar == '!' || lastChar == '?' || lastChar == ':' {
		return text
	}
	
	// Если последнее предложение выглядит незавершенным, обрезаем до последнего полного
	lastPeriod := strings.LastIndex(text, ".")
	lastQuestion := strings.LastIndex(text, "?")
	lastExclamation := strings.LastIndex(text, "!")
	
	cutoff := lastPeriod
	if lastQuestion > cutoff {
		cutoff = lastQuestion
	}
	if lastExclamation > cutoff {
		cutoff = lastExclamation
	}
	
	if cutoff > len(text)*2/3 {
		return strings.TrimSpace(text[:cutoff+1])
	}
	
	return text
}

