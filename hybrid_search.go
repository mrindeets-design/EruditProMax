package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// =========================================================
// HYBRID SEARCH: FTS5 + Embeddings
// =========================================================

// SearchEngine управляет гибридным поиском
type SearchEngine struct {
	db                *sql.DB
	cfg               Config
	embeddingModel    string
	embeddingDim      int
	lexicalWeight     float64
	semanticWeight    float64
	minScoreThreshold float64
}

// NewSearchEngine создает поисковый движок
func NewSearchEngine(db *sql.DB, cfg Config) (*SearchEngine, error) {
	se := &SearchEngine{
		db:                db,
		cfg:               cfg,
		lexicalWeight:     0.4,
		semanticWeight:    0.6,
		minScoreThreshold: 0.3,
	}

	// Загружаем конфигурацию из БД
	if err := se.loadConfig(); err != nil {
		log.Printf("Warning: failed to load search config: %v", err)
	}

	// Проверяем доступность FTS5
	if err := se.checkFTS5Support(); err != nil {
		return nil, fmt.Errorf("FTS5 not supported: %w", err)
	}

	return se, nil
}

// loadConfig загружает конфигурацию поиска из БД
func (se *SearchEngine) loadConfig() error {
	rows, err := se.db.Query(`SELECT key, value FROM search_config`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}

		switch key {
		case "embedding_model":
			se.embeddingModel = value
		case "embedding_dimension":
			fmt.Sscanf(value, "%d", &se.embeddingDim)
		case "lexical_weight":
			fmt.Sscanf(value, "%f", &se.lexicalWeight)
		case "semantic_weight":
			fmt.Sscanf(value, "%f", &se.semanticWeight)
		case "min_score_threshold":
			fmt.Sscanf(value, "%f", &se.minScoreThreshold)
		}
	}

	return nil
}

