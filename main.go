package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// =========================================================
// CONFIG
// =========================================================

type Config struct {
	Port        string
	CORSOrigin  string
	NOMOSBase   string
	CacheTime   time.Duration
	
	// LLM Configuration
	LLMProvider string        // "external" or "ollama"
	LLMTimeout  time.Duration
	
	// External Provider (OpenAI-compatible)
	LLMBaseURL string
	LLMAPIKey  string
	LLMModel   string
	
	// Ollama (legacy, optional)
	OllamaURL   string
	OllamaModel string
}

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, `"'`)

		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}

func getEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func loadConfig() Config {
	loadDotEnv(".env")

	// Determine timeout with default
	timeoutSec := 60
	if envTimeout := getEnv("LLM_TIMEOUT", ""); envTimeout != "" {
		if parsed, err := time.ParseDuration(envTimeout + "s"); err == nil {
			timeoutSec = int(parsed.Seconds())
		}
	}

	return Config{
		Port:        getEnv("PORT", "3000"),
		CORSOrigin:  getEnv("CORS_ORIGIN", "*"),
		NOMOSBase:   strings.TrimRight(getEnv("NOMOS_BASE_URL", "https://college-nomos.ru"), "/"),
		CacheTime:   10 * time.Minute,
		
		// LLM Provider configuration
		LLMProvider: getEnv("LLM_PROVIDER", "external"),
		LLMTimeout:  time.Duration(timeoutSec) * time.Second,
		
		// External provider (OpenAI-compatible)
		LLMBaseURL: strings.TrimRight(getEnv("LLM_BASE_URL", ""), "/"),
		LLMAPIKey:  getEnv("LLM_API_KEY", ""),
		LLMModel:   getEnv("LLM_MODEL", ""),
		
		// Ollama (fallback/legacy)
		OllamaURL:   strings.TrimRight(getEnv("OLLAMA_URL", "http://127.0.0.1:11434"), "/"),
		OllamaModel: getEnv("OLLAMA_MODEL", "llama3.1:8b"),
	}
}

// =========================================================
// HTTP TYPES
// =========================================================

type chatRequest struct {
	Message   string `json:"message"`
	Question  string `json:"question"`
	Query     string `json:"query"`
	SessionID string `json:"session_id,omitempty"` // ������������� ������ ��� ���������� ���������
}

type chatResponse struct {
	Reply     string   `json:"reply"`
	Sources   []Source `json:"sources,omitempty"`
	SessionID string   `json:"session_id,omitempty"` // ���������� session_id �������
}

type errorResponse struct {
	Error string `json:"error"`
}

// =========================================================
// SOURCES
// =========================================================

type Source struct {
	Name string
	URL  string
	Kind string
}

type CachedPage struct {
	Text           string
	Source         Source
	UpdatedAt      time.Time
	PublishedDate  string // ���� ���������� ��������� (���� ��������)
}

func buildSources(base string) []Source {
	base = strings.TrimRight(base, "/")

	return []Source{
		// �������� ��������
		{Name: "������� ��������", URL: base + "/", Kind: "general"},
		{Name: "�����������", URL: base + "/abitur/postupayushchim/", Kind: "admission"},
		{Name: "������������� (������)", URL: base + "/abitur/specialties/", Kind: "specialties"},
		
		// ����������� ���������� (����. �� ��������������� �����������)
		{Name: "�����������", URL: base + "/sveden/education/", Kind: "education"},
		{Name: "���������", URL: base + "/sveden/document/", Kind: "documents"},
		{Name: "������� ������", URL: base + "/sveden/paid_edu/", Kind: "payment"},
		{Name: "�����������-����������� �����������", URL: base + "/sveden/objects/", Kind: "facilities"},
		{Name: "��������� � ���������", URL: base + "/sveden/grants/", Kind: "grants"},
		{Name: "��������� �����", URL: base + "/sveden/vacant/", Kind: "admission"},
		
		// ���������� � �������������
		{Name: "������������� � �������� 1", URL: base + "/teachers/", Kind: "teachers"},
		{Name: "������������� � �������� 2", URL: base + "/teachers/?PAGEN_2=2", Kind: "teachers"},
		{Name: "���������� (�����������)", URL: base + "/sveden/employees/", Kind: "employees"},
		
		// ������������� (������)
		{Name: "����� � ����������� ����������� �����������", URL: base + "/abitur/specialties/40-02-01-pravo-i-organizatsiya-sotsialnogo-obespecheniya/", Kind: "specialty_detail"},
		{Name: "�������������", URL: base + "/abitur/specialties/40-02-04-yurisprudentsiya/", Kind: "specialty_detail"},
		{Name: "������������ � ��������� �������", URL: base + "/abitur/specialties/44-02-02-prepodavanie-v-nachalnykh-klassakh/", Kind: "specialty_detail"},
		{Name: "������", URL: base + "/abitur/specialties/54-02-01-dizayn/", Kind: "specialty_detail"},
		
		// ��������� (�������������)
		{Name: "���������", URL: base + "/students/", Kind: "students"},
		{Name: "�������� � ����������", URL: base + "/students/practice/", Kind: "practice"},
		{Name: "���������� �������", URL: base + "/students/schedule/", Kind: "schedule"},
		{Name: "��������������� �������", URL: base + "/students/educational-resources/", Kind: "students"},
		{Name: "��������� � ������������� �������������", URL: base + "/retake/", Kind: "retake"},
		
		// ������� (������ 10 �������)
		{Name: "�������", URL: base + "/press-center/news/", Kind: "news"},
		{Name: "������� � ���. 2", URL: base + "/press-center/news/?PAGEN_1=2", Kind: "news"},
		{Name: "������� � ���. 3", URL: base + "/press-center/news/?PAGEN_1=3", Kind: "news"},
		{Name: "������� � ���. 4", URL: base + "/press-center/news/?PAGEN_1=4", Kind: "news"},
		{Name: "������� � ���. 5", URL: base + "/press-center/news/?PAGEN_1=5", Kind: "news"},
		{Name: "������� � ���. 6", URL: base + "/press-center/news/?PAGEN_1=6", Kind: "news"},
		{Name: "������� � ���. 7", URL: base + "/press-center/news/?PAGEN_1=7", Kind: "news"},
		{Name: "������� � ���. 8", URL: base + "/press-center/news/?PAGEN_1=8", Kind: "news"},
		{Name: "������� � ���. 9", URL: base + "/press-center/news/?PAGEN_1=9", Kind: "news"},
		{Name: "������� � ���. 10", URL: base + "/press-center/news/?PAGEN_1=10", Kind: "news"},
	}
}

// =========================================================
// CHUNKING AND SEARCH
// =========================================================

type Chunk struct {
	Text       string
	Source     Source
	StartIndex int
	EndIndex   int
}

// ��������� ����� �� ������������� ��������� � ����������� ���������
func splitIntoChunks(text string, source Source, chunkSize int) []Chunk {
	lines := strings.Split(text, "\n")
	var chunks []Chunk
	var currentChunk strings.Builder
	var chunkStart int
	lineIndex := 0
	
	// ����� ��� ���������� ���������� ��������������
	var lastSpecialtyHeader string
	specialtyCodePattern := regexp.MustCompile(`(?i)(40\.02\.0[14]|44\.02\.02|54\.02\.01)`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		// ����������, �������� �� ������ ���������� �������������
		isSpecialtyLine := specialtyCodePattern.MatchString(line) && 
			(strings.Contains(strings.ToLower(line), "������") ||
			 strings.Contains(strings.ToLower(line), "������������") ||
			 strings.Contains(strings.ToLower(line), "�����������") ||
			 strings.Contains(strings.ToLower(line), "�����"))
		
		if isSpecialtyLine {
			lastSpecialtyHeader = line
		}

		// �������� ������������ chunk
		willOverflow := currentChunk.Len() > 0 && currentChunk.Len()+len(line) > chunkSize
		
		// ���� ������ �������� ����, �� ��������� chunk
		hasPriceInfo := strings.Contains(strings.ToLower(line), "���������") ||
			strings.Contains(strings.ToLower(line), "������") ||
			regexp.MustCompile(`\d{2,3}\s*\d{3}\s*���`).MatchString(strings.ToLower(line))
		
		if willOverflow && !hasPriceInfo {
			// ��������� ������� chunk
			chunk := Chunk{
				Text:       strings.TrimSpace(currentChunk.String()),
				Source:     source,
				StartIndex: chunkStart,
				EndIndex:   lineIndex,
			}
			if chunk.Text != "" {
				chunks = append(chunks, chunk)
			}
			currentChunk.Reset()
			chunkStart = lineIndex
			
			// ���� ��� ������� ��������� �������������, ��������� ��� � ����� chunk
			if lastSpecialtyHeader != "" && !strings.Contains(line, lastSpecialtyHeader) {
				currentChunk.WriteString(lastSpecialtyHeader)
				currentChunk.WriteString("\n")
			}
		}

		if currentChunk.Len() > 0 && !strings.HasSuffix(currentChunk.String(), "\n") {
			currentChunk.WriteString("\n")
		}
		currentChunk.WriteString(line)
		lineIndex++
	}

	// ��������� ��������� ��������
	if currentChunk.Len() > 0 {
		chunk := Chunk{
			Text:       strings.TrimSpace(currentChunk.String()),
			Source:     source,
			StartIndex: chunkStart,
			EndIndex:   lineIndex,
		}
		if chunk.Text != "" {
			chunks = append(chunks, chunk)
		}
	}

	return chunks
}

// ������� BM25-�������� ������� ����������
func scoreChunk(chunk Chunk, terms []string) float64 {
	if len(terms) == 0 {
		return 0
	}

	text := normalize(chunk.Text)
	score := 0.0

	for _, term := range terms {
		// ������� ��������� �������
		count := float64(strings.Count(text, term))
		if count > 0 {
			// TF (term frequency) � ����������
			tf := count / (count + 1.0)
			score += tf
		}
	}

	// ����� �� ����� ��������� (����� ������� ��������� ���������������� ��� ������ �����)
	lengthBonus := float64(len(chunk.Text)) / 10000.0
	score += lengthBonus * 0.1

	return score
}

// ����� ���-N ����������� ����������
func findRelevantChunks(pages []CachedPage, question string, limit int) []Chunk {
	terms := questionTerms(normalize(question))
	if len(terms) == 0 {
		// ���� ��� �������� ����, ���������� ������ N ����������
		var allChunks []Chunk
		for _, page := range pages {
			chunks := splitIntoChunks(page.Text, page.Source, 3000)
			allChunks = append(allChunks, chunks...)
		}
		if len(allChunks) > limit {
			return allChunks[:limit]
		}
		return allChunks
	}

	// ��������� ��� �������� �� ���������
	type scoredChunk struct {
		chunk Chunk
		score float64
	}
	
	var scored []scoredChunk
	for _, page := range pages {
		chunks := splitIntoChunks(page.Text, page.Source, 3000)
		for _, chunk := range chunks {
			s := scoreChunk(chunk, terms)
			if s > 0 {
				scored = append(scored, scoredChunk{chunk: chunk, score: s})
			}
		}
	}

	// ��������� �� �������� �����
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// ����� ���-N
	n := limit
	if len(scored) < n {
		n = len(scored)
	}

	result := make([]Chunk, n)
	for i := 0; i < n; i++ {
		result[i] = scored[i].chunk
	}

	return result
}

