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
	SessionID string `json:"session_id,omitempty"` // Идентификатор сессии для сохранения контекста
}

type chatResponse struct {
	Reply     string   `json:"reply"`
	Sources   []Source `json:"sources,omitempty"`
	SessionID string   `json:"session_id,omitempty"` // Возвращаем session_id клиенту
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
	PublishedDate  string // Дата публикации материала (если известна)
}

func buildSources(base string) []Source {
	base = strings.TrimRight(base, "/")

	return []Source{
		// Основные страницы
		{Name: "Главная страница", URL: base + "/", Kind: "general"},
		{Name: "Поступающим", URL: base + "/abitur/postupayushchim/", Kind: "admission"},
		{Name: "Специальности (список)", URL: base + "/abitur/specialties/", Kind: "specialties"},
		
		// Официальная информация (Свед. об образовательной организации)
		{Name: "Образование", URL: base + "/sveden/education/", Kind: "education"},
		{Name: "Документы", URL: base + "/sveden/document/", Kind: "documents"},
		{Name: "Платные услуги", URL: base + "/sveden/paid_edu/", Kind: "payment"},
		{Name: "Материально-техническое обеспечение", URL: base + "/sveden/objects/", Kind: "facilities"},
		{Name: "Стипендии и поддержка", URL: base + "/sveden/grants/", Kind: "grants"},
		{Name: "Вакантные места", URL: base + "/sveden/vacant/", Kind: "admission"},
		
		// Сотрудники и преподаватели
		{Name: "Преподаватели — страница 1", URL: base + "/teachers/", Kind: "teachers"},
		{Name: "Преподаватели — страница 2", URL: base + "/teachers/?PAGEN_2=2", Kind: "teachers"},
		{Name: "Сотрудники (руководство)", URL: base + "/sveden/employees/", Kind: "employees"},
		
		// Специальности (детали)
		{Name: "Право и организация социального обеспечения", URL: base + "/abitur/specialties/40-02-01-pravo-i-organizatsiya-sotsialnogo-obespecheniya/", Kind: "specialty_detail"},
		{Name: "Юриспруденция", URL: base + "/abitur/specialties/40-02-04-yurisprudentsiya/", Kind: "specialty_detail"},
		{Name: "Преподавание в начальных классах", URL: base + "/abitur/specialties/44-02-02-prepodavanie-v-nachalnykh-klassakh/", Kind: "specialty_detail"},
		{Name: "Дизайн", URL: base + "/abitur/specialties/54-02-01-dizayn/", Kind: "specialty_detail"},
		
		// Студентам (ВОССТАНОВЛЕНО)
		{Name: "Студентам", URL: base + "/students/", Kind: "students"},
		{Name: "Практика и стажировка", URL: base + "/students/practice/", Kind: "practice"},
		{Name: "Расписание занятий", URL: base + "/students/schedule/", Kind: "schedule"},
		{Name: "Образовательные ресурсы", URL: base + "/students/educational-resources/", Kind: "students"},
		{Name: "Пересдачи и академическая задолженность", URL: base + "/retake/", Kind: "retake"},
		
		// Новости (первые 10 страниц)
		{Name: "Новости", URL: base + "/press-center/news/", Kind: "news"},
		{Name: "Новости — стр. 2", URL: base + "/press-center/news/?PAGEN_1=2", Kind: "news"},
		{Name: "Новости — стр. 3", URL: base + "/press-center/news/?PAGEN_1=3", Kind: "news"},
		{Name: "Новости — стр. 4", URL: base + "/press-center/news/?PAGEN_1=4", Kind: "news"},
		{Name: "Новости — стр. 5", URL: base + "/press-center/news/?PAGEN_1=5", Kind: "news"},
		{Name: "Новости — стр. 6", URL: base + "/press-center/news/?PAGEN_1=6", Kind: "news"},
		{Name: "Новости — стр. 7", URL: base + "/press-center/news/?PAGEN_1=7", Kind: "news"},
		{Name: "Новости — стр. 8", URL: base + "/press-center/news/?PAGEN_1=8", Kind: "news"},
		{Name: "Новости — стр. 9", URL: base + "/press-center/news/?PAGEN_1=9", Kind: "news"},
		{Name: "Новости — стр. 10", URL: base + "/press-center/news/?PAGEN_1=10", Kind: "news"},
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

// Разбивает текст на семантические фрагменты с сохранением контекста
func splitIntoChunks(text string, source Source, chunkSize int) []Chunk {
	lines := strings.Split(text, "\n")
	var chunks []Chunk
	var currentChunk strings.Builder
	var chunkStart int
	lineIndex := 0
	
	// Буфер для сохранения заголовков специальностей
	var lastSpecialtyHeader string
	specialtyCodePattern := regexp.MustCompile(`(?i)(40\.02\.0[14]|44\.02\.02|54\.02\.01)`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		// Определяем, является ли строка заголовком специальности
		isSpecialtyLine := specialtyCodePattern.MatchString(line) && 
			(strings.Contains(strings.ToLower(line), "дизайн") ||
			 strings.Contains(strings.ToLower(line), "юриспруденци") ||
			 strings.Contains(strings.ToLower(line), "преподавани") ||
			 strings.Contains(strings.ToLower(line), "право"))
		
		if isSpecialtyLine {
			lastSpecialtyHeader = line
		}

		// Проверка переполнения chunk
		willOverflow := currentChunk.Len() > 0 && currentChunk.Len()+len(line) > chunkSize
		
		// Если строка содержит цену, НЕ разрывать chunk
		hasPriceInfo := strings.Contains(strings.ToLower(line), "стоимость") ||
			strings.Contains(strings.ToLower(line), "оплата") ||
			regexp.MustCompile(`\d{2,3}\s*\d{3}\s*руб`).MatchString(strings.ToLower(line))
		
		if willOverflow && !hasPriceInfo {
			// Сохраняем текущий chunk
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
			
			// Если был сохранён заголовок специальности, добавляем его в новый chunk
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

	// Сохраняем последний фрагмент
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

// Простой BM25-подобный скоринг фрагментов
func scoreChunk(chunk Chunk, terms []string) float64 {
	if len(terms) == 0 {
		return 0
	}

	text := normalize(chunk.Text)
	score := 0.0

	for _, term := range terms {
		// Подсчет вхождений термина
		count := float64(strings.Count(text, term))
		if count > 0 {
			// TF (term frequency) с насыщением
			tf := count / (count + 1.0)
			score += tf
		}
	}

	// Бонус за длину фрагмента (более длинные фрагменты предпочтительнее при равном скоре)
	lengthBonus := float64(len(chunk.Text)) / 10000.0
	score += lengthBonus * 0.1

	return score
}

// Поиск топ-N релевантных фрагментов
func findRelevantChunks(pages []CachedPage, question string, limit int) []Chunk {
	terms := questionTerms(normalize(question))
	if len(terms) == 0 {
		// Если нет ключевых слов, возвращаем первые N фрагментов
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

	// Разбиваем все страницы на фрагменты
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

	// Сортируем по убыванию скора
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Берем топ-N
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
	Timeout: 120 * time.Second, // Увеличено для Ollama (загрузка модели + генерация)
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

	// Конвертируем HTML в строку с учетом кодировки
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

// decodeHTML конвертирует байты HTML в строку UTF-8 с учетом кодировки
func decodeHTML(body []byte, contentType string) string {
	// Проверяем Content-Type на наличие charset
	contentType = strings.ToLower(contentType)
	
	// Сначала проверяем валидность UTF-8
	isValidUTF8 := utf8.Valid(body)
	
	// Пытаемся найти charset в Content-Type
	if strings.Contains(contentType, "charset=") {
		parts := strings.Split(contentType, "charset=")
		if len(parts) > 1 {
			charset := strings.TrimSpace(strings.Split(parts[1], ";")[0])
			log.Printf("Detected charset from Content-Type: %s", charset)
			
			// Если явно указан UTF-8
			if strings.Contains(charset, "utf-8") {
				if isValidUTF8 {
					log.Printf("UTF-8 validation passed, using as-is")
					return string(body)
				} else {
					// UTF-8 с ошибками - очищаем невалидные последовательности
					log.Printf("UTF-8 with invalid sequences, cleaning up")
					return strings.ToValidUTF8(string(body), "")
				}
			}
			
			// Если это windows-1251, конвертируем
			if strings.Contains(charset, "windows-1251") || strings.Contains(charset, "cp1251") {
				return decodeWindows1251(body)
			}
		}
	}
	
	// Проверяем на валидность UTF-8
	if isValidUTF8 {
		log.Printf("UTF-8 validation passed, using as-is")
		return string(body)
	}
	
	// Проверяем meta-тег для невалидного UTF-8
	checkLen := 2000
	if len(body) < checkLen {
		checkLen = len(body)
	}
	
	// Используем ToValidUTF8 для безопасного чтения начала
	htmlStart := strings.ToValidUTF8(string(body[:checkLen]), "")
	
	// <meta charset="...">
	charsetRe := regexp.MustCompile(`(?i)<meta[^>]+charset\s*=\s*["']?([^"'\s>]+)`)
	if matches := charsetRe.FindStringSubmatch(htmlStart); len(matches) > 1 {
		charset := strings.ToLower(matches[1])
		log.Printf("Detected charset from meta tag: %s", charset)
		
		if strings.Contains(charset, "utf-8") {
			// Meta говорит UTF-8, но есть невалидные байты - очищаем
			log.Printf("Meta says UTF-8, cleaning invalid sequences")
			return strings.ToValidUTF8(string(body), "")
		}
		
		if strings.Contains(charset, "windows-1251") || strings.Contains(charset, "cp1251") {
			return decodeWindows1251(body)
		}
	}
	
	// По умолчанию пробуем очистить как UTF-8
	log.Printf("No explicit charset found, trying UTF-8 cleanup")
	cleaned := strings.ToValidUTF8(string(body), "")
	
	// Если после очистки слишком много потерялось, пробуем windows-1251
	if len(cleaned) < len(body)*8/10 {
		log.Printf("Too much data lost in UTF-8 cleanup, trying windows-1251")
		return decodeWindows1251(body)
	}
	
	return cleaned
}

// decodeWindows1251 конвертирует windows-1251 в UTF-8
func decodeWindows1251(data []byte) string {
	// Таблица конвертации windows-1251 -> UTF-8 для русских букв
	buf := make([]rune, 0, len(data))
	
	for _, b := range data {
		switch {
		case b < 0x80:
			// ASCII без изменений
			buf = append(buf, rune(b))
		case b >= 0xC0 && b <= 0xDF:
			// А-Я: 0xC0-0xDF -> U+0410-U+042F
			buf = append(buf, rune(0x0410+int(b)-0xC0))
		case b >= 0xE0 && b <= 0xFF:
			// а-я: 0xE0-0xFF -> U+0430-U+044F
			buf = append(buf, rune(0x0430+int(b)-0xE0))
		case b == 0xA8:
			// Ё
			buf = append(buf, 'Ё')
		case b == 0xB8:
			// ё
			buf = append(buf, 'ё')
		default:
			// Остальные символы оставляем как есть
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
	s = strings.ReplaceAll(s, "ё", "е")
	// Убираем знаки препинания
	s = strings.NewReplacer(
		".", " ", ",", " ", "!", " ", "?", " ", ":", " ", ";", " ",
		"(", " ", ")", " ", "-", " ", "—", " ", "/", " ", "\"", " ", "'", " ",
	).Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// stem выполняет простой стемминг для русских слов (обрезка окончаний)
func stem(word string) string {
	if len(word) <= 3 {
		return word
	}
	
	// Список распространенных окончаний русского языка (от длинных к коротким)
	endings := []string{
		"ованиями", "ованиям", "ованием", "ованиях", "ованию", "ования", "ование",
		"остями", "остям", "остию", "остях", "ости", "ость",
		"иями", "иям", "иях", "ией", "ию", "ия",
		"ями", "ям", "ях", "ами", "ам", "ах",
		"ами", "ах", "ом", "ов", "ем", "ев",
		"ами", "ам", "ах", "ой", "ая", "ое", "ые", "ых", "ым", "ую",
		"ть", "ти", "чь", "сь",
		"ла", "ло", "ли", "лы", "ли",
		"ют", "ет", "ит", "ат", "ят",
		"ами", "ах", "ом", "ов", "ем", "ев", "ам", "ом", "ов",
		"и", "а", "о", "у", "ы", "е", "ю", "я", "й",
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
		"(", " ", ")", " ", "-", " ", "—", " ", "/", " ",
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
		"новост",
		"что нового",
		"последние события",
		"события в колледже",
		"последние новости",
		"свежие новости",
		"что произошло",
	)
}

func isTeacherQuestion(q string) bool {
	q = normalize(q)

	// Явные вопросы о преподавателях.
	if containsAny(q,
		"преподавател",
		"преподает",
		"преподаёт",
		"кто ведет",
		"кто ведёт",
		"кто преподает",
		"кто преподаёт",
		"учитель",
		"учителя",
		"педагог",
	) {
		return true
	}

	// «Кто + предмет» тоже считаем вопросом о преподавателе,
	// но сам предмет без «кто» больше не отправляем в этот режим.
	if strings.Contains(q, "кто") && containsAny(q,
		"информатик",
		"математик",
		"истори",
		"физик",
		"географ",
		"английск",
		"дизайн",
		"юриспруден",
	) {
		return true
	}

	return false
}

func isSpecialtyQuestion(q string) bool {
	return containsAny(q,
		"специальност",
		"специальность",
		"направлен",
		"направление",
		"40.02.01",
		"40.02.04",
		"44.02.02",
		"54.02.01",
		"юриспруден",
		"право и организация социального обеспечения",
		"социального обеспечения",
		"преподавание в начальных классах",
		"начальных классах",
		"дизайн",
	)
}

func sourceScore(source Source, q string) int {
	score := 0

	// Новости
	if isNewsQuestion(q) {
		if source.Kind == "news" {
			score += 150
		}
	}
	
	// Преподаватели
	if isTeacherQuestion(q) {
		if source.Kind == "teachers" {
			score += 100
		}
	}
	
	// Специальности
	if isSpecialtyQuestion(q) {
		if source.Kind == "specialties" || source.Kind == "specialty_detail" {
			score += 100
		}
	}
	
	// Поступление, стоимость, экзамены
	if containsAny(q, "поступ", "прием", "приемная", "документ", "стоим", "цен", "экзамен") {
		if source.Kind == "admission" {
			score += 100
		}
		// ИСПРАВЛЕНИЕ: если вопрос о стоимости конкретной специальности,
		// добавляем баллы карточке этой специальности
		if containsAny(q, "стоим", "цен") {
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
	
	// Образование
	if containsAny(q, "образован", "учебный план", "дисциплин", "предмет") {
		if source.Kind == "education" || source.Kind == "specialty_detail" {
			score += 60
		}
	}
	
	// Материально-техническое обеспечение
	if containsAny(q, "кабинет", "библиотек", "спорт", "оборудован", "здание") {
		if source.Kind == "facilities" {
			score += 100
		}
	}
	
	// Документы
	if containsAny(q, "документ", "приказ", "положение") {
		if source.Kind == "documents" {
			score += 100
		}
	}

	return score
}

func chooseSources(sources []Source, question string) []Source {
	q := normalize(question)

	// Новости: весь раздел новостей с пагинацией.
	if isNewsQuestion(q) {
		var result []Source
		for _, source := range sources {
			if source.Kind == "news" {
				result = append(result, source)
			}
		}
		return result
	}

	// Преподаватели: только две страницы преподавателей.

	if isTeacherQuestion(q) {
		var result []Source
		for _, source := range sources {
			if source.Kind == "teachers" {
				result = append(result, source)
			}
		}
		return result
	}

	// Специальности: если в вопросе названа конкретная специальность,
	// загружаем ТОЛЬКО её страницу + при необходимости индекс.
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

		// ИСПРАВЛЕНИЕ: если вопрос о стоимости конкретной специальности,
		// добавляем страницу "Поступающим" где есть все цены
		if containsAny(q, "стоим", "цен") && len(result) > 0 {
			for _, source := range sources {
				if source.Kind == "admission" {
					result = append(result, source)
					break
				}
			}
		}

		// Если пользователь спрашивает «про специальности» без явного названия,
		// берём индекс и все четыре карточки.
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
		// Без уверенного совпадения берём главную и поступление,
		// а не случайные страницы.
		for _, source := range sources {
			if source.Kind == "general" || source.Kind == "admission" {
				scored = append(scored, scoredSource{source: source, score: 1})
			}
		}
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// ИСПРАВЛЕНИЕ: для общих вопросов о стоимости увеличиваем лимит,
	// чтобы захватить и "Поступающим" и карточки специальностей
	limit := 3
	if containsAny(q, "стоим", "цен") && !isSpecialtyQuestion(q) {
		limit = 6 // Поступающим + 4 специальности + запас
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
		{"40.02.01", "право и организация социального обеспечения"},
		{"40.02.04", "юриспруденция"},
		{"44.02.02", "преподавание в начальных классах"},
		{"54.02.01", "дизайн"},
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
		"кто": true, "что": true, "как": true, "какой": true, "какая": true, "какие": true,
		"есть": true, "можно": true, "ли": true, "на": true, "по": true, "для": true,
		"про": true, "расскажи": true, "подробно": true, "мне": true, "нужно": true,
		"колледж": true, "колледже": true,
		// УЛУЧШЕНИЕ: убираем "специальность", "преподаватель", "новости" из стоп-слов
	}

	seen := map[string]bool{}
	var result []string
	for _, word := range tokenize(q) {
		// УЛУЧШЕНИЕ: минимум 3 символа вместо 4
		if len([]rune(word)) < 3 || stop[word] || seen[word] {
			continue
		}
		seen[word] = true
		result = append(result, word)
	}
	
	// УЛУЧШЕНИЕ: добавляем словоформы и синонимы
	expanded := expandTerms(result)
	return expanded
}

// expandTerms добавляет словоформы и синонимы для улучшения поиска
func expandTerms(terms []string) []string {
	synonyms := map[string][]string{
		"стоимост":      {"стоимост", "цен", "оплат", "тариф", "плат"},
		"цен":           {"цен", "стоимост", "оплат", "плат"},
		"оплат":         {"оплат", "стоимост", "цен", "плат"},
		"юриспруденци":  {"юриспруденци", "юрист", "юридическ", "прав"},
		"юрист":         {"юрист", "юриспруденци", "юридическ"},
		"дизайн":        {"дизайн", "дизайнер"},
		"дизайнер":      {"дизайнер", "дизайн"},
		"преподавани":   {"преподавани", "преподавател", "учител"},
		"преподавател":  {"преподавател", "преподавани", "учител"},
		"учител":        {"учител", "преподавател", "преподавани"},
		"медиацентр":    {"медиацентр", "медиа-центр", "медиа", "пресс-центр", "пресс"},
		"практик":       {"практик", "практическ", "производственн"},
	}
	
	seen := make(map[string]bool)
	var expanded []string
	
	for _, term := range terms {
		if !seen[term] {
			expanded = append(expanded, term)
			seen[term] = true
		}
		
		// Добавляем синонимы и словоформы
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
		return "", nil, errors.New("не выбраны источники NOMOS")
	}

	type result struct {
		page CachedPage
		err  error
	}

	// Сохраняем порядок selected: канал с конкурентными запросами давал
	// случайный порядок и из-за лимита контекста модель иногда видела только одну страницу.
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
			log.Printf("Источник не загрузился: %s: %v", item.page.Source.URL, item.err)
			continue
		}
		pages = append(pages, item.page)
	}

	if len(pages) == 0 {
		return "", nil, errors.New("не удалось загрузить ни одной страницы NOMOS")
	}

	questionLower := normalize(question)
	
	// Используем RAG (chunking + поиск) для улучшения релевантности
	useRAG := true
	if isNewsQuestion(questionLower) {
		// Для новостей лучше работает старый подход
		useRAG = false
	}

	var builder strings.Builder
	builder.WriteString("КОНТЕКСТ С ОФИЦИАЛЬНОГО САЙТА КОЛЛЕДЖА «НОМОС»\n\n")
	
	if useRAG {
		// RAG режим: находим релевантные фрагменты
		chunks := findRelevantChunks(pages, question, 15) // Топ-15 релевантных фрагментов
		
		builder.WriteString("Ниже представлены фрагменты материалов, релевантные вопросу пользователя.\n")
		builder.WriteString("Используй ТОЛЬКО факты из этих фрагментов для ответа.\n\n")
		
		seenSources := make(map[string]bool)
		totalChars := 0
		maxTotal := 80000
		
		for i, chunk := range chunks {
			if totalChars+len(chunk.Text) > maxTotal {
				break
			}
			
			fmt.Fprintf(&builder, "--- ФРАГМЕНТ %d: %s ---\n", i+1, chunk.Source.Name)
			builder.WriteString(chunk.Text)
			builder.WriteString("\n\n")
			
			totalChars += len(chunk.Text)
			seenSources[chunk.Source.URL] = true
		}
		
		// Собираем уникальные источники
		var usedSources []Source
		for _, page := range pages {
			if seenSources[page.Source.URL] {
				usedSources = append(usedSources, page.Source)
			}
		}
		
		log.Printf("RAG: использовано %d фрагментов, %d символов из %d источников", len(chunks), totalChars, len(usedSources))
		return builder.String(), usedSources, nil
	}

	// Старый режим: полные страницы
	builder.WriteString("ВАЖНО: блоки ниже относятся к вопросу пользователя; используй только релевантные факты.\n\n")

	maxPerPage := 20000  // Увеличено с 12000
	maxTotal := 80000    // Увеличено с 24000 для поддержки num_ctx=32768
	if isNewsQuestion(questionLower) {
		maxPerPage = 4000  // Увеличено с 2500
		maxTotal = 40000   // Увеличено с 30000
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

		fmt.Fprintf(&builder, "=== ИСТОЧНИК: %s ===\n", page.Source.Name)
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
		// Если это конкретная специальность, не режем страницу по одному ключевому слову:
		// важная информация может находиться далеко ниже.
		if specialtyMatchesQuestion(q, page.Source.Name) || containsAny(q, "подробно", "все", "сравни", "дисциплин", "практик", "квалификац", "срок") {
			return trimRunes(text, limit)
		}
	}

	return trimRunes(text, limit)
}

func focusNewsText(text, q string, limit int) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder

	dateRE := regexp.MustCompile(`^(0?[1-9]|[12]\d|3[01])\.(0?[1-9]|1[0-2])\.20\d\d$`)
	generic := q == "новости" || q == "последние новости" || q == "свежие новости" || q == "что нового"
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
			if !containsAny(normalize(line), "выберите", "следующая", "предыдущая", "select") {
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
	nameRE := regexp.MustCompile(`^[А-ЯЁ][а-яё-]+ [А-ЯЁ][а-яё]+ [А-ЯЁ][а-яё-]+$`)

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

	// Для конкретного предмета берём только совпавшие карточки.
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
		// Сжимаем карточку до сути: ФИО + строка предметов + контакты при наличии.
		lines := strings.Split(r.text, "\n")
		for _, line := range lines {
			lower := normalize(line)
			if line == r.name ||
				strings.Contains(lower, "преподаваемые учебные предметы") ||
				strings.Contains(lower, "контактный телефон") ||
				strings.Contains(lower, "адрес электронной почты") {
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
		q == "преподаватель" ||
		q == "список преподавателей" ||
		q == "какие преподаватели" ||
		q == "какие преподаватели есть" ||
		q == "кто работает преподавателем"
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
		return "", fmt.Errorf("Ollama недоступна по %s: %w", cfg.OllamaURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Ollama /api/tags вернула HTTP %d", resp.StatusCode)
	}

	var tags OllamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", fmt.Errorf("не удалось прочитать список моделей Ollama: %w", err)
	}

	if len(tags.Models) == 0 {
		return "", errors.New("в Ollama нет установленной модели; установи модель командой ollama pull <имя>")
	}

	if cfg.OllamaModel != "" {
		for _, item := range tags.Models {
			if strings.EqualFold(item.Name, cfg.OllamaModel) {
				return item.Name, nil
			}
		}
		log.Printf("OLLAMA MODEL %q не найдена, использую %q", cfg.OllamaModel, tags.Models[0].Name)
	}

	return tags.Models[0].Name, nil
}

func buildAnswerInstructions(question string) string {
	q := normalize(question)
	mode := "общий вопрос"
	if isNewsQuestion(q) {
		mode = "новости"
	} else if isTeacherQuestion(q) {
		mode = "преподаватели"
	} else if isSpecialtyQuestion(q) {
		if isGenericSpecialtyListQuestion(q) {
			mode = "список специальностей"
		} else if containsAny(q, "сравни", "сравнение", "разница", "отличаются") {
			mode = "сравнение специальностей"
		} else {
			mode = "конкретная специальность"
		}
	}

	terms := questionTerms(q)

	var b strings.Builder
	b.WriteString(`Ты — Эрудит, помощник Воронежского колледжа «Номос».

СТИЛЬ ОТВЕТА:
- Отвечай ПОЛНО и ПО СУЩЕСТВУ вопроса
- НЕ добавляй вступления ("Привет!", "Конечно!", "Вы хотите узнать...")
- НЕ добавляй завершения ("Чем ещё помочь?", "Есть вопросы?")
- Обращайся на "вы"
- Без Markdown (**, ##, ---), без URL
- БЕЗ эмодзи (кроме офтопика)

🎯 КРИТИЧЕСКИ ВАЖНО - ОТВЕЧАЙ ТОЛЬКО НА ЗАДАННЫЙ ВОПРОС:
❌ НЕ добавляй информацию, которую НЕ СПРАШИВАЛИ
❌ Если спросили список специальностей → НЕ добавляй стоимость, директора, преподавателей
❌ Если спросили про директора → НЕ добавляй преподавателей, специальности
❌ Если спросили стоимость → отвечай только про стоимость
✅ Читай вопрос внимательно и отвечай ТОЛЬКО на то, что спрашивают

ПОЛНОТА ОТВЕТА:
- Контакты: указывай ВСЕ доступные способы связи (телефон, email, адрес)
- Адрес: указывай ПОЛНЫЙ адрес, не ограничивайся городом
- Стоимость: указывай сумму, период, форму обучения если есть в контексте
- Условия: перечисляй ВСЕ важные требования и документы

ОФТОПИК:
Если вопрос НЕ про колледж → краткий ответ + возврат к теме
Пример: "Погодой не занимаюсь 😊 Расскажу про поступление в колледж. Что интересует?"

ЯЗЫК: только русский (даже если вопрос на другом языке)

РАБОТА С КОНТЕКСТОМ — КРИТИЧЕСКИ ВАЖНО:
✅ Используй ТОЛЬКО информацию из предоставленного контекста
✅ Контекст содержит актуальные данные с сайта колледжа с датами загрузки
✅ Если в контексте есть несколько значений для одного факта (например, разные цены) — НЕ выбирай произвольное
✅ При конфликте данных укажи все варианты ИЛИ попроси уточнить у приёмной комиссии
✅ Если информация ЕСТЬ в контексте → отвечай ПРЯМО, БЕЗ "не нашёл"
❌ НИКОГДА: "не нашёл информации", а потом ответ — это противоречие!
✅ Если информации НЕТ в контексте: "Этой информации нет на сайте. Уточните в приёмной комиссии: +7 (473) 271-35-36"

СТОИМОСТЬ ОБУЧЕНИЯ — ОСОБЫЕ ПРАВИЛА:
- ВСЕГДА указывай КОД специальности (54.02.01, 40.02.04 и т.д.)
- Если указан учебный год или период — обязательно упомяни его
- Если в контексте нет информации о форме обучения (очная/заочная) — не додумывай
- При конфликте цен в разных документах: "Нашёл разную информацию. Уточните актуальную стоимость в приёмной комиссии: +7 (473) 271-35-36"

СПЕЦИАЛЬНОСТИ:
- "Дизайнер" / "дизайн" → специальность 54.02.01 Дизайн (по отраслям)
- "Юрист" / "юриспруденция" → специальность 40.02.04 Юриспруденция
- "Учитель начальных классов" / "преподавание" → 44.02.02 Преподавание в начальных классах
- "Право и социальное обеспечение" → 40.02.01 Право и организация социального обеспечения
- НЕ путай эти специальности между собой
- "Дизайн интерьера" — это желаемая профессия, связанная со специальностью "Дизайн"

ДАТА И АКТУАЛЬНОСТЬ:
- Предоставленный контекст содержит дату загрузки страницы
- Дата загрузки НЕ означает дату вступления документа в силу
- Если в контексте указан период (например, "2024-2025 учебный год"), обязательно упомяни его
- НЕ делай предположений об актуальности на основе даты загрузки

ЧАСТИЧНАЯ ИНФОРМАЦИЯ:
- Дай что есть БЕЗ "не нашёл"
- Укажи, чего не хватает
- Предложи контакты приёмной

ДИАЛОГ:
Короткий вопрос ("а стоимость?") → используй контекст предыдущих сообщений для понимания, о чём речь
НО: история диалога используется только для понимания вопроса
Предыдущие ответы бота НЕ являются источником фактов — всегда проверяй по контексту

ПРЕПОДАВАТЕЛИ:
- Перечисли ФИО из контекста
- Добавь предметы/должности если есть
- Не придумывай

ПРИМЕРЫ ПРАВИЛЬНЫХ ОТВЕТОВ:

Q: "Какие специальности?"
✅ "В колледже четыре специальности:
- 54.02.01 Дизайн (по отраслям)
- 40.02.04 Юриспруденция
- 44.02.02 Преподавание в начальных классах
- 40.02.01 Право и организация социального обеспечения"

Q: "Стоимость дизайна?"
Если в контексте есть: "Обучение на специальности 54.02.01 Дизайн (по отраслям) стоит [цена из контекста] рублей в год."
Если нет: "Актуальную стоимость обучения уточните в приёмной комиссии: +7 (473) 271-35-36"

Q: "Контакты колледжа?"
✅ Указывай ВСЕ найденные: телефон, email, адрес, часы работы
❌ НЕ ограничивайся только телефоном

Q: "Где расположен колледж?"
✅ Укажи ПОЛНЫЙ адрес: город, улица, номер дома
❌ НЕ ограничивайся только городом
`)

	fmt.Fprintf(&b, "\nРЕЖИМ ВОПРОСА: %s\n", mode)
	if len(terms) > 0 {
		fmt.Fprintf(&b, "КЛЮЧЕВЫЕ СЛОВА ВОПРОСА: %s\n", strings.Join(terms, ", "))
	}

	if isNewsQuestion(q) {
		b.WriteString(`
ДОПОЛНИТЕЛЬНО ДЛЯ НОВОСТЕЙ:
- Покажи несколько последних новостей с датами
- Указывай название и краткое содержание каждой новости
- Не придумывай детали, которых нет в контексте
`)
	}

	if isTeacherQuestion(q) {
		b.WriteString(`
ДОПОЛНИТЕЛЬНО ДЛЯ ПРЕПОДАВАТЕЛЕЙ:
- Перечисли всех преподавателей из контекста в формате: ФИО
- Если указаны предметы — добавь их после ФИО
- Используй ТОЛЬКО данные из контекста, не добавляй преподавателей "от себя"
`)
	}

	if isSpecialtyQuestion(q) {
		if isGenericSpecialtyListQuestion(q) {
			b.WriteString(`
ДОПОЛНИТЕЛЬНО ДЛЯ СПИСКА СПЕЦИАЛЬНОСТЕЙ:
- Покажи ТОЛЬКО список специальностей (название + код)
- НЕ добавляй стоимость, директора, преподавателей, документы
- Просто перечисли 4 специальности — и всё
`)
		} else if containsAny(q, "сравни", "сравнение", "разница", "отличаются") {
			b.WriteString(`
ДОПОЛНИТЕЛЬНО ДЛЯ СРАВНЕНИЯ:
- Сравни только названные специальности по критериям из контекста
`)
		} else {
			b.WriteString(`
ДОПОЛНИТЕЛЬНО ДЛЯ КОНКРЕТНОЙ СПЕЦИАЛЬНОСТИ:
- Отвечай только про специальность, которую спросил пользователь
- Не добавляй информацию про другие специальности
`)
		}
	}

	return b.String()
}

func predictionLimit(question string) int {
	q := normalize(question)
	if isNewsQuestion(q) {
		return 650
	}
	if isTeacherQuestion(q) {
		return 800 // Увеличен лимит для списка преподавателей
	}
	if containsAny(q, "подробно", "сравни", "сравнение", "все", "полный список") {
		return 700
	}
	if isSpecialtyQuestion(q) {
		return 500
	}
	return 450 // Увеличено с 350 для более полных ответов
}

func askOllama(ctx context.Context, cfg Config, question, contextText string, dialogContext *DialogContext) (string, error) {
	model, err := pickOllamaModel(ctx, cfg)
	if err != nil {
		return "", err
	}

	log.Printf("OLLAMA MODEL: %s", model)

	systemPrompt := buildAnswerInstructions(question)
	
	// Формируем промпт с историей диалога
	var promptBuilder strings.Builder
	
	// Добавляем историю диалога если есть
	// КРИТИЧЕСКИ ВАЖНО: НЕ включаем предыдущие ответы модели (BotReply)
	// чтобы избежать распространения ошибок через историю
	if dialogContext != nil && len(dialogContext.History) > 0 {
		promptBuilder.WriteString("=== КОНТЕКСТ ДИАЛОГА (для понимания темы и follow-up вопросов) ===\n")
		
		// Берём последние 3 реплики
		start := len(dialogContext.History) - 3
		if start < 0 {
			start = 0
		}
		
		for i := start; i < len(dialogContext.History); i++ {
			turn := dialogContext.History[i]
			// Включаем только вопросы пользователя и тему
			promptBuilder.WriteString(fmt.Sprintf("Вопрос пользователя: %s\n", turn.UserMessage))
			if turn.Intent.Topic != "" && turn.Intent.Topic != "general" {
				promptBuilder.WriteString(fmt.Sprintf("Тема: %s\n", turn.Intent.Topic))
			}
			promptBuilder.WriteString("\n")
		}
		
		promptBuilder.WriteString("=== ТЕКУЩИЙ ВОПРОС ===\n")
	}
	
	promptBuilder.WriteString("ВОПРОС ПОЛЬЗОВАТЕЛЯ:\n" + question)
	promptBuilder.WriteString("\n\n=== КОНТЕКСТ С САЙТА КОЛЛЕДЖА (единственный источник фактов) ===\n" + contextText)
	promptBuilder.WriteString("\n\n=== ТВОЯ ЗАДАЧА ===\nПрочитай контекст внимательно. Если в нём есть ответ на вопрос — отвечай ПРЯМО и УВЕРЕННО, БЕЗ фразы \"не нашёл\". Используй \"не нашёл\" ТОЛЬКО если ответа действительно нет.\n")
	promptBuilder.WriteString("\nВСЕ ФАКТЫ (цены, даты, имена, телефоны, адреса) бери ТОЛЬКО из контекста выше. Используй контекст диалога ТОЛЬКО для понимания темы, а не как источник фактов.\n")
	
	// ВАЖНО: Если есть история - используй её для понимания контекста уточняющих вопросов
	if dialogContext != nil && len(dialogContext.History) > 0 {
		promptBuilder.WriteString("\nВНИМАНИЕ: Если пользователь задаёт короткий уточняющий вопрос (например, 'а стоимость?' после вопроса о специальности), используй контекст диалога выше для понимания, о чём именно спрашивают. Но факты всегда проверяй в контексте с сайта колледжа.\n")
	}
	
	prompt := promptBuilder.String()

	payload := OllamaGenerateRequest{
		Model:     model,
		Prompt:    prompt,
		System:    systemPrompt,
		Stream:    false,
		KeepAlive: "10m",
		Options: map[string]any{
			"temperature":    0.2,  // Снижено с 0.5 для стабильных фактических ответов (стоимость, контакты, адреса)
			"top_p":          0.90,
			"repeat_penalty": 1.08,
			"num_ctx":        32768, // Увеличено с 8192 для больших документов
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
		return "", fmt.Errorf("ошибка подключения к Ollama: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("OLLAMA HTTP: %d (%s)", resp.StatusCode, time.Since(started).Round(time.Millisecond))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		return "", fmt.Errorf("Ollama /api/generate HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var result OllamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("ошибка разбора ответа Ollama: %w", err)
	}

	answer := cleanLLMAnswer(result.Response)
	if answer == "" {
		return "", errors.New("Ollama вернула пустой ответ")
	}

	if result.DoneReason == "length" {
		log.Printf("OLLAMA: ответ достиг лимита генерации")
	}

	return ensureCompleteAnswer(answer), nil
}

// streamOllama выполняет streaming запрос к Ollama
func streamOllama(ctx context.Context, cfg Config, question, contextText string, dialogContext *DialogContext, onChunk func(string) error) error {
	model, err := pickOllamaModel(ctx, cfg)
	if err != nil {
		return err
	}

	systemPrompt := buildAnswerInstructions(question)
	
	// Формируем промпт с историей диалога (аналогично askOllama)
	var promptBuilder strings.Builder
	
	// Добавляем историю диалога если есть
	if dialogContext != nil && len(dialogContext.History) > 0 {
		promptBuilder.WriteString("=== ИСТОРИЯ ДИАЛОГА (последние сообщения) ===\n")
		
		start := len(dialogContext.History) - 3
		if start < 0 {
			start = 0
		}
		
		for i := start; i < len(dialogContext.History); i++ {
			turn := dialogContext.History[i]
			promptBuilder.WriteString(fmt.Sprintf("Пользователь: %s\n", turn.UserMessage))
			if turn.BotReply != "" {
				promptBuilder.WriteString(fmt.Sprintf("Ты ответил: %s\n", turn.BotReply))
			}
			promptBuilder.WriteString("\n")
		}
		
		promptBuilder.WriteString("=== ТЕКУЩИЙ ВОПРОС ===\n")
	}
	
	promptBuilder.WriteString("ВОПРОС ПОЛЬЗОВАТЕЛЯ:\n" + question)
	promptBuilder.WriteString("\n\n=== КОНТЕКСТ С САЙТА КОЛЛЕДЖА ===\n" + contextText)
	promptBuilder.WriteString("\n\n=== ТВОЯ ЗАДАЧА ===\nПрочитай контекст внимательно. Если в нём есть ответ на вопрос — отвечай ПРЯМО и УВЕРЕННО, БЕЗ фразы \"не нашёл\". Используй \"не нашёл\" ТОЛЬКО если ответа действительно нет.\n")
	
	if dialogContext != nil && len(dialogContext.History) > 0 {
		promptBuilder.WriteString("\nВНИМАНИЕ: Учитывай историю диалога выше. Если пользователь задаёт короткий уточняющий вопрос (например, 'дизайн' или 'а стоимость?'), используй контекст предыдущих сообщений для понимания, о чём именно спрашивают.\n")
	}
	
	prompt := promptBuilder.String()

	payload := OllamaGenerateRequest{
		Model:     model,
		Prompt:    prompt,
		System:    systemPrompt,
		Stream:    true,
		KeepAlive: "10m",
		Options: map[string]any{
			"temperature":    0.2,  // Снижено с 0.5 для стабильных фактических ответов (стоимость, контакты, адреса)
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
		return fmt.Errorf("ошибка подключения к Ollama: %w", err)
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
			log.Printf("OLLAMA STREAM: ошибка разбора: %v", err)
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
	if containsAny(q, "преподавател", "преподает", "преподаёт") {
		return "Не удалось запустить локальный ИИ для анализа данных преподавателей. Проверь, что Ollama запущена и в ней установлена модель."
	}
	if isSpecialtyQuestion(q) {
		return "Не удалось запустить локальный ИИ для анализа специальностей. Проверь, что Ollama запущена и в ней установлена модель."
	}
	if strings.TrimSpace(contextText) == "" {
		return "Не удалось получить информацию с сайта колледжа."
	}
	return "Не удалось сформировать ответ. Проверь, что Ollama запущена и в ней установлена модель."
}

// =========================================================
// СПЕЦИАЛЬНОСТИ: ОБЩИЙ СПИСОК
// =========================================================

func isGenericSpecialtyListQuestion(q string) bool {
	q = normalize(q)

	return q == "специальности" ||
		q == "специальность" ||
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
		{Code: "40.02.01", Name: "Право и организация социального обеспечения", Qualification: "Юрист"},
		{Code: "40.02.04", Name: "Юриспруденция", Qualification: "Юрист"},
		{Code: "44.02.02", Name: "Преподавание в начальных классах", Qualification: "Учитель начальных классов"},
		{Code: "54.02.01", Name: "Дизайн", Qualification: "Дизайнер"},
	}

	var b strings.Builder
	b.WriteString("В колледже «Номос» представлены следующие специальности:\n\n")

	for i, item := range items {
		fmt.Fprintf(&b, "%d. %s «%s»\nКвалификация: %s\n\n", i+1, item.Code, item.Name, item.Qualification)
	}

	return strings.TrimSpace(b.String())
}

// =========================================================
// ANSWERING
// =========================================================

func answerQuestion(ctx context.Context, cfg Config, sources []Source, question string) (string, []Source) {
	started := time.Now()

	q := normalize(question)

	// Общий список специальностей не отправляем в LLM:
	// он должен всегда содержать все 4 программы.
	if isGenericSpecialtyListQuestion(q) {
		log.Printf("SPECIALTY LIST: direct structured answer")
		return makeSpecialtyListAnswer(), nil
	}

	// Проверка кэша
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
		log.Printf("NEWS MODE: весь раздел новостей NOMOS")
	}

	contextText, usedSources, err := loadContext(ctx, sources, question, cfg.CacheTime)
	if err != nil {
		log.Printf("Ошибка получения контекста NOMOS: %v", err)
		return "Не удалось получить актуальную информацию с сайта колледжа. Проверь подключение к NOMOS.", nil
	}
	log.Printf("NOMOS CONTEXT READY: %d chars (%s)", len(contextText), time.Since(started).Round(time.Millisecond))

	answer, err := askOllama(ctx, cfg, question, contextText, nil) // nil = нет контекста для старой функции
	if err != nil {
		log.Printf("Ошибка Ollama: %v", err)
		return fallbackAnswer(question, contextText), usedSources
	}

	log.Printf("ANSWER READY: %s", time.Since(started).Round(time.Millisecond))
	
	// Сохранение в кэш
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

	// Получаем или создаем сессию
	sessionID, dialogContext := s.SessionManager.GetOrCreate(request.SessionID)
	
	log.Printf("Вопрос [session=%s]: %s", sessionID, question)

	// Создаём ключ кэша ОДИН РАЗ до любых изменений контекста
	var cacheKey CacheKey
	if globalCacheManager != nil {
		indexVersion := GetIndexVersion(globalDB)
		cacheKey = BuildCacheKey(question, s.Config.OllamaModel, dialogContext.ToContextStrings(), dialogContext.LastEntities, indexVersion)
		
		// Проверяем кэш перед обработкой
		cached, err := globalCacheManager.Get(cacheKey)
		if err == nil && cached != nil {
			log.Printf("CACHE HIT: %s (hit %d, fragments: %v)", cacheKey.Hash()[:16], cached.HitCount, cached.FragmentIDs)
			
			// При cache hit обновляем ТОЛЬКО текущую сессию, не переносим чужую историю
			// Важно: не вызываем finalizeDialogTurn, т.к. это вызовет дублирование в истории
			// Просто возвращаем закешированный ответ
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

	// Используем новый механизм обработки с контекстом сессии
	answer, sources, fragmentIDs, err := GenerateAnswer(r.Context(), s.Config, s.LLMProvider, question, &dialogContext)
	if err != nil {
		log.Printf("Ошибка генерации ответа: %v", err)
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Ошибка: %v", err))
		return
	}

	// Обновляем контекст сессии ПОСЛЕ успешной генерации
	s.SessionManager.UpdateContext(sessionID, dialogContext)

	// Сохраняем в кэш ТОЛЬКО успешные ответы с fragmentIDs
	// КРИТИЧЕСКИ ВАЖНО: fragmentIDs == nil для unverified answers (см. GenerateAnswer)
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
		writeJSONError(w, http.StatusBadRequest, "пустой вопрос")
		return
	}

	// Получаем или создаем сессию
	sessionID, dialogContext := s.SessionManager.GetOrCreate(request.SessionID)
	
	log.Printf("STREAM [session=%s]: %s", sessionID, question)

	// Настройка SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", s.Config.CORSOrigin)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Отправляем session_id клиенту
	sessionData := map[string]string{"session_id": sessionID}
	sessionJSON, _ := json.Marshal(sessionData)
	fmt.Fprintf(w, "event: session\ndata: %s\n\n", string(sessionJSON))
	flusher.Flush()

	// Создаём ключ кэша ОДИН РАЗ до любых изменений контекста
	var cacheKey CacheKey
	if globalCacheManager != nil {
		indexVersion := GetIndexVersion(globalDB)
		cacheKey = BuildCacheKey(question, s.Config.OllamaModel, dialogContext.ToContextStrings(), dialogContext.LastEntities, indexVersion)
		
		// Проверка кэша
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

	// Используем новый механизм с streaming и контекстом сессии
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
		fmt.Fprintf(w, "data: {\"error\": \"Ошибка генерации ответа\"}\n\n")
		flusher.Flush()
		return
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	// Обновляем контекст сессии ПОСЛЕ успешной генерации
	s.SessionManager.UpdateContext(sessionID, dialogContext)

	// Сохраняем в кэш ТОЛЬКО если всё успешно и есть fragmentIDs
	// Для streaming нужно получить fragmentIDs из answerContext
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
	prompt := "ВОПРОС ПОЛЬЗОВАТЕЛЯ:\n" + question +
		"\n\n=== КОНТЕКСТ С САЙТА КОЛЛЕДЖА ===\n" + contextText +
		"\n\n=== ТВОЯ ЗАДАЧА ===\nПрочитай контекст внимательно. Если в нём есть ответ на вопрос — отвечай ПРЯМО и УВЕРЕННО, БЕЗ фразы \"не нашёл\". Используй \"не нашёл\" ТОЛЬКО если ответа действительно нет.\n"

	payload := OllamaGenerateRequest{
		Model:     model,
		Prompt:    prompt,
		System:    systemPrompt,
		Stream:    true,
		KeepAlive: "10m",
		Options: map[string]any{
			"temperature":    0.2,  // Снижено с 0.5 для стабильных фактических ответов (стоимость, контакты, адреса)
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


func runCrawlCommand(cfg Config) {
	log.Println("=== 🔄 Краулер сайта колледжа «Номос» ===")
	log.Println()

	// Парсим флаги после команды crawl
	args := os.Args[2:]
	var url string
	maxDepth := 3
	maxPages := 200
	workers := 3

	// Простой парсинг флагов
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

	// Если URL не указан, используем из конфига
	if url == "" {
		url = cfg.NOMOSBase
	}

	log.Printf("📍 URL: %s", url)
	log.Printf("🔍 Параметры: глубина=%d, страниц=%d, воркеров=%d", maxDepth, maxPages, workers)
	log.Println()

	// Инициализация БД
	dbPath := filepath.Join("data", "erudit.db")
	if err := os.MkdirAll("data", 0755); err != nil {
		log.Fatalf("❌ Ошибка создания папки: %v", err)
	}

	db, err := InitDatabase(dbPath)
	if err != nil {
		log.Fatalf("❌ Ошибка инициализации БД: %v", err)
	}
	defer db.Close()
	
	// Сохраняем глобальную ссылку для гибридного поиска
	globalDB = db

	log.Printf("💾 База: %s", dbPath)
	log.Println()

	// Создаём и запускаем краулер
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

	log.Println("🚀 Запуск обхода...")
	log.Println()

	if err := crawler.Start(); err != nil {
		log.Fatalf("❌ Ошибка обхода: %v", err)
	}

	log.Println()
	log.Println("✅ Обход завершён успешно!")
}

var globalCacheManager *CacheManager


// =========================================================
// EMBEDDING COMMANDS
// =========================================================

func runSetupEmbeddingsCommand(cfg Config, modelName string) {
	log.Println("=== 🔧 Настройка модели эмбеддингов ===")
	log.Println()
	
	dbPath := filepath.Join("data", "erudit.db")
	if err := os.MkdirAll("data", 0755); err != nil {
		log.Fatalf("❌ Ошибка создания папки: %v", err)
	}
	
	db, err := InitDatabase(dbPath)
	if err != nil {
		log.Fatalf("❌ Ошибка инициализации БД: %v", err)
	}
	defer db.Close()
	
	globalDB = db
	
	searchEngine, err := NewSearchEngine(db, cfg)
	if err != nil {
		log.Fatalf("❌ Ошибка создания поискового движка: %v", err)
	}
	
	log.Printf("🔍 Проверка модели: %s", modelName)
	log.Printf("📡 Ollama URL: %s", cfg.OllamaURL)
	
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	
	if err := searchEngine.SetupEmbeddings(ctx, modelName); err != nil {
		log.Fatalf("❌ Ошибка настройки: %v", err)
	}
	
	log.Println()
	log.Println("✅ Модель эмбеддингов настроена!")
	log.Println("💡 Теперь запустите: erudit generate-embeddings")
}

func runGenerateEmbeddingsCommand(cfg Config, batchSize int) {
	log.Println("=== 🧮 Генерация эмбеддингов ===")
	log.Println()
	
	dbPath := filepath.Join("data", "erudit.db")
	db, err := InitDatabase(dbPath)
	if err != nil {
		log.Fatalf("❌ Ошибка инициализации БД: %v", err)
	}
	defer db.Close()
	
	globalDB = db
	
	searchEngine, err := NewSearchEngine(db, cfg)
	if err != nil {
		log.Fatalf("❌ Ошибка создания поискового движка: %v", err)
	}
	
	if searchEngine.embeddingModel == "" {
		log.Fatalf("❌ Модель эмбеддингов не настроена. Запустите: erudit setup-embeddings <model-name>")
	}
	
	log.Printf("📊 Модель: %s (размерность: %d)", searchEngine.embeddingModel, searchEngine.embeddingDim)
	log.Printf("📦 Размер батча: %d", batchSize)
	log.Println()
	
	ctx := context.Background()
	
	// Генерируем эмбеддинги порциями
	totalGenerated := 0
	for {
		err := searchEngine.GenerateEmbeddings(ctx, batchSize)
		if err != nil {
			log.Fatalf("❌ Ошибка генерации: %v", err)
		}
		
		// Проверяем, остались ли ещё фрагменты
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
		log.Printf("⏳ Осталось фрагментов: %d", remaining)
		time.Sleep(1 * time.Second)
	}
	
	log.Println()
	log.Println("✅ Все эмбеддинги сгенерированы!")
	log.Println("💡 Гибридный поиск активирован")
}

func runSearchStatusCommand(cfg Config) {
	log.Println("=== 📊 Статус поискового движка ===")
	log.Println()
	
	dbPath := filepath.Join("data", "erudit.db")
	db, err := InitDatabase(dbPath)
	if err != nil {
		log.Fatalf("❌ Ошибка инициализации БД: %v", err)
	}
	defer db.Close()
	
	globalDB = db
	
	searchEngine, err := NewSearchEngine(db, cfg)
	if err != nil {
		log.Fatalf("❌ Ошибка создания поискового движка: %v", err)
	}
	
	mode := searchEngine.GetSearchMode()
	
	// Статистика по фрагментам
	var fragmentCount, embeddingCount int
	db.QueryRow(`SELECT COUNT(*) FROM fragments`).Scan(&fragmentCount)
	db.QueryRow(`SELECT COUNT(*) FROM embeddings`).Scan(&embeddingCount)
	
	log.Printf("🔍 Режим поиска: %s", mode)
	log.Printf("📄 Фрагментов в индексе: %d", fragmentCount)
	
	if searchEngine.embeddingModel != "" {
		log.Printf("🧮 Модель эмбеддингов: %s (размерность: %d)", searchEngine.embeddingModel, searchEngine.embeddingDim)
		log.Printf("📊 Эмбеддингов: %d (%.1f%%)", embeddingCount, float64(embeddingCount)/float64(fragmentCount)*100)
		log.Printf("⚖️  Веса: лексический=%.2f, семантический=%.2f", searchEngine.lexicalWeight, searchEngine.semanticWeight)
		log.Printf("🎯 Минимальный порог: %.2f", searchEngine.minScoreThreshold)
	} else {
		log.Printf("⚠️  Эмбеддинги не настроены (только лексический поиск)")
		log.Printf("💡 Для настройки: erudit setup-embeddings nomic-embed-text")
	}
	
	// Статистика по страницам
	var pageCount int
	db.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&pageCount)
	log.Printf("🌐 Страниц проиндексировано: %d", pageCount)
	
	log.Println()
	log.Println("✅ Поисковый движок работает")
}


var globalDB *sql.DB

func main() {
	cfg := loadConfig()
	
	// Проверяем наличие аргументов командной строки
	if len(os.Args) > 1 {
		command := os.Args[1]
		
		// Команда crawl для запуска краулера
		if command == "crawl" {
			runCrawlCommand(cfg)
			return
		}
		
		// Команда setup-embeddings для настройки векторного поиска
		if command == "setup-embeddings" {
			if len(os.Args) < 3 {
				log.Fatalf("Usage: erudit setup-embeddings <model-name>\nExample: erudit setup-embeddings nomic-embed-text")
			}
			runSetupEmbeddingsCommand(cfg, os.Args[2])
			return
		}
		
		// Команда generate-embeddings для генерации векторов
		if command == "generate-embeddings" {
			batchSize := 100
			if len(os.Args) > 2 {
				fmt.Sscanf(os.Args[2], "%d", &batchSize)
			}
			runGenerateEmbeddingsCommand(cfg, batchSize)
			return
		}
		
		// Команда search-status для проверки статуса поиска
		if command == "search-status" {
			runSearchStatusCommand(cfg)
			return
		}
		
		// Режим CLI - один вопрос без запуска сервера
		question := command
		
		// Инициализация БД и кэша
		dbPath := filepath.Join("data", "erudit.db")
		if err := os.MkdirAll("data", 0755); err != nil {
			log.Fatalf("Failed to create data dir: %v", err)
		}
		
		db, err := InitDatabase(dbPath)
		if err != nil {
			log.Fatalf("Failed to init database: %v", err)
		}
		defer db.Close()
		
		// Сохраняем глобальную ссылку для гибридного поиска
		globalDB = db
		
		// Вставка начальных источников
		if err := InsertInitialSources(db, cfg.NOMOSBase); err != nil {
			log.Fatalf("Failed to insert initial sources: %v", err)
		}
		
		globalCacheManager = NewCacheManager(db)
		
		// Инициализация LLM Provider для CLI режима
		var llmProvider LLMProvider
		if cfg.LLMProvider == "external" {
			if cfg.LLMBaseURL == "" || cfg.LLMAPIKey == "" || cfg.LLMModel == "" {
				log.Fatalf("LLM_PROVIDER=external requires LLM_BASE_URL, LLM_API_KEY, and LLM_MODEL")
			}
			llmProvider = NewExternalProvider(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMTimeout)
		} else {
			llmProvider = NewOllamaProvider(cfg.OllamaURL, cfg.OllamaModel, cfg.LLMTimeout)
		}
		
		// Получаем ответ
		answer, sources, _, err := GenerateAnswer(context.Background(), cfg, llmProvider, question, nil)
		if err != nil {
			log.Fatalf("Ошибка получения ответа: %v", err)
		}
		
		// Выводим результат
		fmt.Println(answer)
		if len(sources) > 0 {
			fmt.Println("\nИсточники:")
			for i, src := range sources {
				fmt.Printf("[%d] %s\n", i+1, src.Name)
			}
		}
		
		os.Exit(0)
	}
	
	// Обычный режим - запускаем сервер
	sources := buildSources(cfg.NOMOSBase)

	// Инициализация БД и кэша
	dbPath := filepath.Join("data", "erudit.db")
	if err := os.MkdirAll("data", 0755); err != nil {
		log.Fatalf("Failed to create data dir: %v", err)
	}
	
	db, err := InitDatabase(dbPath)
	if err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}
	defer db.Close()
	
	// Сохраняем глобальную ссылку для гибридного поиска
	globalDB = db
	
	// Вставка начальных источников
	if err := InsertInitialSources(db, cfg.NOMOSBase); err != nil {
		log.Fatalf("Failed to insert initial sources: %v", err)
	}
	
	globalCacheManager = NewCacheManager(db)
	log.Printf("Cache manager initialized")

	// Инициализация менеджера сессий
	sessionManager := NewSessionManager()
	log.Printf("Session manager initialized")
	
	// Инициализация LLM Provider
	var llmProvider LLMProvider
	if cfg.LLMProvider == "external" {
		if cfg.LLMBaseURL == "" || cfg.LLMAPIKey == "" || cfg.LLMModel == "" {
			log.Fatalf("LLM_PROVIDER=external requires LLM_BASE_URL, LLM_API_KEY, and LLM_MODEL")
		}
		llmProvider = NewExternalProvider(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMTimeout)
		log.Printf("LLM Provider: external (OpenAI-compatible)")
	} else if cfg.LLMProvider == "ollama" {
		llmProvider = NewOllamaProvider(cfg.OllamaURL, cfg.OllamaModel, cfg.LLMTimeout)
		log.Printf("LLM Provider: ollama")
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

	// Ищем виджет сначала в ./widget, затем в ./web/widget.
	mux.Handle("/widget/", staticFromRoots("/widget/", "widget", filepath.Join("web", "widget")))

	// Админку оставляем совместимой с обеими структурами проекта.
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
			log.Println("Ollama model: auto (первая установленная модель)")
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

