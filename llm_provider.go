package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// LLMProvider определяет интерфейс для работы с LLM провайдерами
type LLMProvider interface {
	Generate(ctx context.Context, req LLMRequest) (LLMResponse, error)
	Stream(ctx context.Context, req LLMRequest, onChunk func(string) error) error
}

var ErrLLMRateLimited = errors.New("LLM API rate limit or quota exceeded")

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
	baseURL       string
	apiKey        string
	model         string
	timeout       time.Duration
	client        *http.Client
	accessToken   string
	tokenExpiry   time.Time
	tokenMutex    sync.Mutex
}

func NewExternalProvider(baseURL, apiKey, model string, timeout time.Duration) LLMProvider {
	client := &http.Client{
		Timeout: timeout,
	}
	
	// Для GigaChat нужен специальный клиент с отключенной проверкой сертификата
	if strings.Contains(baseURL, "giga.chat") {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		log.Printf("GigaChat: using HTTP client with InsecureSkipVerify for SSL")
	}
	
	return &ExternalProvider{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		timeout: timeout,
		client:  client,
	}
}

// getGigaChatAccessToken получает Access Token для GigaChat OAuth
func (p *ExternalProvider) getGigaChatAccessToken(ctx context.Context) (string, error) {
	log.Printf("GigaChat OAuth: getGigaChatAccessToken called")
	p.tokenMutex.Lock()
	defer p.tokenMutex.Unlock()
	
	// Проверяем, есть ли валидный токен
	if p.accessToken != "" && time.Now().Before(p.tokenExpiry) {
		return p.accessToken, nil
	}
	
	// Получаем новый токен
	authURL := "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"
	
	// Формируем body в правильном формате OAuth 2.0
	formData := "scope=GIGACHAT_API_PERS&grant_type=client_credentials"
	
	authReq, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL, strings.NewReader(formData))
	if err != nil {
		return "", fmt.Errorf("failed to create GigaChat auth request: %w", err)
	}
	
	authReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	authReq.Header.Set("Accept", "application/json")
	// Используем UUID формат для RqUID как требует документация
	rquid := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", 
		time.Now().Unix(), 
		time.Now().UnixNano()&0xFFFF,
		(time.Now().UnixNano()>>16)&0xFFFF,
		(time.Now().UnixNano()>>32)&0xFFFF,
		time.Now().UnixNano()&0xFFFFFFFFFFFF)
	authReq.Header.Set("RqUID", rquid)
	authReq.Header.Set("Authorization", "Basic "+p.apiKey)
	
	log.Printf("GigaChat OAuth: requesting access token from %s", authURL)
	log.Printf("GigaChat OAuth: body='%s'", formData)
	log.Printf("GigaChat OAuth: headers - Authorization: Basic %s..., RqUID: %s", p.apiKey[:20], rquid)
	
	// Создаём отдельный клиент для OAuth с отключенной проверкой сертификата
	oauthClient := &http.Client{
		Timeout: p.timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	
	resp, err := oauthClient.Do(authReq)
	if err != nil {
		log.Printf("GigaChat OAuth HTTP error: %v", err)
		return "", fmt.Errorf("GigaChat auth request failed: %w", err)
	}
	defer resp.Body.Close()
	
	log.Printf("GigaChat OAuth response: status=%d, content-length=%d, headers=%v", 
		resp.StatusCode, resp.ContentLength, resp.Header)
	
	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		log.Printf("GigaChat OAuth: failed to read body: %v", readErr)
	}
	log.Printf("GigaChat OAuth: read %d bytes from body", len(bodyBytes))
	
	if resp.StatusCode != http.StatusOK {
		log.Printf("GigaChat OAuth failed: status=%d, body=%s", resp.StatusCode, string(bodyBytes))
		return "", fmt.Errorf("GigaChat auth failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}
	
	var authResp struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	
	if err := json.Unmarshal(bodyBytes, &authResp); err != nil {
		log.Printf("GigaChat OAuth response decode failed: %s", string(bodyBytes))
		return "", fmt.Errorf("failed to decode GigaChat auth response: %w", err)
	}
	
	p.accessToken = authResp.AccessToken
	p.tokenExpiry = time.Unix(authResp.ExpiresAt/1000, 0) // ExpiresAt в миллисекундах
	
	log.Printf("GigaChat: obtained new access token, expires at %s", p.tokenExpiry.Format("2006-01-02 15:04:05"))
	
	return p.accessToken, nil
}

func (p *ExternalProvider) doRequest(ctx context.Context, endpoint string, body []byte, stream bool) (*http.Response, error) {
	const maxAttempts = 3
	requestCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Для Google Gemini API добавляем ключ как URL параметр
		requestURL := endpoint
		if strings.HasPrefix(p.apiKey, "AQ.") || strings.Contains(p.baseURL, "generativelanguage.googleapis.com") {
			separator := "?"
			if strings.Contains(endpoint, "?") {
				separator = "&"
			}
			requestURL = endpoint + separator + "key=" + p.apiKey
		}
		
		httpReq, err := http.NewRequestWithContext(requestCtx, http.MethodPost, requestURL, strings.NewReader(string(body)))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		
		// Для GigaChat получаем Access Token
		if strings.Contains(p.baseURL, "giga.chat") {
			accessToken, err := p.getGigaChatAccessToken(requestCtx)
			if err != nil {
				// Если OAuth не работает, пробуем использовать apiKey напрямую как Bearer
				log.Printf("GigaChat OAuth failed (%v), trying direct Bearer token with base64 key", err)
				httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
			} else {
				httpReq.Header.Set("Authorization", "Bearer "+accessToken)
			}
		} else if !strings.HasPrefix(p.apiKey, "AQ.") && !strings.Contains(p.baseURL, "generativelanguage.googleapis.com") {
			// Для не-Gemini и не-GigaChat API используем Authorization header
			httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
		}
		
		if stream {
			httpReq.Header.Set("Accept", "text/event-stream")
		}

		resp, err := p.client.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("external API request failed: %w", err)
		}
		if resp.StatusCode != http.StatusServiceUnavailable || attempt == maxAttempts {
			return resp, nil
		}

		resp.Body.Close()
		delay := time.Duration(attempt) * time.Second
		log.Printf("LLM API returned HTTP %d; retrying in %s (attempt %d/%d)", resp.StatusCode, delay, attempt+1, maxAttempts)
		timer := time.NewTimer(delay)
		select {
		case <-requestCtx.Done():
			timer.Stop()
			return nil, fmt.Errorf("external API retry canceled: %w", requestCtx.Err())
		case <-timer.C:
		}
	}

	return nil, fmt.Errorf("external API request failed after retries")
}