// =========================================================
// HTTP CLIENT + CACHE
// =========================================================

var httpClient = &http.Client{
	Timeout: 120 * time.Second, // ��������� ��� Ollama (�������� ������ + ���������)
}

var pageCache = struct {
	sync.RWMutex
	items map[string]CachedPage
}{
	items: make(map[string]CachedPage),
}

func fetchPage(ctx context.Context, source Source, cacheTime time.Duration) (CachedPage, error) {
	pageCache.RLock()
	cached, ok := pageCache.items[source.URL]
	pageCache.RUnlock()

	if ok && time.Since(cached.UpdatedAt) < cacheTime {
		return cached, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return CachedPage{}, err
	}

	req.Header.Set("User-Agent", "Erudit/3.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Charset", "utf-8")

	log.Printf("NOMOS URL: %s", req.URL.String())

	resp, err := httpClient.Do(req)
	if err != nil {
		return CachedPage{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("NOMOS ERROR: HTTP %d, URL: %s", resp.StatusCode, req.URL.String())
		return CachedPage{}, fmt.Errorf("NOMOS HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 15*1024*1024))
	if err != nil {
		return CachedPage{}, err
	}

	// ������������ HTML � ������ � ������ ���������
	htmlContent := decodeHTML(body, resp.Header.Get("Content-Type"))
	extractedText := extractTextFromHTML(htmlContent)
	
	log.Printf("NOMOS CONTENT: URL=%s, HTML=%d bytes, Text=%d chars", 
		req.URL.String(), len(body), len(extractedText))

	page := CachedPage{
		Text:      extractedText,
		Source:    source,
		UpdatedAt: time.Now(),
	}

	pageCache.Lock()
	pageCache.items[source.URL] = page
	pageCache.Unlock()

	return page, nil
}

// =========================================================
// HTML -> TEXT
// =========================================================

// decodeHTML ������������ ����� HTML � ������ UTF-8 � ������ ���������
func decodeHTML(body []byte, contentType string) string {
	// ��������� Content-Type �� ������� charset
	contentType = strings.ToLower(contentType)
	
	// ������� ��������� ���������� UTF-8
	isValidUTF8 := utf8.Valid(body)
	
	// �������� ����� charset � Content-Type
	if strings.Contains(contentType, "charset=") {
		parts := strings.Split(contentType, "charset=")
		if len(parts) > 1 {
			charset := strings.TrimSpace(strings.Split(parts[1], ";")[0])
			log.Printf("Detected charset from Content-Type: %s", charset)
			
			// ���� ���� ������ UTF-8
			if strings.Contains(charset, "utf-8") {
				if isValidUTF8 {
					log.Printf("UTF-8 validation passed, using as-is")
					return string(body)
				} else {
					// UTF-8 � �������� - ������� ���������� ������������������
					log.Printf("UTF-8 with invalid sequences, cleaning up")
					return strings.ToValidUTF8(string(body), "")
				}
			}
			
			// ���� ��� windows-1251, ������������
			if strings.Contains(charset, "windows-1251") || strings.Contains(charset, "cp1251") {
				return decodeWindows1251(body)
			}
		}
	}
	
	// ��������� �� ���������� UTF-8
	if isValidUTF8 {
		log.Printf("UTF-8 validation passed, using as-is")
		return string(body)
	}
	
	// ��������� meta-��� ��� ����������� UTF-8
	checkLen := 2000
	if len(body) < checkLen {
		checkLen = len(body)
	}
	
	// ���������� ToValidUTF8 ��� ����������� ������ ������
	htmlStart := strings.ToValidUTF8(string(body[:checkLen]), "")
	
	// <meta charset="...">
	charsetRe := regexp.MustCompile(`(?i)<meta[^>]+charset\s*=\s*["']?([^"'\s>]+)`)
	if matches := charsetRe.FindStringSubmatch(htmlStart); len(matches) > 1 {
		charset := strings.ToLower(matches[1])
		log.Printf("Detected charset from meta tag: %s", charset)
		
		if strings.Contains(charset, "utf-8") {
			// Meta ������� UTF-8, �� ���� ���������� ����� - �������
			log.Printf("Meta says UTF-8, cleaning invalid sequences")
			return strings.ToValidUTF8(string(body), "")
		}
		
		if strings.Contains(charset, "windows-1251") || strings.Contains(charset, "cp1251") {
			return decodeWindows1251(body)
		}
	}
	
	// �� ��������� ������� �������� ��� UTF-8
	log.Printf("No explicit charset found, trying UTF-8 cleanup")
	cleaned := strings.ToValidUTF8(string(body), "")
	
	// ���� ����� ������� ������� ����� ����������, ������� windows-1251
	if len(cleaned) < len(body)*8/10 {
		log.Printf("Too much data lost in UTF-8 cleanup, trying windows-1251")
		return decodeWindows1251(body)
	}
	
	return cleaned
}

// decodeWindows1251 ������������ windows-1251 � UTF-8
func decodeWindows1251(data []byte) string {
	// ������� ����������� windows-1251 -> UTF-8 ��� ������� ����
	buf := make([]rune, 0, len(data))
	
	for _, b := range data {
		switch {
		case b < 0x80:
			// ASCII ��� ���������
			buf = append(buf, rune(b))
		case b >= 0xC0 && b <= 0xDF:
			// �-�: 0xC0-0xDF -> U+0410-U+042F
			buf = append(buf, rune(0x0410+int(b)-0xC0))
		case b >= 0xE0 && b <= 0xFF:
			// �-�: 0xE0-0xFF -> U+0430-U+044F
			buf = append(buf, rune(0x0430+int(b)-0xE0))
		case b == 0xA8:
			// �
			buf = append(buf, '�')
		case b == 0xB8:
			// �
			buf = append(buf, '�')
		default:
			// ��������� ������� ��������� ��� ����
			buf = append(buf, rune(b))
		}
	}
	
	return string(buf)
}

func extractTextFromHTML(source string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`),
		regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`),
		regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`),
		regexp.MustCompile(`(?is)<svg[^>]*>.*?</svg>`),
		regexp.MustCompile(`(?s)<!--.*?-->`),
	}

	for _, re := range patterns {
		source = re.ReplaceAllString(source, "\n")
	}

	blockTags := regexp.MustCompile(`(?i)</?(p|div|section|article|li|h1|h2|h3|h4|h5|h6|tr|br|td|th|ul|ol|main|header|footer|table)[^>]*>`)
	source = blockTags.ReplaceAllString(source, "\n")

	tags := regexp.MustCompile(`(?s)<[^>]+>`)
	source = tags.ReplaceAllString(source, " ")
	source = html.UnescapeString(source)
	source = strings.ReplaceAll(source, "\u00a0", " ")

	lines := strings.Split(source, "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		line = strings.TrimSpace(line)
		if line != "" {
			clean = append(clean, line)
		}
	}

	return strings.Join(clean, "\n")
}

// =========================================================
// TEXT HELPERS
// =========================================================

func normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "�", "�")
	// ������� ����� ����������
	s = strings.NewReplacer(
		".", " ", ",", " ", "!", " ", "?", " ", ":", " ", ";", " ",
		"(", " ", ")", " ", "-", " ", "�", " ", "/", " ", "\"", " ", "'", " ",
	).Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// stem ��������� ������� �������� ��� ������� ���� (������� ���������)
func stem(word string) string {
	if len(word) <= 3 {
		return word
	}
	
	// ������ ���������������� ��������� �������� ����� (�� ������� � ��������)
	endings := []string{
		"��������", "�������", "�������", "�������", "������", "������", "������",
		"������", "�����", "�����", "�����", "����", "����",
		"����", "���", "���", "���", "��", "��",
		"���", "��", "��", "���", "��", "��",
		"���", "��", "��", "��", "��", "��",
		"���", "��", "��", "��", "��", "��", "��", "��", "��", "��",
		"��", "��", "��", "��",
		"��", "��", "��", "��", "��",
		"��", "��", "��", "��", "��",
		"���", "��", "��", "��", "��", "��", "��", "��", "��",
		"�", "�", "�", "�", "�", "�", "�", "�", "�",
	}
	
	for _, ending := range endings {
		if len(word) > len(ending)+2 && strings.HasSuffix(word, ending) {
			return word[:len(word)-len(ending)]
		}
	}
	
	return word
}

func tokenize(s string) []string {
	s = normalize(s)
	s = strings.NewReplacer(
		".", " ", ",", " ", "!", " ", "?", " ", ":", " ", ";", " ",
		"(", " ", ")", " ", "-", " ", "�", " ", "/", " ",
	).Replace(s)
	return strings.Fields(s)
}

func containsAny(s string, words ...string) bool {
	for _, word := range words {
		if strings.Contains(s, word) {
			return true
		}
	}
	return false
}

// =========================================================
// QUESTION ROUTING
// =========================================================

func isNewsQuestion(q string) bool {
	q = normalize(q)
	return containsAny(q,
		"������",
		"��� ������",
		"��������� �������",
		"������� � ��������",
		"��������� �������",
		"������ �������",
		"��� ���������",
	)
}

func isTeacherQuestion(q string) bool {
	q = normalize(q)

	// ����� ������� � ��������������.
	if containsAny(q,
		"������������",
		"���������",
		"��������",
		"��� �����",
		"��� ����",
		"��� ���������",
		"��� ��������",
		"�������",
		"�������",
		"�������",
	) {
		return true
	}

	// ���� + ������� ���� ������� �������� � �������������,
	// �� ��� ������� ��� ���� ������ �� ���������� � ���� �����.
	if strings.Contains(q, "���") && containsAny(q,
		"����������",
		"���������",
		"������",
		"�����",
		"�������",
		"��������",
		"������",
		"����������",
	) {
		return true
	}

	return false
}

func isSpecialtyQuestion(q string) bool {
	return containsAny(q,
		"������������",
		"�������������",
		"���������",
		"�����������",
		"40.02.01",
		"40.02.04",
		"44.02.02",
		"54.02.01",
		"����������",
		"����� � ����������� ����������� �����������",
		"����������� �����������",
		"������������ � ��������� �������",
		"��������� �������",
		"������",
	)
}

func sourceScore(source Source, q string) int {
	score := 0

	// �������
	if isNewsQuestion(q) {
		if source.Kind == "news" {
			score += 150
		}
	}
	
	// �������������
	if isTeacherQuestion(q) {
		if source.Kind == "teachers" {
			score += 100
		}
	}
	
	// �������������
	if isSpecialtyQuestion(q) {
		if source.Kind == "specialties" || source.Kind == "specialty_detail" {
			score += 100
		}
	}
	
	// �����������, ���������, ��������
	if containsAny(q, "������", "�����", "��������", "��������", "�����", "���", "�������") {
		if source.Kind == "admission" {
			score += 100
		}
		// �����������: ���� ������ � ��������� ���������� �������������,
		// ��������� ����� �������� ���� �������������
		if containsAny(q, "�����", "���") {
			if source.Kind == "specialty_detail" {
				score += 80
			}
			if source.Kind == "payment" {
				score += 120
			}
			if source.Kind == "specialties" {
				score += 90
			}
		}
	}
	
	// �����������
	if containsAny(q, "���������", "������� ����", "���������", "�������") {
		if source.Kind == "education" || source.Kind == "specialty_detail" {
			score += 60
		}
	}
	
	// �����������-����������� �����������
	if containsAny(q, "�������", "���������", "�����", "����������", "������") {
		if source.Kind == "facilities" {
			score += 100
		}
	}
	
	// ���������
	if containsAny(q, "��������", "������", "���������") {
		if source.Kind == "documents" {
			score += 100
		}
	}

	return score
}

func chooseSources(sources []Source, question string) []Source {
	q := normalize(question)

	// �������: ���� ������ �������� � ����������.
	if isNewsQuestion(q) {
		var result []Source
		for _, source := range sources {
			if source.Kind == "news" {
				result = append(result, source)
			}
		}
		return result
	}

	// �������������: ������ ��� �������� ��������������.

	if isTeacherQuestion(q) {
		var result []Source
		for _, source := range sources {
			if source.Kind == "teachers" {
				result = append(result, source)
			}
		}
		return result
	}

	// �������������: ���� � ������� ������� ���������� �������������,
	// ��������� ������ � �������� + ��� ������������� ������.
	if isSpecialtyQuestion(q) {
		if isGenericSpecialtyListQuestion(q) {
			var result []Source
			for _, source := range sources {
				if source.Kind == "specialties" {
					result = append(result, source)
				}
			}
			return result
		}

		var result []Source
		for _, source := range sources {
			if source.Kind != "specialty_detail" {
				continue
			}
			if specialtyMatchesQuestion(q, source.Name) {
				result = append(result, source)
			}
		}

		// �����������: ���� ������ � ��������� ���������� �������������,
		// ��������� �������� "�����������" ��� ���� ��� ����
		if containsAny(q, "�����", "���") && len(result) > 0 {
			for _, source := range sources {
				if source.Kind == "admission" {
					result = append(result, source)
					break
				}
			}
		}

		// ���� ������������ ���������� ���� ������������� ��� ������ ��������,
		// ���� ������ � ��� ������ ��������.
		if len(result) == 0 {
			for _, source := range sources {
				if source.Kind == "specialties" || source.Kind == "specialty_detail" {
					result = append(result, source)
				}
			}
		}
		return uniqueSources(result)
	}

	type scoredSource struct {
		source Source
		score  int
	}

	var scored []scoredSource
	for _, source := range sources {
		s := sourceScore(source, q)
		if s > 0 {
			scored = append(scored, scoredSource{source: source, score: s})
		}
	}

	if len(scored) == 0 {
		// ��� ���������� ���������� ���� ������� � �����������,
		// � �� ��������� ��������.
		for _, source := range sources {
			if source.Kind == "general" || source.Kind == "admission" {
				scored = append(scored, scoredSource{source: source, score: 1})
			}
		}
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// �����������: ��� ����� �������� � ��������� ����������� �����,
	// ����� ��������� � "�����������" � �������� ��������������
	limit := 3
	if containsAny(q, "�����", "���") && !isSpecialtyQuestion(q) {
		limit = 6 // ����������� + 4 ������������� + �����
	}
	
	if len(scored) < limit {
		limit = len(scored)
	}

	result := make([]Source, 0, limit)
	for _, item := range scored[:limit] {
		result = append(result, item.source)
	}
	return uniqueSources(result)
}

func uniqueSources(sources []Source) []Source {
	seen := make(map[string]bool, len(sources))
	result := make([]Source, 0, len(sources))
	for _, source := range sources {
		if seen[source.URL] {
			continue
		}
		seen[source.URL] = true
		result = append(result, source)
	}
	return result
}

func titleMatchesSpecialty(q, name string) bool {
	return specialtyMatchesQuestion(q, name)
}

func specialtyMatchesQuestion(q, name string) bool {
	q = normalize(q)
	n := normalize(name)

	pairs := [][2]string{
		{"40.02.01", "����� � ����������� ����������� �����������"},
		{"40.02.04", "�������������"},
		{"44.02.02", "������������ � ��������� �������"},
		{"54.02.01", "������"},
	}

	for _, pair := range pairs {
		if (strings.Contains(q, pair[0]) || strings.Contains(q, pair[1])) &&
			(strings.Contains(n, pair[0]) || strings.Contains(n, pair[1])) {
			return true
		}
	}

	return false
}

func questionTerms(q string) []string {
	stop := map[string]bool{
		"как": true, "что": true, "где": true, "когда": true, "почему": true, "какой": true,
		"какая": true, "какие": true, "кто": true, "чей": true, "чья": true, "чьи": true,
		"эта": true, "например": true, "пожалуйста": true, "для": true, "более": true,
		"подробно": true, "расскажи": true,
		// Важно: удалили "специальность", "специальности", "направление" из стоп-слов
	}

	seen := map[string]bool{}
	var result []string
	for _, word := range tokenize(q) {
		// ���������: ������� 3 ������� ������ 4
		if len([]rune(word)) < 3 || stop[word] || seen[word] {
			continue
		}
		seen[word] = true
		result = append(result, word)
	}
	
	// ���������: ��������� ���������� � ��������
	expanded := expandTerms(result)
	return expanded
}

// expandTerms ��������� ���������� � �������� ��� ��������� ������
func expandTerms(terms []string) []string {
	synonyms := map[string][]string{
		"бакалавр":      {"бакалавр", "бак", "бакалавриат", "бакалавра", "бакалаврская"},
		"маг":           {"маг", "магистр", "магистратура", "магистерская"},
		"магистр":       {"магистр", "магистратура", "маг", "магистерская"},
		"специалист":    {"специалист", "специалитет", "специальность", "спец"},
		"аспирант":      {"аспирант", "аспирантура", "послевуз"},
		"очная":         {"очная", "очное"},
		"заочная":       {"заочная", "заочное"},
		"очно-заочная":  {"очно-заочная", "вечерняя", "вечерка"},
		"вечерняя":      {"вечерняя", "очно-заочная", "вечерка"},
		"дневная":       {"дневная", "очная", "дневное"},
		"платное":       {"платное", "платная", "платный", "коммерция", "коммерческое"},
		"бюджет":        {"бюджет", "бесплатно", "госбюджет"},
	}
	seen := make(map[string]bool)
	var expanded []string
	
	for _, term := range terms {
		if !seen[term] {
			expanded = append(expanded, term)
			seen[term] = true
		}
		
		// ��������� �������� � ����������
		for key, values := range synonyms {
			if strings.HasPrefix(term, key) || strings.HasPrefix(key, term) {
				for _, syn := range values {
					if !seen[syn] && syn != term {
						expanded = append(expanded, syn)
						seen[syn] = true
					}
				}
				break
			}
		}
	}
	
	return expanded
}

// =========================================================
// CONTEXT
// =========================================================

func loadContext(ctx context.Context, sources []Source, question string, cacheTime time.Duration) (string, []Source, error) {
	selected := chooseSources(sources, question)
	if len(selected) == 0 {
		return "", nil, errors.New("�� ������� ��������� NOMOS")
	}

	type result struct {
		page CachedPage
		err  error
	}

	// ��������� ������� selected: ����� � ������������� ��������� �����
	// ��������� ������� � ��-�� ������ ��������� ������ ������ ������ ������ ���� ��������.
	results := make([]result, len(selected))
	var wg sync.WaitGroup

	for i, source := range selected {
		wg.Add(1)
		go func(index int, src Source) {
			defer wg.Done()
			page, err := fetchPage(ctx, src, cacheTime)
			results[index] = result{page: page, err: err}
		}(i, source)
	}
	wg.Wait()

	var pages []CachedPage
	for _, item := range results {
		if item.err != nil {
			log.Printf("�������� �� ����������: %s: %v", item.page.Source.URL, item.err)
			continue
		}
		pages = append(pages, item.page)
	}

	if len(pages) == 0 {
		return "", nil, errors.New("�� ������� ��������� �� ����� �������� NOMOS")
	}

	questionLower := normalize(question)
	
	// ���������� RAG (chunking + �����) ��� ��������� �������������
	useRAG := true
	if isNewsQuestion(questionLower) {
		// ��� �������� ����� �������� ������ ������
		useRAG = false
	}

	var builder strings.Builder
	builder.WriteString("�������� � ������������ ����� �������� �����ѻ\n\n")
	
	if useRAG {
		// RAG �����: ������� ����������� ���������
		chunks := findRelevantChunks(pages, question, 15) // ���-15 ����������� ����������
		
		builder.WriteString("���� ������������ ��������� ����������, ����������� ������� ������������.\n")
		builder.WriteString("��������� ������ ����� �� ���� ���������� ��� ������.\n\n")
		
		seenSources := make(map[string]bool)
		totalChars := 0
		maxTotal := 80000
		
		for i, chunk := range chunks {
			if totalChars+len(chunk.Text) > maxTotal {
				break
			}
			
			fmt.Fprintf(&builder, "--- �������� %d: %s ---\n", i+1, chunk.Source.Name)
			builder.WriteString(chunk.Text)
			builder.WriteString("\n\n")
			
			totalChars += len(chunk.Text)
			seenSources[chunk.Source.URL] = true
		}
		
		// �������� ���������� ���������
		var usedSources []Source
		for _, page := range pages {
			if seenSources[page.Source.URL] {
				usedSources = append(usedSources, page.Source)
			}
		}
		
		log.Printf("RAG: ������������ %d ����������, %d �������� �� %d ����������", len(chunks), totalChars, len(usedSources))
		return builder.String(), usedSources, nil
	}

	// ������ �����: ������ ��������
	builder.WriteString("�����: ����� ���� ��������� � ������� ������������; ��������� ������ ����������� �����.\n\n")

	maxPerPage := 20000  // ��������� � 12000
	maxTotal := 80000    // ��������� � 24000 ��� ��������� num_ctx=32768
	if isNewsQuestion(questionLower) {
		maxPerPage = 4000  // ��������� � 2500
		maxTotal = 40000   // ��������� � 30000
	}
	total := 0

	for _, page := range pages {
		text := focusPageText(page, question, maxPerPage)
		if strings.TrimSpace(text) == "" {
			continue
		}
		if total+len(text) > maxTotal {
			remain := maxTotal - total
			if remain <= 0 {
				break
			}
			text = text[:remain]
		}

		fmt.Fprintf(&builder, "=== ��������: %s ===\n", page.Source.Name)
		builder.WriteString(text)
		builder.WriteString("\n\n")
		total += len(text)
	}

	return builder.String(), pagesToSources(pages), nil
}

func focusPageText(page CachedPage, question string, limit int) string {
	text := page.Text
	q := normalize(question)

	if page.Source.Kind == "news" {
		return focusNewsText(text, q, limit)
	}

	if page.Source.Kind == "teachers" {
		return focusTeacherText(text, q, limit)
	}

	if page.Source.Kind == "teachers" {
		return focusTeacherText(text, q, limit)
	}

	if page.Source.Kind == "specialty_detail" {
		// ���� ��� ���������� �������������, �� ����� �������� �� ������ ��������� �����:
		// ������ ���������� ����� ���������� ������ ����.
		if specialtyMatchesQuestion(q, page.Source.Name) || containsAny(q, "��������", "���", "������", "���������", "�������", "����������", "����") {
			return trimRunes(text, limit)
		}
	}

	return trimRunes(text, limit)
}

func focusNewsText(text, q string, limit int) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder

	dateRE := regexp.MustCompile(`^(0?[1-9]|[12]\d|3[01])\.(0?[1-9]|1[0-2])\.20\d\d$`)
	generic := q == "�������" || q == "��������� �������" || q == "������ �������" || q == "��� ������"
	terms := questionTerms(q)

	var date, title string
	var snippet []string
	seen := make(map[string]bool)

	flush := func() {
		if title == "" {
			date, title, snippet = "", "", nil
			return
		}
		block := strings.TrimSpace(strings.Join(snippet, " "))
		lower := normalize(title + " " + block)
		match := generic
		if !generic {
			for _, term := range terms {
				if strings.Contains(lower, term) {
					match = true
					break
				}
			}
		}
		key := normalize(title) + "|" + date
		if match && !seen[key] {
			seen[key] = true
			if date != "" {
				b.WriteString(date)
				b.WriteByte('\n')
			}
			b.WriteString(title)
			b.WriteByte('\n')
			if block != "" {
				b.WriteString(block)
				b.WriteByte('\n')
			}
			b.WriteByte('\n')
		}
		date, title, snippet = "", "", nil
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if dateRE.MatchString(line) {
			flush()
			date = line
			continue
		}
		if date != "" && title == "" && len([]rune(line)) >= 8 && len([]rune(line)) <= 180 {
			if !containsAny(normalize(line), "��������", "���������", "����������", "select") {
				title = line
				continue
			}
		}
		if title != "" {
			snippet = append(snippet, line)
		}
	}
	flush()

	if strings.TrimSpace(b.String()) == "" {
		return trimRunes(text, limit)
	}
	return trimRunes(b.String(), limit)
}

func focusTeacherText(text, q string, limit int) string {
	lines := strings.Split(text, "\n")
	nameRE := regexp.MustCompile(`^[�-ߨ][�-��-]+ [�-ߨ][�-��]+ [�-ߨ][�-��-]+$`)

	type record struct {
		name  string
		text  string
		score int
	}

	var records []record
	current := ""
	var chunk []string
	terms := questionTerms(q)
	generic := isGenericTeacherQuestion(q)

	flush := func() {
		if current == "" {
			chunk = nil
			return
		}
		recordText := strings.TrimSpace(strings.Join(chunk, "\n"))
		score := 0
		lower := normalize(recordText)
		for _, term := range terms {
			if strings.Contains(lower, term) {
				score++
			}
		}
		if generic {
			score = 1
		}
		records = append(records, record{name: current, text: recordText, score: score})
		chunk = nil
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if nameRE.MatchString(line) {
			flush()
			current = line
			chunk = append(chunk, line)
			continue
		}
		if current != "" {
			chunk = append(chunk, line)
		}
	}
	flush()

	// ��� ����������� �������� ���� ������ ��������� ��������.
	if !generic {
		filtered := records[:0]
		for _, r := range records {
			if r.score > 0 {
				filtered = append(filtered, r)
			}
		}
		records = filtered
	}

	var b strings.Builder
	for _, r := range records {
		// ������� �������� �� ����: ��� + ������ ��������� + �������� ��� �������.
		lines := strings.Split(r.text, "\n")
		for _, line := range lines {
			lower := normalize(line)
			if line == r.name ||
				strings.Contains(lower, "������������� ������� ��������") ||
				strings.Contains(lower, "���������� �������") ||
				strings.Contains(lower, "����� ����������� �����") {
				b.WriteString(line)
				b.WriteByte('\n')
			}
		}
		b.WriteByte('\n')
		if b.Len() >= limit {
			break
		}
	}

	return trimRunes(b.String(), limit)
}

func isGenericTeacherQuestion(q string) bool {
	q = normalize(q)
	return q == "преподаватели" ||
		q == "список преподавателей" ||
		q == "какие преподаватели" ||
		q == "какие преподаватели есть" ||
		q == "кто работает преподавателями"
}

func trimRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	r := []rune(text)
	if len(r) <= limit {
		return text
	}
	return string(r[:limit])
}

func pagesToSources(pages []CachedPage) []Source {
	result := make([]Source, 0, len(pages))
	seen := map[string]bool{}
	for _, page := range pages {
		if seen[page.Source.URL] {
			continue
		}
		seen[page.Source.URL] = true
		result = append(result, page.Source)
	}
	return result
}

// =========================================================
// OLLAMA
// =========================================================

type OllamaGenerateRequest struct {
	Model     string         `json:"model"`
	Prompt    string         `json:"prompt"`
	System    string         `json:"system,omitempty"`
	Stream    bool           `json:"stream"`
	Options   map[string]any `json:"options,omitempty"`
	KeepAlive string         `json:"keep_alive,omitempty"`
}

type OllamaGenerateResponse struct {
	Model      string `json:"model"`
	Response   string `json:"response"`
	Done       bool   `json:"done"`
	DoneReason string `json:"done_reason"`
}

type OllamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

func pickOllamaModel(ctx context.Context, cfg Config) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.OllamaURL+"/api/tags", nil)
	if err != nil {
		return "", err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Ollama ���������� �� %s: %w", cfg.OllamaURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Ollama /api/tags ������� HTTP %d", resp.StatusCode)
	}

	var tags OllamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", fmt.Errorf("�� ������� ��������� ������ ������� Ollama: %w", err)
	}

	if len(tags.Models) == 0 {
		return "", errors.New("� Ollama ��� ������������� ������; �������� ������ �������� ollama pull <���>")
	}

	if cfg.OllamaModel != "" {
		for _, item := range tags.Models {
			if strings.EqualFold(item.Name, cfg.OllamaModel) {
				return item.Name, nil
			}
		}
		log.Printf("OLLAMA MODEL %q �� �������, ��������� %q", cfg.OllamaModel, tags.Models[0].Name)
	}

	return tags.Models[0].Name, nil
}

func buildAnswerInstructions(question string) string {
	q := normalize(question)
	mode := "����� ������"
	if isNewsQuestion(q) {
		mode = "�������"
	} else if isTeacherQuestion(q) {
		mode = "�������������"
	} else if isSpecialtyQuestion(q) {
		if isGenericSpecialtyListQuestion(q) {
			mode = "������ ��������������"
		} else if containsAny(q, "������", "���������", "�������", "����������") {
			mode = "��������� ��������������"
		} else {
			mode = "���������� �������������"
		}
	}

	terms := questionTerms(q)

	var b strings.Builder
	b.WriteString(`�� � ������, �������� ������������ �������� ������.

?? ���������� ����� - ������ � ����������:
==========================================
? ��� ����� ����� ������ �� ��������������� ���������� ��������� (3-5 ����������)
? �������� �������� ���������� ������ � ����� ��������
? ���� ���������� ���� � ��������� > ������� ����� � ��������
? ���� ���������� ��� � ��������� > ������ ������� ���
? ������� �� ��������� �����, ����, ����, �����, ��������
? �� ��������� ������ �� ��������� ������ � ������ ��������
? �� ������ "�� ����� ����������" � ����� ����� ����� � ��� ������������!

?? ������� ��� �������� ������:
- ���� ������ ���: "� ���������, ���� ���������� ��� �� ����� ��������. �������� � ������� ��������: +7 (473) 271-35-36"
- ���� ������ ��������������: ����� ��� �������� ��� �������� ��������� � ���������
- �� ������� ������� ��� ��������� ����������

����� ������:
- ������� ����� � �� �������� �������
- �� �������� ���������� ("������!", "�������!", "�� ������ ������...")
- �� �������� ���������� ("��� ��� ������?", "���� �������?")
- ��������� �� "��"
- ��� Markdown (**, ##, ---), ��� URL
- ��� ������ (����� ��������)

?? ������� ������ �� �������� ������:
? �� �������� ����������, ������� �� ����������
? ���� �������� ������ �������������� > �� �������� ���������, ���������, ��������������
? ���� �������� ��� ��������� > �� �������� ��������������, �������������
? ���� �������� ��������� > ������� ������ ��� ���������
? ����� ������ ����������� � ������� ������ �� ��, ��� ����������

������� ������:
- ��������: �������� ��� ��������� ������� ����� (�������, email, �����)
- �����: �������� ������ �����, �� ������������� �������
- ���������: �������� �����, ������, ����� �������� ���� ���� � ���������
- �������: ���������� ��� ������ ���������� � ���������

�������:
���� ������ �� ��� ������� > ������� ����� + ������� � ����
������: "������� �� ��������� ?? �������� ��� ����������� � �������. ��� ����������?"

����: ������ ������� (���� ���� ������ �� ������ �����)

��������� �������� � ������ �������:
- ������ �������� ��� ������������� (54.02.01, 40.02.04 � �.�.)
- ���� ������ ������� ��� ��� ������ � ����������� ������� ���
- ���� � ��������� ��� ���������� � ����� �������� (�����/�������) � �� ���������
- ��� ��������� ��� � ������ ����������: "����� ������ ����������. �������� ���������� ��������� � ������� ��������: +7 (473) 271-35-36"

�������������:
- "��������" / "������" > ������������� 54.02.01 ������ (�� ��������)
- "�����" / "�������������" > ������������� 40.02.04 �������������
- "������� ��������� �������" / "������������" > 44.02.02 ������������ � ��������� �������
- "����� � ���������� �����������" > 40.02.01 ����� � ����������� ����������� �����������
- �� ����� ��� ������������� ����� �����
- "������ ���������" � ��� �������� ���������, ��������� �� �������������� "������"

���� � ������������:
- ��������������� �������� �������� ���� �������� ��������
- ���� �������� �� �������� ���� ���������� ��������� � ����
- ���� � ��������� ������ ������ (��������, "2024-2025 ������� ���"), ����������� ������� ���
- �� ����� ������������� �� ������������ �� ������ ���� ��������

��������� ����������:
- ��� ��� ���� ��� "�� �����"
- �����, ���� �� �������
- �������� �������� �������

������:
�������� ������ ("� ���������?") > ��������� �������� ���������� ��������� ��� ���������, � ��� ����
��: ������� ������� ������������ ������ ��� ��������� �������
���������� ������ ���� �� �������� ���������� ������ � ������ �������� �� ���������

�������������:
- ��������� ��� �� ���������
- ������ ��������/��������� ���� ����
- �� ����������

������� ���������� �������:

Q: "����� �������������?"
? "� �������� ������ �������������:
- 54.02.01 ������ (�� ��������)
- 40.02.04 �������������
- 44.02.02 ������������ � ��������� �������
- 40.02.01 ����� � ����������� ����������� �����������"

Q: "��������� �������?"
���� � ��������� ����: "�������� �� ������������� 54.02.01 ������ (�� ��������) ����� [���� �� ���������] ������ � ���."
���� ���: "���������� ��������� �������� �������� � ������� ��������: +7 (473) 271-35-36"

Q: "�������� ��������?"
? �������� ��� ���������: �������, email, �����, ���� ������
? �� ������������� ������ ���������

Q: "��� ���������� �������?"
? ����� ������ �����: �����, �����, ����� ����
? �� ������������� ������ �������
`)

	fmt.Fprintf(&b, "\n����� �������: %s\n", mode)
	if len(terms) > 0 {
		fmt.Fprintf(&b, "�������� ����� �������: %s\n", strings.Join(terms, ", "))
	}

	if isNewsQuestion(q) {
		b.WriteString(`
������������� ��� ��������:
- ������ ��������� ��������� �������� � ������
- �������� �������� � ������� ���������� ������ �������
- �� ���������� ������, ������� ��� � ���������
`)
	}

	if isTeacherQuestion(q) {
		b.WriteString(`
������������� ��� ��������������:
- ��������� ���� �������������� �� ��������� � �������: ���
- ���� ������� �������� � ������ �� ����� ���
- ��������� ������ ������ �� ���������, �� �������� �������������� "�� ����"
`)
	}

	if isSpecialtyQuestion(q) {
		if isGenericSpecialtyListQuestion(q) {
			b.WriteString(`
������������� ��� ������ ��������������:
- ������ ������ ������ �������������� (�������� + ���)
- �� �������� ���������, ���������, ��������������, ���������
- ������ ��������� 4 ������������� � � ��
`)
		} else if containsAny(q, "������", "���������", "�������", "����������") {
			b.WriteString(`
������������� ��� ���������:
- ������ ������ ��������� ������������� �� ��������� �� ���������
`)
		} else {
			b.WriteString(`
������������� ��� ���������� �������������:
- ������� ������ ��� �������������, ������� ������� ������������
- �� �������� ���������� ��� ������ �������������
`)
		}
	}

	return b.String()
}

func predictionLimit(question string) int {
	q := normalize(question)
	if isNewsQuestion(q) {
		return 2048
	}
	if isTeacherQuestion(q) {
		return 2048
	}
	if containsAny(q, "��������", "������", "���������", "���", "������ ������") {
		return 2048
	}
	if isSpecialtyQuestion(q) {
		return 1200
	}
	return 1200
}

func llmCacheModel(cfg Config) string {
	if cfg.LLMProvider == "external" {
		return cfg.LLMProvider + ":" + cfg.LLMModel
	}
	return cfg.LLMProvider + ":" + cfg.OllamaModel
}

func askOllama(ctx context.Context, cfg Config, question, contextText string, dialogContext *DialogContext) (string, error) {
	model, err := pickOllamaModel(ctx, cfg)
	if err != nil {
		return "", err
	}

	log.Printf("OLLAMA MODEL: %s", model)

	systemPrompt := buildAnswerInstructions(question)
	
	// ��������� ������ � �������� �������
	var promptBuilder strings.Builder
	
	// ��������� ������� ������� ���� ����
	// ���������� �����: �� �������� ���������� ������ ������ (BotReply)
	// ����� �������� ��������������� ������ ����� �������
	if dialogContext != nil && len(dialogContext.History) > 0 {
		promptBuilder.WriteString("=== �������� ������� (��� ��������� ���� � follow-up ��������) ===\n")
		
		// ���� ��������� 3 �������
		start := len(dialogContext.History) - 3
		if start < 0 {
			start = 0
		}
		
		for i := start; i < len(dialogContext.History); i++ {
			turn := dialogContext.History[i]
			// �������� ������ ������� ������������ � ����
			promptBuilder.WriteString(fmt.Sprintf("������ ������������: %s\n", turn.UserMessage))
			if turn.Intent.Topic != "" && turn.Intent.Topic != "general" {
				promptBuilder.WriteString(fmt.Sprintf("����: %s\n", turn.Intent.Topic))
			}
			promptBuilder.WriteString("\n")
		}
		
		promptBuilder.WriteString("=== ������� ������ ===\n")
	}
	
	promptBuilder.WriteString("������ ������������:\n" + question)
	promptBuilder.WriteString("\n\n=== �������� � ����� �������� (������������ �������� ������) ===\n" + contextText)
	promptBuilder.WriteString("\n\n=== ���� ������ ===\n�������� �������� �����������. ���� � �� ���� ����� �� ������ � ������� ����� � ��������, ��� ����� \"�� �����\". ��������� \"�� �����\" ������ ���� ������ ������������� ���.\n")
	promptBuilder.WriteString("\n��� ����� (����, ����, �����, ��������, ������) ���� ������ �� ��������� ����. ��������� �������� ������� ������ ��� ��������� ����, � �� ��� �������� ������.\n")
	
	// �����: ���� ���� ������� - ��������� � ��� ��������� ��������� ���������� ��������
	if dialogContext != nil && len(dialogContext.History) > 0 {
		promptBuilder.WriteString("\n��������: ���� ������������ ����� �������� ���������� ������ (��������, '� ���������?' ����� ������� � �������������), ��������� �������� ������� ���� ��� ���������, � ��� ������ ����������. �� ����� ������ �������� � ��������� � ����� ��������.\n")
	}
	
	prompt := promptBuilder.String()

	payload := OllamaGenerateRequest{
		Model:     model,
		Prompt:    prompt,
		System:    systemPrompt,
		Stream:    false,
		KeepAlive: "10m",
		Options: map[string]any{
			"temperature":    0.2,  // ������� � 0.5 ��� ���������� ����������� ������� (���������, ��������, ������)
			"top_p":          0.90,
			"repeat_penalty": 1.08,
			"num_ctx":        32768, // ��������� � 8192 ��� ������� ����������
			"num_predict":    predictionLimit(question),
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	modelCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(
		modelCtx,
		http.MethodPost,
		cfg.OllamaURL+"/api/generate",
		strings.NewReader(string(body)),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	started := time.Now()
	log.Printf("OLLAMA REQUEST: %s model=%s predict=%d", cfg.OllamaURL+"/api/generate", model, predictionLimit(question))

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("������ ����������� � Ollama: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("OLLAMA HTTP: %d (%s)", resp.StatusCode, time.Since(started).Round(time.Millisecond))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		return "", fmt.Errorf("Ollama /api/generate HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var result OllamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("������ ������� ������ Ollama: %w", err)
	}

	answer := cleanLLMAnswer(result.Response)
	if answer == "" {
		return "", errors.New("Ollama ������� ������ �����")
	}

	if result.DoneReason == "length" {
		log.Printf("OLLAMA: ����� ������ ������ ���������")
	}

	return ensureCompleteAnswer(answer), nil
}

// streamOllama ��������� streaming ������ � Ollama
func streamOllama(ctx context.Context, cfg Config, question, contextText string, dialogContext *DialogContext, onChunk func(string) error) error {
	model, err := pickOllamaModel(ctx, cfg)
	if err != nil {
		return err
	}

	systemPrompt := buildAnswerInstructions(question)
	
	// ��������� ������ � �������� ������� (���������� askOllama)
	var promptBuilder strings.Builder
	
	// ��������� ������� ������� ���� ����
	if dialogContext != nil && len(dialogContext.History) > 0 {
		promptBuilder.WriteString("=== ������� ������� (��������� ���������) ===\n")
		
		start := len(dialogContext.History) - 3
		if start < 0 {
			start = 0
		}
		
		for i := start; i < len(dialogContext.History); i++ {
			turn := dialogContext.History[i]
			promptBuilder.WriteString(fmt.Sprintf("������������: %s\n", turn.UserMessage))
			if turn.BotReply != "" {
				promptBuilder.WriteString(fmt.Sprintf("�� �������: %s\n", turn.BotReply))
			}
			promptBuilder.WriteString("\n")
		}
		
		promptBuilder.WriteString("=== ������� ������ ===\n")
	}
	
	promptBuilder.WriteString("������ ������������:\n" + question)
	promptBuilder.WriteString("\n\n=== �������� � ����� �������� ===\n" + contextText)
	promptBuilder.WriteString("\n\n=== ���� ������ ===\n�������� �������� �����������. ���� � �� ���� ����� �� ������ � ������� ����� � ��������, ��� ����� \"�� �����\". ��������� \"�� �����\" ������ ���� ������ ������������� ���.\n")
	
	if dialogContext != nil && len(dialogContext.History) > 0 {
		promptBuilder.WriteString("\n��������: �������� ������� ������� ����. ���� ������������ ����� �������� ���������� ������ (��������, '������' ��� '� ���������?'), ��������� �������� ���������� ��������� ��� ���������, � ��� ������ ����������.\n")
	}
	
	prompt := promptBuilder.String()

	payload := OllamaGenerateRequest{
		Model:     model,
		Prompt:    prompt,
		System:    systemPrompt,
		Stream:    true,
		KeepAlive: "10m",
		Options: map[string]any{
			"temperature":    0.2,  // ������� � 0.5 ��� ���������� ����������� ������� (���������, ��������, ������)
			"top_p":          0.90,
			"repeat_penalty": 1.08,
			"num_ctx":        32768,
			"num_predict":    predictionLimit(question),
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	modelCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(
		modelCtx,
		http.MethodPost,
		cfg.OllamaURL+"/api/generate",
		strings.NewReader(string(body)),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	log.Printf("OLLAMA STREAM REQUEST: %s model=%s", cfg.OllamaURL+"/api/generate", model)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("������ ����������� � Ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		return fmt.Errorf("Ollama /api/generate HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var chunk OllamaGenerateResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			log.Printf("OLLAMA STREAM: ������ �������: %v", err)
			continue
		}

		if chunk.Response != "" {
			if err := onChunk(chunk.Response); err != nil {
				return err
			}
		}

		if chunk.Done {
			break
		}
	}

	return scanner.Err()
}

// =========================================================
// LLM HELPER FUNCTIONS (moved to llm_provider.go)
// =========================================================

// =========================================================
// FALLBACK WITHOUT OLLAMA
// =========================================================

func fallbackAnswer(question string, contextText string) string {
	q := normalize(question)
	if containsAny(q, "������������", "���������", "��������") {
		return "�� ������� ��������� ��������� �� ��� ������� ������ ��������������. �������, ��� Ollama �������� � � ��� ����������� ������."
	}
	if isSpecialtyQuestion(q) {
		return "�� ������� ��������� ��������� �� ��� ������� ��������������. �������, ��� Ollama �������� � � ��� ����������� ������."
	}
	if strings.TrimSpace(contextText) == "" {
		return "�� ������� �������� ���������� � ����� ��������."
	}
	return "�� ������� ������������ �����. �������, ��� Ollama �������� � � ��� ����������� ������."
}

// =========================================================
// �������������: ����� ������
// =========================================================

func isGenericSpecialtyListQuestion(q string) bool {
	q = normalize(q)

	return q == "специальности" ||
		q == "список специальностей" ||
		q == "какие специальности" ||
		q == "какие специальности есть" ||
		q == "какие есть специальности" ||
		q == "какие направления есть" ||
		q == "направления" ||
		q == "список направлений"
}

func makeSpecialtyListAnswer() string {
	type specialty struct {
		Code          string
		Name          string
		Qualification string
	}

	items := []specialty{
		{Code: "40.02.01", Name: "����� � ����������� ����������� �����������", Qualification: "�����"},
		{Code: "40.02.04", Name: "�������������", Qualification: "�����"},
		{Code: "44.02.02", Name: "������������ � ��������� �������", Qualification: "������� ��������� �������"},
		{Code: "54.02.01", Name: "������", Qualification: "��������"},
	}

	var b strings.Builder
	b.WriteString("� �������� ������ ������������ ��������� �������������:\n\n")

	for i, item := range items {
		fmt.Fprintf(&b, "%d. %s �%s�\n������������: %s\n\n", i+1, item.Code, item.Name, item.Qualification)
	}

	return strings.TrimSpace(b.String())
}

// =========================================================
// ANSWERING
// =========================================================

func answerQuestion(ctx context.Context, cfg Config, sources []Source, question string) (string, []Source) {
	started := time.Now()

	q := normalize(question)

	// ����� ������ �������������� �� ���������� � LLM:
	// �� ������ ������ ��������� ��� 4 ���������.
	if isGenericSpecialtyListQuestion(q) {
		log.Printf("SPECIALTY LIST: direct structured answer")
		return makeSpecialtyListAnswer(), nil
	}

	// �������� ����
	if globalCacheManager != nil {
		indexVersion := GetIndexVersion(globalDB)
		cacheKey := BuildCacheKey(question, cfg.OllamaModel, nil, nil, indexVersion)
		cached, err := globalCacheManager.Get(cacheKey)
		if err != nil {
			log.Printf("Cache lookup error: %v", err)
		} else if cached != nil {
			log.Printf("CACHE HIT: %s (%d hits)", cacheKey.Hash()[:16], cached.HitCount)
			return cached.Answer, cached.Sources
		}
		log.Printf("CACHE MISS: %s", cacheKey.Hash()[:16])
	}

	if isNewsQuestion(q) {
		log.Printf("NEWS MODE: ���� ������ �������� NOMOS")
	}

	contextText, usedSources, err := loadContext(ctx, sources, question, cfg.CacheTime)
	if err != nil {
		log.Printf("������ ��������� ��������� NOMOS: %v", err)
		return "�� ������� �������� ���������� ���������� � ����� ��������. ������� ����������� � NOMOS.", nil
	}
	log.Printf("NOMOS CONTEXT READY: %d chars (%s)", len(contextText), time.Since(started).Round(time.Millisecond))

	answer, err := askOllama(ctx, cfg, question, contextText, nil) // nil = ��� ��������� ��� ������ �������
	if err != nil {
		log.Printf("������ Ollama: %v", err)
		return fallbackAnswer(question, contextText), usedSources
	}

	log.Printf("ANSWER READY: %s", time.Since(started).Round(time.Millisecond))
	
	// ���������� � ���
	if globalCacheManager != nil {
		indexVersion := GetIndexVersion(globalDB)
		cacheKey := BuildCacheKey(question, cfg.OllamaModel, nil, nil, indexVersion)
		if err := globalCacheManager.Put(cacheKey, answer, usedSources, nil); err != nil {
			log.Printf("Cache save error: %v", err)
		}
	}
	
	return answer, usedSources
}

// =========================================================
// API SERVER
// =========================================================

type Server struct {
	Config         Config
	Sources        []Source
	SessionManager *SessionManager
	LLMProvider    LLMProvider // Injected LLM provider
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC in handleChat: %v", r)
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Internal error: %v", r))
		}
	}()
	
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var request chatRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 2*1024*1024)).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	question := strings.TrimSpace(request.Message)
	if question == "" {
		question = strings.TrimSpace(request.Question)
	}
	if question == "" {
		question = strings.TrimSpace(request.Query)
	}
	if question == "" {
		writeJSONError(w, http.StatusBadRequest, "message is empty")
		return
	}

	// DEBUG: Проверяем кодировку вопроса
	log.Printf("🔍 handleChat: question length=%d, runes=%d", len(question), len([]rune(question)))
	
	// Получаем или создаем сессию
	sessionID, dialogContext := s.SessionManager.GetOrCreate(request.SessionID)
	
	log.Printf("Запрос [session=%s]: %s", sessionID, question)

	// ������ ���� ���� ���� ��� �� ����� ��������� ���������
	var cacheKey CacheKey
	if globalCacheManager != nil {
		indexVersion := GetIndexVersion(globalDB)
		cacheKey = BuildCacheKey(question, llmCacheModel(s.Config), dialogContext.ToContextStrings(), dialogContext.LastEntities, indexVersion)
		
		// ��������� ��� ����� ����������
		cached, err := globalCacheManager.Get(cacheKey)
		if err == nil && cached != nil {
			log.Printf("CACHE HIT: %s (hit %d, fragments: %v)", cacheKey.Hash()[:16], cached.HitCount, cached.FragmentIDs)
			
			// ��� cache hit ��������� ������ ������� ������, �� ��������� ����� �������
			// �����: �� �������� finalizeDialogTurn, �.�. ��� ������� ������������ � �������
			// ������ ���������� �������������� �����
			writeJSON(w, http.StatusOK, chatResponse{
				Reply:     cached.Answer,
				Sources:   cached.Sources,
				SessionID: sessionID,
			})
			return
		}
		if err != nil {
			log.Printf("Cache lookup error: %v", err)
		} else {
			log.Printf("CACHE MISS: %s", cacheKey.Hash()[:16])
		}
	}

	// ���������� ����� �������� ��������� � ���������� ������
	answer, sources, fragmentIDs, err := GenerateAnswer(r.Context(), s.Config, s.LLMProvider, question, &dialogContext)
	if err != nil {
		log.Printf("������ ��������� ������: %v", err)
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("������: %v", err))
		return
	}

	// ��������� �������� ������ ����� �������� ���������
	s.SessionManager.UpdateContext(sessionID, dialogContext)

	// ��������� � ��� ������ �������� ������ � fragmentIDs
	// ���������� �����: fragmentIDs == nil ��� unverified answers (��. GenerateAnswer)
	if globalCacheManager != nil && err == nil && len(fragmentIDs) > 0 {
		log.Printf("CACHE: Saving verified answer (fragmentIDs=%v)", fragmentIDs)
		if err := globalCacheManager.Put(cacheKey, answer, sources, fragmentIDs); err != nil {
			log.Printf("Cache save error: %v", err)
		}
	} else if len(fragmentIDs) == 0 {
		log.Printf("CACHE: NOT saving (answer not verified or no evidence)")
	}

	writeJSON(w, http.StatusOK, chatResponse{
		Reply:     answer,
		Sources:   sources,
		SessionID: sessionID,
	})
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var request chatRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 2*1024*1024)).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	question := strings.TrimSpace(request.Message)
	if question == "" {
		question = strings.TrimSpace(request.Question)
	}
	if question == "" {
		question = strings.TrimSpace(request.Query)
	}
	if question == "" {
		writeJSONError(w, http.StatusBadRequest, "������ ������")
		return
	}

	// �������� ��� ������� ������
	sessionID, dialogContext := s.SessionManager.GetOrCreate(request.SessionID)
	
	log.Printf("STREAM [session=%s]: %s", sessionID, question)

	// ��������� SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", s.Config.CORSOrigin)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// ���������� session_id �������
	sessionData := map[string]string{"session_id": sessionID}
	sessionJSON, _ := json.Marshal(sessionData)
	fmt.Fprintf(w, "event: session\ndata: %s\n\n", string(sessionJSON))
	flusher.Flush()

	// ������ ���� ���� ���� ��� �� ����� ��������� ���������
	var cacheKey CacheKey
	if globalCacheManager != nil {
		indexVersion := GetIndexVersion(globalDB)
		cacheKey = BuildCacheKey(question, llmCacheModel(s.Config), dialogContext.ToContextStrings(), dialogContext.LastEntities, indexVersion)
		
		// �������� ����
		cached, err := globalCacheManager.Get(cacheKey)
		if err == nil && cached != nil {
			log.Printf("CACHE HIT (stream): %s (hit %d)", cacheKey.Hash()[:16], cached.HitCount)
			fmt.Fprintf(w, "data: %s\n\n", jsonEscape(cached.Answer))
			flusher.Flush()
			
			if len(cached.Sources) > 0 {
				sourcesJSON, _ := json.Marshal(cached.Sources)
				fmt.Fprintf(w, "event: sources\ndata: %s\n\n", string(sourcesJSON))
				flusher.Flush()
			}
			
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		if err != nil {
			log.Printf("Cache lookup error: %v", err)
		} else {
			log.Printf("CACHE MISS (stream): %s", cacheKey.Hash()[:16])
		}
	}

	// ���������� ����� �������� � streaming � ���������� ������
	var collectedAnswer strings.Builder
	var streamSources []Source
	var streamFragmentIDs []int64
	var streamError error
	
	streamError = StreamAnswer(r.Context(), s.Config, s.LLMProvider, question, &dialogContext, 
		func(chunk string) error {
			collectedAnswer.WriteString(chunk)
			fmt.Fprintf(w, "data: %s\n\n", jsonEscape(chunk))
			flusher.Flush()
			return nil
		},
		func(sources []Source) error {
			streamSources = sources
			if len(sources) > 0 {
				sourcesJSON, _ := json.Marshal(sources)
				fmt.Fprintf(w, "event: sources\ndata: %s\n\n", string(sourcesJSON))
				flusher.Flush()
			}
			return nil
		},
		func(fragmentIDs []int64) error {
			streamFragmentIDs = fragmentIDs
			return nil
		},
	)

	if streamError != nil {
		log.Printf("Stream error: %v", streamError)
		fmt.Fprintf(w, "data: {\"error\": \"������ ��������� ������\"}\n\n")
		flusher.Flush()
		return
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	// ��������� �������� ������ ����� �������� ���������
	s.SessionManager.UpdateContext(sessionID, dialogContext)

	// ��������� � ��� ������ ���� �� ������� � ���� fragmentIDs
	// ��� streaming ����� �������� fragmentIDs �� answerContext
	if globalCacheManager != nil && streamError == nil && collectedAnswer.Len() > 0 && len(streamFragmentIDs) > 0 {
		if err := globalCacheManager.Put(cacheKey, collectedAnswer.String(), streamSources, streamFragmentIDs); err != nil {
			log.Printf("Cache save error: %v", err)
		}
	}
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func streamFromOllama(ctx context.Context, cfg Config, question, contextText string, w http.ResponseWriter, flusher http.Flusher) (string, error) {
	model, err := pickOllamaModel(ctx, cfg)
	if err != nil {
		return "", err
	}

	systemPrompt := buildAnswerInstructions(question)
	prompt := "������ ������������:\n" + question +
		"\n\n=== �������� � ����� �������� ===\n" + contextText +
		"\n\n=== ���� ������ ===\n�������� �������� �����������. ���� � �� ���� ����� �� ������ � ������� ����� � ��������, ��� ����� \"�� �����\". ��������� \"�� �����\" ������ ���� ������ ������������� ���.\n"

	payload := OllamaGenerateRequest{
		Model:     model,
		Prompt:    prompt,
		System:    systemPrompt,
		Stream:    true,
		KeepAlive: "10m",
		Options: map[string]any{
			"temperature":    0.2,  // ������� � 0.5 ��� ���������� ����������� ������� (���������, ��������, ������)
			"top_p":          0.90,
			"repeat_penalty": 1.08,
			"num_ctx":        32768,
			"num_predict":    predictionLimit(question),
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.OllamaURL+"/api/generate", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Ollama HTTP %d", resp.StatusCode)
	}

	var fullAnswer strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	
	for scanner.Scan() {
		line := scanner.Bytes()
		var chunk OllamaGenerateResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue
		}

		if chunk.Response != "" {
			fullAnswer.WriteString(chunk.Response)
			fmt.Fprintf(w, "data: %s\n\n", jsonEscape(chunk.Response))
			flusher.Flush()
		}

		if chunk.Done {
			break
		}
	}

	return fullAnswer.String(), scanner.Err()
}

// =========================================================
// STATIC FILES
// =========================================================

func staticFromRoots(prefix string, roots ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relative := strings.TrimPrefix(r.URL.Path, prefix)
		relative = filepath.Clean(relative)
		if relative == "." || relative == ".." {
			relative = "index.html"
		}
		if strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			http.NotFound(w, r)
			return
		}

		for _, root := range roots {
			candidate := filepath.Join(root, relative)
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() {
				continue
			}

			http.StripPrefix(prefix, http.FileServer(http.Dir(root))).ServeHTTP(w, r)
			return
		}

		http.NotFound(w, r)
	})
}

func withCORS(origin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

// =========================================================
// MAIN
// =========================================================


func runReindexCommand(cfg Config) {
	log.Println("=== ?? �������������� ������� � ������������ ����������� ===")
	log.Println()
	
	// ������������� ��
	dbPath := filepath.Join("data", "erudit.db")
	if err := os.MkdirAll("data", 0755); err != nil {
		log.Fatalf("? ������ �������� �����: %v", err)
	}

	db, err := InitDatabase(dbPath)
	if err != nil {
		log.Fatalf("? ������ ������������� ��: %v", err)
	}
	defer db.Close()

	log.Printf("?? ����: %s", dbPath)
	log.Println()

	// �������� ��� ��������
	rows, err := db.Query(`
		SELECT id, final_url, raw_html 
		FROM pages 
		WHERE raw_html IS NOT NULL AND raw_html != ''
		ORDER BY id
	`)
	if err != nil {
		log.Fatalf("? ������ ��������� �������: %v", err)
	}
	defer rows.Close()

	type PageInfo struct {
		ID      int64
		URL     string
		RawHTML string
	}

	var pages []PageInfo
	for rows.Next() {
		var p PageInfo
		if err := rows.Scan(&p.ID, &p.URL, &p.RawHTML); err != nil {
			log.Printf("??  ������ ������ ��������: %v", err)
			continue
		}
		pages = append(pages, p)
	}

	log.Printf("?? ������� ������� ��� ��������������: %d", len(pages))
	log.Println()

	success := 0
	failed := 0

	for i, page := range pages {
		log.Printf("[%d/%d] ��������������: %s (id=%d)", i+1, len(pages), page.URL, page.ID)

		// ������ HTML ���������
		doc, err := ParseHTMLStructure(page.RawHTML, page.URL)
		if err != nil {
			log.Printf("  ??  ������ �������� HTML: %v", err)
			failed++
			continue
		}

		// ������� ������ ���������
		_, err = db.Exec(`DELETE FROM fragments WHERE page_id = ?`, page.ID)
		if err != nil {
			log.Printf("  ??  ������ �������� ������ ����������: %v", err)
			failed++
			continue
		}

		// ������ ����������� ���������
		if err := CreateEnrichedFragments(db, page.ID, doc); err != nil {
			log.Printf("  ??  ������ �������� ����������� ����������: %v", err)
			failed++
			continue
		}

		// ��������� ���������� ��������� ����������
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM fragments WHERE page_id = ?`, page.ID).Scan(&count)
		log.Printf("  ? ������� %d ����������� ����������", count)
		success++
	}

	log.Println()
	log.Println("=== ���������� ===")
	log.Printf("? �������: %d", success)
	log.Printf("? ������: %d", failed)
	log.Println()
	log.Println("? �������������� ���������!")
}


func runCrawlCommand(cfg Config) {
	log.Println("=== ?? ������� ����� �������� ������ ===")
	log.Println()

	// ������ ����� ����� ������� crawl
	args := os.Args[2:]
	var url string
	maxDepth := 3
	maxPages := 200
	workers := 3

	// ������� ������� ������
	for i := 0; i < len(args); i++ {
		if args[i] == "-url" && i+1 < len(args) {
			url = args[i+1]
			i++
		} else if args[i] == "-depth" && i+1 < len(args) {
			fmt.Sscanf(args[i+1], "%d", &maxDepth)
			i++
		} else if args[i] == "-pages" && i+1 < len(args) {
			fmt.Sscanf(args[i+1], "%d", &maxPages)
			i++
		} else if args[i] == "-workers" && i+1 < len(args) {
			fmt.Sscanf(args[i+1], "%d", &workers)
			i++
		}
	}

	// ���� URL �� ������, ���������� �� �������
	if url == "" {
		url = cfg.NOMOSBase
	}

	log.Printf("?? URL: %s", url)
	log.Printf("?? ���������: �������=%d, �������=%d, ��������=%d", maxDepth, maxPages, workers)
	log.Println()

	// ������������� ��
	dbPath := filepath.Join("data", "erudit.db")
	if err := os.MkdirAll("data", 0755); err != nil {
		log.Fatalf("? ������ �������� �����: %v", err)
	}

	db, err := InitDatabase(dbPath)
	if err != nil {
		log.Fatalf("? ������ ������������� ��: %v", err)
	}
	defer db.Close()
	
	// ��������� ���������� ������ ��� ���������� ������
	globalDB = db

	log.Printf("?? ����: %s", dbPath)
	log.Println()

	// ������ � ��������� �������
	config := CrawlerConfig{
		MaxDepth:        maxDepth,
		MaxPages:        maxPages,
		MaxFileSize:     10 * 1024 * 1024,
		WorkerCount:     workers,
		RequestDelay:    500 * time.Millisecond,
		RequestTimeout:  30 * time.Second,
		AllowedHosts:    []string{},
		SkipExtensions:  []string{".jpg", ".jpeg", ".png", ".gif", ".css", ".js", ".xml", ".zip", ".rar", ".ico"},
		SkipPaths:       []string{"/bitrix/", "/upload/iblock/", "/local/", "/ajax/"},
		FollowRedirects: true,
		MaxRedirects:    5,
	}

	crawler := NewCrawler(db, url, config)

	log.Println("?? ������ ������...")
	log.Println()

	if err := crawler.Start(); err != nil {
		log.Fatalf("? ������ ������: %v", err)
	}

	log.Println()
	log.Println("? ����� �������� �������!")
}

var globalCacheManager *CacheManager


// =========================================================
// EMBEDDING COMMANDS
// =========================================================

func runSetupEmbeddingsCommand(cfg Config, modelName string) {
	log.Println("=== ?? ��������� ������ ����������� ===")
	log.Println()
	
	dbPath := filepath.Join("data", "erudit.db")
	if err := os.MkdirAll("data", 0755); err != nil {
		log.Fatalf("? ������ �������� �����: %v", err)
	}
	
	db, err := InitDatabase(dbPath)
	if err != nil {
		log.Fatalf("? ������ ������������� ��: %v", err)
	}
	defer db.Close()
	
	globalDB = db
	
	searchEngine, err := NewSearchEngine(db, cfg)
	if err != nil {
		log.Fatalf("? ������ �������� ���������� ������: %v", err)
	}
	
	log.Printf("?? �������� ������: %s", modelName)
	log.Printf("?? Ollama URL: %s", cfg.OllamaURL)
	
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	
	if err := searchEngine.SetupEmbeddings(ctx, modelName); err != nil {
		log.Fatalf("? ������ ���������: %v", err)
	}
	
	log.Println()
	log.Println("? ������ ����������� ���������!")
	log.Println("?? ������ ���������: erudit generate-embeddings")
}

func runGenerateEmbeddingsCommand(cfg Config, batchSize int) {
	log.Println("=== ?? ��������� ����������� ===")
	log.Println()
	
	dbPath := filepath.Join("data", "erudit.db")
	db, err := InitDatabase(dbPath)
	if err != nil {
		log.Fatalf("? ������ ������������� ��: %v", err)
	}
	defer db.Close()
	
	globalDB = db
	
	searchEngine, err := NewSearchEngine(db, cfg)
	if err != nil {
		log.Fatalf("? ������ �������� ���������� ������: %v", err)
	}
	
	if searchEngine.embeddingModel == "" {
		log.Fatalf("? ������ ����������� �� ���������. ���������: erudit setup-embeddings <model-name>")
	}
	
	log.Printf("?? ������: %s (�����������: %d)", searchEngine.embeddingModel, searchEngine.embeddingDim)
	log.Printf("?? ������ �����: %d", batchSize)
	log.Println()
	
	ctx := context.Background()
	
	// ���������� ���������� ��������
	totalGenerated := 0
	for {
		err := searchEngine.GenerateEmbeddings(ctx, batchSize)
		if err != nil {
			log.Fatalf("? ������ ���������: %v", err)
		}
		
		// ���������, �������� �� ��� ���������
		var remaining int
		err = db.QueryRow(`
			SELECT COUNT(*)
			FROM fragments f
			LEFT JOIN embeddings e ON f.id = e.fragment_id AND e.model_name = ?
			WHERE e.id IS NULL
		`, searchEngine.embeddingModel).Scan(&remaining)
		
		if err != nil || remaining == 0 {
			break
		}
		
		totalGenerated += batchSize
		log.Printf("? �������� ����������: %d", remaining)
		time.Sleep(1 * time.Second)
	}
	
	log.Println()
	log.Println("? ��� ���������� �������������!")
	log.Println("?? ��������� ����� �����������")
}

func runSearchStatusCommand(cfg Config) {
	log.Println("=== ?? ������ ���������� ������ ===")
	log.Println()
	
	dbPath := filepath.Join("data", "erudit.db")
	db, err := InitDatabase(dbPath)
	if err != nil {
		log.Fatalf("? ������ ������������� ��: %v", err)
	}
	defer db.Close()
	
	globalDB = db
	
	searchEngine, err := NewSearchEngine(db, cfg)
	if err != nil {
		log.Fatalf("? ������ �������� ���������� ������: %v", err)
	}
	
	mode := searchEngine.GetSearchMode()
	
	// ���������� �� ����������
	var fragmentCount, embeddingCount int
	db.QueryRow(`SELECT COUNT(*) FROM fragments`).Scan(&fragmentCount)
	db.QueryRow(`SELECT COUNT(*) FROM embeddings`).Scan(&embeddingCount)
	
	log.Printf("?? ����� ������: %s", mode)
	log.Printf("?? ���������� � �������: %d", fragmentCount)
	
	if searchEngine.embeddingModel != "" {
		log.Printf("?? ������ �����������: %s (�����������: %d)", searchEngine.embeddingModel, searchEngine.embeddingDim)
		log.Printf("?? �����������: %d (%.1f%%)", embeddingCount, float64(embeddingCount)/float64(fragmentCount)*100)

		log.Printf("?? ����������� �����: %.2f", searchEngine.minScoreThreshold)
	} else {
		log.Printf("??  ���������� �� ��������� (������ ����������� �����)")
		log.Printf("?? ��� ���������: erudit setup-embeddings nomic-embed-text")
	}
	
	// ���������� �� ���������
	var pageCount int
	db.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&pageCount)
	log.Printf("?? ������� ����������������: %d", pageCount)
	
	log.Println()
	log.Println("? ��������� ������ ��������")
}