// checkFTS5Support проверяет поддержку FTS5
func (se *SearchEngine) checkFTS5Support() error {
	var name string
	err := se.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='fragments_fts'`).Scan(&name)
	if err == sql.ErrNoRows {
		return fmt.Errorf("fragments_fts table not found - run migration v2")
	}
	return err
}

// HybridSearchResult представляет результат поиска
type HybridSearchResult struct {
	FragmentID      int64
	Text            string
	PageID          *int64
	DocumentID      *int64
	URL             string
	Title           string
	LexicalScore    float64
	SemanticScore   float64
	CombinedScore   float64
	Version         int
	IndexedAt       string
	SpecialtyCode   *string
	SpecialtyName   *string
	PublishedDate   *string
}

// Search выполняет гибридный поиск
func (se *SearchEngine) Search(ctx context.Context, query string, limit int) ([]HybridSearchResult, error) {
	// Проверяем, есть ли данные в индексе
	var count int
	if err := se.db.QueryRow(`SELECT COUNT(*) FROM fragments`).Scan(&count); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("index is empty - run crawler first")
	}

	// Лексический поиск через FTS5
	lexicalResults, err := se.lexicalSearch(ctx, query, limit*3)
	if err != nil {
		return nil, fmt.Errorf("lexical search failed: %w", err)
	}

	// Семантический поиск через эмбеддинги (если доступен)
	var semanticResults []HybridSearchResult
	if se.embeddingModel != "" {
		semanticResults, err = se.semanticSearch(ctx, query, limit*3)
		if err != nil {
			log.Printf("Warning: semantic search failed: %v", err)
			// Продолжаем с лексическим поиском
		}
	}

	// Объединяем результаты
	combined := se.combineResults(lexicalResults, semanticResults)

	// Фильтруем по минимальному score
	filtered := []HybridSearchResult{}
	for _, r := range combined {
		if r.CombinedScore >= se.minScoreThreshold {
			filtered = append(filtered, r)
		}
	}

	// Сортируем по релевантности
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CombinedScore > filtered[j].CombinedScore
	})

	// Дедупликация по URL
	deduped := se.deduplicateByURL(filtered)

	// Ограничиваем результат
	if len(deduped) > limit {
		deduped = deduped[:limit]
	}

	return deduped, nil
}




// lexicalSearch выполняет FTS5 поиск
func (se *SearchEngine) lexicalSearch(ctx context.Context, query string, limit int) ([]HybridSearchResult, error) {
	// Подготавливаем запрос для FTS5
	ftsQuery := prepareFTS5Query(query)
	log.Printf("FTS5 query: original='%s' -> fts='%s'", query, ftsQuery)

	sqlQuery := `
		SELECT 
			f.id,
			f.text,
			f.page_id,
			f.document_id,
			f.version_number,
			COALESCE(p.url, d.url, '') as url,
			COALESCE(p.title, d.title, '') as title,
			COALESCE(p.last_check_at, d.last_check_at, '') as indexed_at,
			COALESCE(p.specialty_code, d.specialty_code, '') as specialty_code,
			p.specialty_name as specialty_name,
			COALESCE(p.published_date, d.published_date, '') as published_date,
			bm25(fragments_fts) as score
		FROM fragments_fts
		JOIN fragments f ON f.id = fragments_fts.rowid
		LEFT JOIN pages p ON f.page_id = p.id
		LEFT JOIN documents d ON f.document_id = d.id
		WHERE fragments_fts MATCH ?
		ORDER BY score
		LIMIT ?
	`

	rows, err := se.db.QueryContext(ctx, sqlQuery, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("FTS5 query failed: %w", err)
	}
	defer rows.Close()

	results := []HybridSearchResult{}
	for rows.Next() {
		var r HybridSearchResult
		var bm25Score float64

		err := rows.Scan(
			&r.FragmentID,
			&r.Text,
			&r.PageID,
			&r.DocumentID,
			&r.Version,
			&r.URL,
			&r.Title,
			&r.IndexedAt,
			&r.SpecialtyCode,
			&r.SpecialtyName,
			&r.PublishedDate,
			&bm25Score,
		)
		if err != nil {
			log.Printf("Warning: failed to scan row: %v", err)
			continue
		}

		// Нормализуем BM25 score в диапазон [0, 1]
		r.LexicalScore = normalizeBM25(bm25Score)
		r.CombinedScore = r.LexicalScore

		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return results, nil
}

// prepareFTS5Query подготавливает запрос для FTS5
func prepareFTS5Query(query string) string {
	// Удаляем специальные символы
	query = strings.Map(func(r rune) rune {
		if r == '"' || r == '*' || r == '(' || r == ')' || r == '?' || r == ':' {
			return -1
		}
		return r
	}, query)

	words := strings.Fields(query)
	if len(words) == 0 {
		return query
	}

	// Для точного поиска ФИО и кодов используем фразовый поиск
	if looksLikeNameOrCode(query) {
		return `"` + strings.Join(words, " ") + `"`
	}

	// Для слов >3 символов добавляем prefix matching
	parts := []string{}
	for _, word := range words {
		word = strings.TrimSpace(word)
		if len(word) > 3 {
			parts = append(parts, word+"*")
		} else {
			parts = append(parts, word)
		}
	}

	return strings.Join(parts, " OR ")
}

// looksLikeNameOrCode проверяет, похож ли запрос на ФИО или код
func looksLikeNameOrCode(query string) bool {
	// Коды специальностей вида 09.02.07
	if len(query) >= 8 && strings.Count(query, ".") >= 2 {
		return true
	}

	// ФИО: несколько слов с заглавных букв
	words := strings.Fields(query)
	if len(words) >= 2 {
		capitalCount := 0
		for _, w := range words {
			if len(w) > 0 {
				r, _ := utf8.DecodeRuneInString(w)
				if (r >= 'А' && r <= 'Я') || (r >= 'A' && r <= 'Z') {
					capitalCount++
				}
			}
		}
		if capitalCount >= 2 {
			return true
		}
	}

	return false
}

// normalizeBM25 нормализует BM25 score в диапазон [0, 1]
func normalizeBM25(score float64) float64 {
	normalized := 1.0 / (1.0 + math.Exp(score/5.0))
	return normalized
}

// semanticSearch выполняет векторный поиск
func (se *SearchEngine) semanticSearch(ctx context.Context, query string, limit int) ([]HybridSearchResult, error) {
	queryEmbedding, err := se.getQueryEmbedding(ctx, query)
	if err != nil {
		return nil, err
	}

	sqlQuery := `
		SELECT 
			f.id, f.text, f.page_id, f.document_id, f.version_number,
			COALESCE(p.url, d.url) as url,
			COALESCE(p.title, d.title) as title,
			COALESCE(p.last_check_at, d.last_check_at) as indexed_at,
			p.specialty_code, p.specialty_name,
			COALESCE(p.published_date, d.published_date) as published_date,
			e.embedding_blob
		FROM embeddings e
		JOIN fragments f ON e.fragment_id = f.id
		LEFT JOIN pages p ON f.page_id = p.id
		LEFT JOIN documents d ON f.document_id = d.id
		WHERE e.model_name = ? AND e.content_version = f.version_number
	`

	rows, err := se.db.QueryContext(ctx, sqlQuery, se.embeddingModel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []HybridSearchResult{}
	for rows.Next() {
		var r HybridSearchResult
		var embeddingBlob []byte

		err := rows.Scan(&r.FragmentID, &r.Text, &r.PageID, &r.DocumentID,
			&r.Version, &r.URL, &r.Title, &r.IndexedAt,
			&r.SpecialtyCode, &r.SpecialtyName, &r.PublishedDate, &embeddingBlob)
		if err != nil {
			continue
		}

		fragmentEmbedding := bytesToFloat32Array(embeddingBlob)
		if len(fragmentEmbedding) != len(queryEmbedding) {
			log.Printf("Warning: dimension mismatch for fragment %d", r.FragmentID)
			continue
		}

		similarity := cosineSimilarity(queryEmbedding, fragmentEmbedding)
		r.SemanticScore = similarity
		r.CombinedScore = similarity
		results = append(results, r)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].SemanticScore > results[j].SemanticScore
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results, rows.Err()
}

// getQueryEmbedding получает эмбеддинг через Ollama
func (se *SearchEngine) getQueryEmbedding(ctx context.Context, query string) ([]float32, error) {
	url := se.cfg.OllamaURL + "/api/embeddings"

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":  se.embeddingModel,
		"prompt": query,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var result struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Embedding, nil
}

// combineResults объединяет лексические и семантические результаты
func (se *SearchEngine) combineResults(lexical, semantic []HybridSearchResult) []HybridSearchResult {
	if len(semantic) == 0 {
		for i := range lexical {
			lexical[i].CombinedScore = lexical[i].LexicalScore
		}
		return lexical
	}

	resultMap := make(map[int64]*HybridSearchResult)

	for i := range lexical {
		resultMap[lexical[i].FragmentID] = &lexical[i]
	}

	for i := range semantic {
		if existing, ok := resultMap[semantic[i].FragmentID]; ok {
			existing.SemanticScore = semantic[i].SemanticScore
			existing.CombinedScore = se.lexicalWeight*existing.LexicalScore +
				se.semanticWeight*existing.SemanticScore
		} else {
			semantic[i].CombinedScore = se.semanticWeight * semantic[i].SemanticScore
			resultMap[semantic[i].FragmentID] = &semantic[i]
		}
	}

	combined := make([]HybridSearchResult, 0, len(resultMap))
	for _, r := range resultMap {
		combined = append(combined, *r)
	}

	return combined
}

// deduplicateByURL удаляет дубликаты по URL
func (se *SearchEngine) deduplicateByURL(results []HybridSearchResult) []HybridSearchResult {
	seen := make(map[string]bool)
	deduped := []HybridSearchResult{}

	for _, r := range results {
		// URL + первые 50 символов как ключ
		key := r.URL
		if len(r.Text) > 50 {
			key += "::" + r.Text[:50]
		} else {
			key += "::" + r.Text
		}

		if !seen[key] {
			seen[key] = true
			deduped = append(deduped, r)
		}
	}

	return deduped
}

// cosineSimilarity вычисляет косинусное сходство
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// bytesToFloat32Array преобразует BLOB в массив float32
func bytesToFloat32Array(data []byte) []float32 {
	if len(data)%4 != 0 {
		return nil
	}

	result := make([]float32, len(data)/4)
	for i := 0; i < len(result); i++ {
		bits := binary.LittleEndian.Uint32(data[i*4 : (i+1)*4])
		result[i] = math.Float32frombits(bits)
	}
	return result
}

// float32ArrayToBytes преобразует массив float32 в BLOB
func float32ArrayToBytes(arr []float32) []byte {
	data := make([]byte, len(arr)*4)
	for i, v := range arr {
		bits := math.Float32bits(v)
		binary.LittleEndian.PutUint32(data[i*4:(i+1)*4], bits)
	}
	return data
}

// SetupEmbeddings настраивает модель эмбеддингов
func (se *SearchEngine) SetupEmbeddings(ctx context.Context, modelName string) error {
	embedding, err := se.getQueryEmbedding(ctx, "test")
	if err != nil {
		return fmt.Errorf("embedding model not available: %w", err)
	}

	dimension := len(embedding)
	if dimension == 0 {
		return fmt.Errorf("invalid embedding dimension")
	}

	_, err = se.db.Exec(`
		INSERT OR REPLACE INTO search_config (key, value, updated_at) 
		VALUES 
			('embedding_model', ?, datetime('now')),
			('embedding_dimension', ?, datetime('now'))
	`, modelName, fmt.Sprintf("%d", dimension))

	if err != nil {
		return err
	}

	se.embeddingModel = modelName
	se.embeddingDim = dimension

	log.Printf("✅ Embedding model configured: %s (dim=%d)", modelName, dimension)
	return nil
}

// GenerateEmbeddings генерирует эмбеддинги для фрагментов
func (se *SearchEngine) GenerateEmbeddings(ctx context.Context, batchSize int) error {
	if se.embeddingModel == "" {
		return fmt.Errorf("embedding model not configured")
	}

	if batchSize <= 0 {
		batchSize = 100
	}

	rows, err := se.db.QueryContext(ctx, `
		SELECT f.id, f.text, f.version_number
		FROM fragments f
		LEFT JOIN embeddings e ON f.id = e.fragment_id AND e.model_name = ?
		WHERE e.id IS NULL
		LIMIT ?
	`, se.embeddingModel, batchSize)
	if err != nil {
		return err
	}
	defer rows.Close()

	type fragmentToEmbed struct {
		id      int64
		text    string
		version int
	}

	fragments := []fragmentToEmbed{}
	for rows.Next() {
		var f fragmentToEmbed
		if err := rows.Scan(&f.id, &f.text, &f.version); err != nil {
			continue
		}
		fragments = append(fragments, f)
	}

	if len(fragments) == 0 {
		log.Println("No fragments to embed")
		return nil
	}

	log.Printf("Generating embeddings for %d fragments...", len(fragments))

	for i, f := range fragments {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		embedding, err := se.getQueryEmbedding(ctx, f.text)
		if err != nil {
			log.Printf("Warning: failed to generate embedding for fragment %d: %v", f.id, err)
			continue
		}

		if len(embedding) != se.embeddingDim {
			log.Printf("Warning: dimension mismatch for fragment %d", f.id)
			continue
		}

		embeddingBlob := float32ArrayToBytes(embedding)
		_, err = se.db.Exec(`
			INSERT OR REPLACE INTO embeddings 
			(fragment_id, model_name, model_dimension, embedding_blob, content_version)
			VALUES (?, ?, ?, ?, ?)
		`, f.id, se.embeddingModel, se.embeddingDim, embeddingBlob, f.version)

		if err != nil {
			log.Printf("Warning: failed to save embedding for fragment %d: %v", f.id, err)
			continue
		}

		if (i+1)%10 == 0 {
			log.Printf("Progress: %d/%d embeddings generated", i+1, len(fragments))
		}

		time.Sleep(100 * time.Millisecond)
	}

	log.Printf("✅ Generated %d embeddings", len(fragments))
	return nil
}

// GetSearchMode возвращает текущий режим поиска
func (se *SearchEngine) GetSearchMode() string {
	if se.embeddingModel == "" {
		return "lexical"
	}
	
	var count int
	se.db.QueryRow(`SELECT COUNT(*) FROM embeddings WHERE model_name = ?`, se.embeddingModel).Scan(&count)
	if count == 0 {
		return "lexical"
	}
	
	return "hybrid"
}

// =========================================================
// INTEGRATION WITH HYBRID SEARCH
// =========================================================

// SearchMaterialsWithHybridEngine использует новый гибридный поиск
func SearchMaterialsWithHybridEngine(ctx context.Context, db *sql.DB, cfg Config, queries []SearchQuery, dialogContext *DialogContext) ([]SearchResult, error) {
	// Создаем поисковый движок
	searchEngine, err := NewSearchEngine(db, cfg)
	if err != nil {
		log.Printf("Warning: Hybrid search not available, using fallback: %v", err)
		return SearchMaterialsWithContext(ctx, cfg, queries, dialogContext)
	}

	mode := searchEngine.GetSearchMode()
	log.Printf("Search mode: %s", mode)

	allResults := []SearchResult{}
	
	for _, query := range queries {
		// Выполняем гибридный поиск
		hybridResults, err := searchEngine.Search(ctx, query.Text, 20)
		if err != nil {
			log.Printf("Warning: hybrid search failed: %v", err)
			// Fallback на старый метод
			return SearchMaterialsWithContext(ctx, cfg, queries, dialogContext)
		}

		// Конвертируем HybridSearchResult в SearchResult
		for _, hr := range hybridResults {
			sr := SearchResult{
				Fragment:   hr.Text,
				FragmentID: hr.FragmentID,
				Source: Source{
					URL:  hr.URL,
					Name: hr.Title,
				},
				Relevance: hr.CombinedScore,
				Date:      hr.IndexedAt,
				Metadata:  make(map[string]string),
			}

			// Добавляем метаданные
			if hr.SpecialtyCode != nil {
				sr.Metadata["specialty_code"] = *hr.SpecialtyCode
			}
			if hr.SpecialtyName != nil {
				sr.Metadata["specialty_name"] = *hr.SpecialtyName
			}
			if hr.PublishedDate != nil {
				sr.Metadata["published_date"] = *hr.PublishedDate
			}
			sr.Metadata["fragment_id"] = fmt.Sprintf("%d", hr.FragmentID)
			sr.Metadata["version"] = fmt.Sprintf("%d", hr.Version)
			sr.Metadata["lexical_score"] = fmt.Sprintf("%.3f", hr.LexicalScore)
			sr.Metadata["semantic_score"] = fmt.Sprintf("%.3f", hr.SemanticScore)

			allResults = append(allResults, sr)
		}
	}

	// Дедупликация и сортировка
	allResults = deduplicateSearchResults(allResults)
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Relevance > allResults[j].Relevance
	})

	// Ограничиваем результат
	if len(allResults) > 15 {
		allResults = allResults[:15]
	}

	log.Printf("Hybrid search found %d results", len(allResults))
	return allResults, nil
}

// deduplicateSearchResults удаляет дубликаты результатов
func deduplicateSearchResults(results []SearchResult) []SearchResult {
	seen := make(map[string]bool)
	unique := []SearchResult{}

	for _, r := range results {
		// Используем URL + первые 80 символов как ключ
		key := r.Source.URL
		if len(r.Fragment) > 80 {
			key += "::" + r.Fragment[:80]
		} else {
			key += "::" + r.Fragment
		}

		if !seen[key] {
			seen[key] = true
			unique = append(unique, r)
		}
	}

	return unique
}