type openAIChatRequest struct {
	Model           string              `json:"model"`
	Messages        []openAIChatMessage `json:"messages"`
	Temperature     float64             `json:"temperature"`
	MaxTokens       int                 `json:"max_tokens,omitempty"`
	ReasoningEffort string              `json:"reasoning_effort,omitempty"`
	Stream          bool                `json:"stream"`
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

// Gemini API native structures
type geminiGenerateRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
	Role  string       `json:"role,omitempty"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
}

type geminiGenerateResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}




func (p *ExternalProvider) Generate(ctx context.Context, req LLMRequest) (LLMResponse, error) {
	startTime := time.Now()
	
	// Проверяем, используем ли мы Google Gemini API
	isGemini := strings.HasPrefix(p.apiKey, "AQ.") || strings.Contains(p.baseURL, "generativelanguage.googleapis.com")
	
	var endpoint string
	var body []byte
	var err error
	
	if isGemini {
		// Нативный Google Gemini API
		// Убираем /openai из базового URL, если он есть
		baseURL := strings.TrimSuffix(strings.TrimSuffix(p.baseURL, "/"), "/openai")
		
		// Убираем префикс "models/" из названия модели, если он есть
		modelName := strings.TrimPrefix(p.model, "models/")
		
		endpoint = fmt.Sprintf("%s/models/%s:generateContent", baseURL, modelName)
		
		geminiReq := geminiGenerateRequest{
			Contents: []geminiContent{
				{
					Parts: []geminiPart{
						{Text: req.SystemPrompt + "\n\n" + req.UserPrompt},
					},
				},
			},
			GenerationConfig: geminiGenerationConfig{
				Temperature:     req.Temperature,
				MaxOutputTokens: req.MaxTokens,
			},
		}
		
		body, err = json.Marshal(geminiReq)
	} else {
		// OpenAI-compatible API
		temperature, reasoningEffort := externalModelSettings(p.model, req.Temperature)
		endpoint = p.baseURL + "/chat/completions"
		
		messages := []openAIChatMessage{
			{Role: "system", Content: req.SystemPrompt},
			{Role: "user", Content: req.UserPrompt},
		}
		
		openAIReq := openAIChatRequest{
			Model:           p.model,
			Messages:        messages,
			Temperature:     temperature,
			MaxTokens:       req.MaxTokens,
			ReasoningEffort: reasoningEffort,
			Stream:          false,
		}
		
		body, err = json.Marshal(openAIReq)
	}
	
	if err != nil {
		return LLMResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}
	
	log.Printf("LLM REQUEST: provider=external model=%s endpoint=%s isGemini=%v", p.model, endpoint, isGemini)
	
	resp, err := p.doRequest(ctx, endpoint, body, false)
	if err != nil {
		duration := time.Since(startTime)
		log.Printf("LLM REQUEST FAILED: provider=external model=%s duration=%v error=%v", p.model, duration, err)
		return LLMResponse{}, err
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
			return LLMResponse{}, fmt.Errorf("%w: %s", ErrLLMRateLimited, strings.TrimSpace(string(bodyBytes)))
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
			return LLMResponse{}, fmt.Errorf("external API error: status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
		default:
			return LLMResponse{}, fmt.Errorf("external API error: status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
		}
	}
	
	var content string
	var totalTokens int
	
	if isGemini {
		var geminiResp geminiGenerateResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&geminiResp)
		if decodeErr != nil {
			log.Printf("LLM RESPONSE PARSE FAILED: provider=external model=%s duration=%v error=%v", p.model, duration, decodeErr)
			return LLMResponse{}, fmt.Errorf("failed to parse Gemini response: %w", decodeErr)
		}
		
		if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
			log.Printf("LLM RESPONSE EMPTY: provider=external model=%s duration=%v", p.model, duration)
			return LLMResponse{}, fmt.Errorf("no content in Gemini response")
		}
		
		content = geminiResp.Candidates[0].Content.Parts[0].Text
		totalTokens = geminiResp.UsageMetadata.TotalTokenCount
	} else {
		var chatResp openAIChatResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&chatResp)
		if isInterruptedResponse(decodeErr) && ctx.Err() == nil {
			log.Printf("LLM response interrupted; retrying once: provider=external model=%s error=%v", p.model, decodeErr)
			resp.Body.Close()

			retryResp, retryErr := p.doRequest(ctx, endpoint, body, false)
			if retryErr != nil {
				return LLMResponse{}, retryErr
			}
			defer retryResp.Body.Close()
			if retryResp.StatusCode != http.StatusOK {
				bodyBytes, _ := io.ReadAll(retryResp.Body)
				return LLMResponse{}, fmt.Errorf("external API error: status %d: %s", retryResp.StatusCode, strings.TrimSpace(string(bodyBytes)))
			}

			decodeErr = json.NewDecoder(retryResp.Body).Decode(&chatResp)
		}
		if decodeErr != nil {
			log.Printf("LLM RESPONSE PARSE FAILED: provider=external model=%s duration=%v error=%v", p.model, duration, decodeErr)
			return LLMResponse{}, fmt.Errorf("failed to parse response: %w", decodeErr)
		}
		
		if len(chatResp.Choices) == 0 {
			log.Printf("LLM RESPONSE EMPTY: provider=external model=%s duration=%v", p.model, duration)
			return LLMResponse{}, fmt.Errorf("no choices in response")
		}
		
		content = chatResp.Choices[0].Message.Content
		totalTokens = chatResp.Usage.TotalTokens
	}
	
	log.Printf("LLM REQUEST COMPLETED: provider=external model=%s duration=%v tokens=%d length=%d", 
		p.model, duration, totalTokens, len(content))
	
	return LLMResponse{Content: content}, nil
}

func isInterruptedResponse(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func (p *ExternalProvider) Stream(ctx context.Context, req LLMRequest, onChunk func(string) error) error {
	startTime := time.Now()
	temperature, reasoningEffort := externalModelSettings(p.model, req.Temperature)
	
	messages := []openAIChatMessage{
		{Role: "system", Content: req.SystemPrompt},
		{Role: "user", Content: req.UserPrompt},
	}
	
	payload := openAIChatRequest{
		Model:           p.model,
		Messages:        messages,
		Temperature:     temperature,
		MaxTokens:       req.MaxTokens,
		ReasoningEffort: reasoningEffort,
		Stream:          true,
	}
	
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	
	endpoint := p.baseURL + "/chat/completions"
	log.Printf("LLM STREAM REQUEST: provider=external model=%s endpoint=%s", p.model, endpoint)
	
	resp, err := p.doRequest(ctx, endpoint, body, true)
	if err != nil {
		duration := time.Since(startTime)
		log.Printf("LLM STREAM FAILED: provider=external model=%s duration=%v error=%v", p.model, duration, err)
		return err
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
			return fmt.Errorf("%w: %s", ErrLLMRateLimited, strings.TrimSpace(string(bodyBytes)))
		default:
			return fmt.Errorf("external API error: status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
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

func externalModelSettings(model string, requestedTemperature float64) (float64, string) {
	if strings.HasPrefix(strings.ToLower(model), "gemini-3") {
		return 1.0, "low"
	}
	return requestedTemperature, ""
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