var globalDB *sql.DB

func main() {
	cfg := loadConfig()
	
	// ��������� ������� ���������� ��������� ������
	if len(os.Args) > 1 {
		command := os.Args[1]
		
		// ������� crawl ��� ������� ��������
		if command == "crawl" {
			runCrawlCommand(cfg)
			return
		}
		
		// ������� reindex ��� �������������� �������������� ���� �������
		if command == "reindex" {
			runReindexCommand(cfg)
			return
		}
		
		// ������� setup-embeddings ��� ��������� ���������� ������
		if command == "setup-embeddings" {
			if len(os.Args) < 3 {
				log.Fatalf("Usage: erudit setup-embeddings <model-name>\nExample: erudit setup-embeddings nomic-embed-text")
			}
			runSetupEmbeddingsCommand(cfg, os.Args[2])
			return
		}
		
		// ������� generate-embeddings ��� ��������� ��������
		if command == "generate-embeddings" {
			batchSize := 100
			if len(os.Args) > 2 {
				fmt.Sscanf(os.Args[2], "%d", &batchSize)
			}
			runGenerateEmbeddingsCommand(cfg, batchSize)
			return
		}
		
		// ������� search-status ��� �������� ������� ������
		if command == "search-status" {
			runSearchStatusCommand(cfg)
			return
		}
		
		// ����� CLI - ���� ������ ��� ������� �������
		question := command
		
		// ������������� �� � ����
		dbPath := filepath.Join("data", "erudit.db")
		if err := os.MkdirAll("data", 0755); err != nil {
			log.Fatalf("Failed to create data dir: %v", err)
		}
		
		db, err := InitDatabase(dbPath)
		if err != nil {
			log.Fatalf("Failed to init database: %v", err)
		}
		defer db.Close()
		
		// ��������� ���������� ������ ��� ���������� ������
		globalDB = db
		
		// ������� ��������� ����������
		if err := InsertInitialSources(db, cfg.NOMOSBase); err != nil {
			log.Fatalf("Failed to insert initial sources: %v", err)
		}
		
		globalCacheManager = NewCacheManager(db)
		
		// ������������� LLM Provider ��� CLI ������
		var llmProvider LLMProvider
		if cfg.LLMProvider == "ollama" {
			llmProvider = NewOllamaProvider(cfg.OllamaURL, cfg.OllamaModel, cfg.LLMTimeout)
		} else if cfg.LLMProvider == "external" {
			if cfg.LLMBaseURL == "" || cfg.LLMAPIKey == "" || cfg.LLMModel == "" {
				log.Fatalf("LLM_PROVIDER=external requires LLM_BASE_URL, LLM_API_KEY, and LLM_MODEL")
			}
			llmProvider = NewExternalProvider(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMTimeout)
		} else {
			llmProvider = NewOllamaProvider(cfg.OllamaURL, cfg.OllamaModel, cfg.LLMTimeout)
		}
		
		// �������� �����
		answer, sources, _, err := GenerateAnswer(context.Background(), cfg, llmProvider, question, nil)
		if err != nil {
			log.Fatalf("������ ��������� ������: %v", err)
		}
		
		// ������� ���������
		fmt.Println(answer)
		if len(sources) > 0 {
			fmt.Println("\n���������:")
			for i, src := range sources {
				fmt.Printf("[%d] %s\n", i+1, src.Name)
			}
		}
		
		os.Exit(0)
	}
	
	// ������� ����� - ��������� ������
	sources := buildSources(cfg.NOMOSBase)

	// ������������� �� � ����
	dbPath := filepath.Join("data", "erudit.db")
	if err := os.MkdirAll("data", 0755); err != nil {
		log.Fatalf("Failed to create data dir: %v", err)
	}
	
	db, err := InitDatabase(dbPath)
	if err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}
	defer db.Close()
	
	// ��������� ���������� ������ ��� ���������� ������
	globalDB = db
	
	// ������� ��������� ����������
	if err := InsertInitialSources(db, cfg.NOMOSBase); err != nil {
		log.Fatalf("Failed to insert initial sources: %v", err)
	}
	
	globalCacheManager = NewCacheManager(db)
	log.Printf("Cache manager initialized")

	// Инициализация менеджера сессий
	sessionManager := NewSessionManager()
	log.Printf("Session manager initialized")
	
	// Загрузка fallback данных
	if err := loadFallbackData(); err != nil {
		log.Printf("⚠️ Warning: Failed to load fallback data: %v", err)
	}
	
	// Инициализация LLM Provider
	var llmProvider LLMProvider
	if cfg.LLMProvider == "ollama" {
		llmProvider = NewOllamaProvider(cfg.OllamaURL, cfg.OllamaModel, cfg.LLMTimeout)
		log.Printf("LLM Provider: ollama")
	} else if cfg.LLMProvider == "external" {
		if cfg.LLMBaseURL == "" || cfg.LLMAPIKey == "" || cfg.LLMModel == "" {
			log.Fatalf("LLM_PROVIDER=external requires LLM_BASE_URL, LLM_API_KEY, and LLM_MODEL")
		}
		llmProvider = NewExternalProvider(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMTimeout)
		log.Printf("LLM Provider: external (OpenAI-compatible)")
	} else {
		log.Fatalf("Unknown LLM_PROVIDER: %s (supported: external, ollama)", cfg.LLMProvider)
	}

	server := &Server{
		Config:         cfg,
		Sources:        sources,
		SessionManager: sessionManager,
		LLMProvider:    llmProvider,
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", server.handleHealth)
	mux.HandleFunc("/api/chat", server.handleChat)
	mux.HandleFunc("/api/chat/stream", server.handleChatStream)

	// ���� ������ ������� � ./widget, ����� � ./web/widget.
	mux.Handle("/widget/", staticFromRoots("/widget/", "widget", filepath.Join("web", "widget")))

	// ������� ��������� ����������� � ������ ����������� �������.
	mux.Handle("/admin/", staticFromRoots("/admin/", "admin", filepath.Join("web", "admin")))

	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	log.Println("========================================")
	log.Printf("Erudit backend running on :%s", cfg.Port)
	log.Printf("NOMOS: %s", cfg.NOMOSBase)
	
	// LLM Provider startup diagnostics
	log.Printf("LLM PROVIDER: %s", cfg.LLMProvider)
	log.Printf("LLM TIMEOUT: %s", cfg.LLMTimeout)
	if cfg.LLMProvider == "external" {
		if cfg.LLMBaseURL != "" {
			log.Printf("LLM BASE URL: %s", cfg.LLMBaseURL)
		} else {
			log.Printf("LLM BASE URL: NOT CONFIGURED")
		}
		if cfg.LLMModel != "" {
			log.Printf("LLM MODEL: %s", cfg.LLMModel)
		} else {
			log.Printf("LLM MODEL: NOT CONFIGURED")
		}
	} else if cfg.LLMProvider == "ollama" {
		log.Printf("Ollama: %s", cfg.OllamaURL)
		if cfg.OllamaModel == "" {
			log.Println("Ollama model: auto (������ ������������� ������)")
		} else {
			log.Printf("Ollama model: %s", cfg.OllamaModel)
		}
	}
	
	log.Printf("Widget: http://localhost:%s/widget/chat.html", cfg.Port)
	log.Printf("Chat API: http://localhost:%s/api/chat", cfg.Port)
	log.Println("========================================")

	if err := http.ListenAndServe(":"+cfg.Port, withCORS(cfg.CORSOrigin, mux)); err != nil {
		log.Fatal(err)
	}
}

