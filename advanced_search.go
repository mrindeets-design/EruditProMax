package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
)

// =========================================================
// ADVANCED HYBRID SEARCH: FTS5 + Semantic + RRF
// =========================================================

// SearchEngine управляет продвинутым гибридным поиском
type SearchEngine struct {
	db                *sql.DB
	cfg               Config
	embeddingModel    string
	embeddingDim      int
	minScoreThreshold float64
	rrfK              int // RRF parameter (typically 60)
}

// HybridSearchResult представляет результат поиска с расширенными метаданными
type HybridSearchResult struct {
	FragmentID      int64
	Text            string
	PageID          *int64
	DocumentID      *int64
	URL             string
	Title           string
	
	// Оценки из разных источников
	LexicalScore    float64
	SemanticScore   float64
	CombinedScore   float64
	
	// Ранжирование
	LexicalRank     int
	SemanticRank    int
	RRFScore        float64
	
	// Метаданные
	Version         int
	IndexedAt       string
	SpecialtyCode   *string
	SpecialtyName   *string
	PublishedDate   *string
	SectionTitle    *string
	ParentTitles    *string
	
	// Контекстная оценка
	ContextRelevance float64
}

// CandidatePool представляет пул кандидатов для ранжирования
type CandidatePool struct {
	Candidates    []HybridSearchResult
	Query         string
	DialogContext *DialogContext
}

// NewSearchEngine создает улучшенный поисковый движок
func NewSearchEngine(db *sql.DB, cfg Config) (*SearchEngine, error) {
	se := &SearchEngine{
		db:                db,
		cfg:               cfg,
		minScoreThreshold: 0.2, // Снижен для широкого первичного поиска
		rrfK:              60,  // Стандартное значение для RRF
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
		case "min_score_threshold":
			fmt.Sscanf(value, "%f", &se.minScoreThreshold)
		case "rrf_k":
			fmt.Sscanf(value, "%d", &se.rrfK)
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


// =========================================================
// MULTI-STAGE SEARCH PIPELINE
// =========================================================

// Search выполняет многоэтапный гибридный поиск
func (se *SearchEngine) Search(ctx context.Context, query string, dialogContext *DialogContext, limit int) ([]HybridSearchResult, error) {
	log.Printf("🔍 ADVANCED SEARCH: query='%s' limit=%d", query, limit)
	
	// ЭТАП 1: Широкий сбор кандидатов (retrieve)
	candidateLimit := limit * 10 // Собираем в 10 раз больше для последующей фильтрации
	candidates, err := se.gatherCandidates(ctx, query, candidateLimit)
	if err != nil {
		return nil, fmt.Errorf("candidate gathering failed: %w", err)
	}
	log.Printf("📊 Stage 1 (Retrieve): gathered %d candidates", len(candidates))
	
	if len(candidates) == 0 {
		return []HybridSearchResult{}, nil
	}
	
	// ЭТАП 2: Устранение дублей
	candidates = se.deduplicateResults(candidates)
	log.Printf("📊 Stage 2 (Dedup): %d unique candidates", len(candidates))
	
	// ЭТАП 3: Глубокое ранжирование с учетом контекста
	pool := &CandidatePool{
		Candidates:    candidates,
		Query:         query,
		DialogContext: dialogContext,
	}
	rankedResults := se.deepRanking(pool)
	log.Printf("📊 Stage 3 (Rank): ranked %d results", len(rankedResults))
	
	// ЭТАП 4: Отбор доказательных фрагментов
	evidentialResults := se.selectEvidentialFragments(rankedResults, query)
	log.Printf("📊 Stage 4 (Evidence): selected %d evidential fragments", len(evidentialResults))
	
	// ЭТАП 5: Финальная фильтрация по порогу
	finalResults := se.filterByThreshold(evidentialResults)
	log.Printf("📊 Stage 5 (Filter): %d results above threshold", len(finalResults))
	
	// ЭТАП 6: Ограничение Top-K
	if len(finalResults) > limit {
		finalResults = finalResults[:limit]
	}
	log.Printf("✅ FINAL: returning top-%d results", len(finalResults))
	
	return finalResults, nil
}

// gatherCandidates собирает кандидатов из лексического и семантического поиска
func (se *SearchEngine) gatherCandidates(ctx context.Context, query string, limit int) ([]HybridSearchResult, error) {
	// Лексический поиск (FTS5)
	lexicalResults, err := se.lexicalSearch(ctx, query, limit)
	if err != nil {
		log.Printf("Warning: lexical search failed: %v", err)
		lexicalResults = []HybridSearchResult{}
	}
	log.Printf("  📖 Lexical: %d results", len(lexicalResults))
	
	// Семантический поиск (если доступен)
	var semanticResults []HybridSearchResult
	if se.embeddingModel != "" {
		semanticResults, err = se.semanticSearch(ctx, query, limit)
		if err != nil {
			log.Printf("Warning: semantic search failed: %v", err)
			semanticResults = []HybridSearchResult{}
		}
		log.Printf("  🧠 Semantic: %d results", len(semanticResults))
	}
	
	// Объединяем результаты через RRF
	combined := se.combineWithRRF(lexicalResults, semanticResults)
	
	return combined, nil
}

// deduplicateResults удаляет дубликаты
func (se *SearchEngine) deduplicateResults(results []HybridSearchResult) []HybridSearchResult {
	seen := make(map[string]bool)
	unique := []HybridSearchResult{}
	
	for _, r := range results {
		// Используем URL + первые 100 символов как ключ
		key := r.URL
		textKey := r.Text
		if len(textKey) > 100 {
			textKey = textKey[:100]
		}
		key += "::" + textKey
		
		if !seen[key] {
			seen[key] = true
			unique = append(unique, r)
		}
	}
	
	return unique
}

// selectEvidentialFragments отбирает доказательные фрагменты
func (se *SearchEngine) selectEvidentialFragments(results []HybridSearchResult, query string) []HybridSearchResult {
	queryNorm := normalize(query)
	queryWords := extractKeywords(queryNorm)
	
	evidential := []HybridSearchResult{}
	
	for _, r := range results {
		textNorm := normalize(r.Text)
		
		// Проверяем наличие ключевых слов запроса
		matchCount := 0
		for _, kw := range queryWords {
			if strings.Contains(textNorm, kw) {
				matchCount++
			}
		}
		
		// Фрагмент доказательный, если содержит хотя бы 40% ключевых слов
		if len(queryWords) > 0 && float64(matchCount)/float64(len(queryWords)) >= 0.4 {
			evidential = append(evidential, r)
		}
	}
	
	// Если слишком строгий фильтр отсек все - берем top-N по оценке
	if len(evidential) == 0 && len(results) > 0 {
		limit := 5
		if len(results) < limit {
			limit = len(results)
		}
		evidential = results[:limit]
		log.Printf("  ⚠️  Evidence filter too strict, taking top-%d by score", limit)
	}
	
	return evidential
}

// filterByThreshold фильтрует результаты по порогу
func (se *SearchEngine) filterByThreshold(results []HybridSearchResult) []HybridSearchResult {
	filtered := []HybridSearchResult{}
	
	for _, r := range results {
		if r.CombinedScore >= se.minScoreThreshold {
			filtered = append(filtered, r)
		} else {
			log.Printf("  🚫 Filtered: score=%.3f < threshold=%.3f", r.CombinedScore, se.minScoreThreshold)
		}
	}
	
	return filtered
}


// =========================================================
// LEXICAL AND SEMANTIC SEARCH
// =========================================================

// lexicalSearch выполняет полнотекстовый поиск (FTS5)
func (se *SearchEngine) lexicalSearch(ctx context.Context, query string, limit int) ([]HybridSearchResult, error) {
	// Нормализуем запрос для FTS5
	searchQuery := se.prepareFTS5Query(query)
	
	// Используем BM25 для ранжирования
	sqlQuery := `
		SELECT 
			f.id,
			f.text,
			f.page_id,
			NULL as document_id,
			COALESCE(p.url, '') as url,
			COALESCE(p.title, '') as title,
			f.specialty_code,
			f.section_title,
			f.parent_titles,
			f.version,
			f.indexed_at,
			bm25(fragments_fts) as score
		FROM fragments_fts fts
		JOIN fragments f ON fts.rowid = f.id
		LEFT JOIN pages p ON f.page_id = p.id
		WHERE fragments_fts MATCH ?
		ORDER BY bm25(fragments_fts)
		LIMIT ?
	`
	
	rows, err := se.db.QueryContext(ctx, sqlQuery, searchQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	results := []HybridSearchResult{}
	rank := 1
	
	for rows.Next() {
		var r HybridSearchResult
		var score float64
		
		err := rows.Scan(
			&r.FragmentID,
			&r.Text,
			&r.PageID,
			&r.DocumentID,
			&r.URL,
			&r.Title,
			&r.SpecialtyCode,
			&r.SectionTitle,
			&r.ParentTitles,
			&r.Version,
			&r.IndexedAt,
			&score,
		)
		if err != nil {
			continue
		}
		
		// Нормализуем BM25 score (обычно отрицательный)
		r.LexicalScore = se.normalizeBM25Score(score)
		r.LexicalRank = rank
		rank++
		
		results = append(results, r)
	}
	
	return results, nil
}

// semanticSearch выполняет векторный поиск
func (se *SearchEngine) semanticSearch(ctx context.Context, query string, limit int) ([]HybridSearchResult, error) {
	queryEmbedding, err := se.getQueryEmbedding(ctx, query)
	if err != nil {
		return nil, err
	}
	
	// Получаем все фрагменты с эмбеддингами
	sqlQuery := `
		SELECT 
			f.id,
			f.text,
			f.page_id,
			NULL as document_id,
			COALESCE(p.url, '') as url,
			COALESCE(p.title, '') as title,
			f.specialty_code,
			f.section_title,
			f.parent_titles,
			f.version,
			f.indexed_at,
			fe.embedding
		FROM fragments f
		JOIN fragment_embeddings fe ON f.id = fe.fragment_id
		LEFT JOIN pages p ON f.page_id = p.id
		WHERE fe.embedding IS NOT NULL
	`
	
	rows, err := se.db.QueryContext(ctx, sqlQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	type candidate struct {
		result     HybridSearchResult
		similarity float64
	}
	
	candidates := []candidate{}
	
	for rows.Next() {
		var r HybridSearchResult
		var embeddingBytes []byte
		
		err := rows.Scan(
			&r.FragmentID,
			&r.Text,
			&r.PageID,
			&r.DocumentID,
			&r.URL,
			&r.Title,
			&r.SpecialtyCode,
			&r.SectionTitle,
			&r.ParentTitles,
			&r.Version,
			&r.IndexedAt,
			&embeddingBytes,
		)
		if err != nil {
			continue
		}
		
		embedding := bytesToFloat32Array(embeddingBytes)
		if embedding == nil {
			continue
		}
		
		similarity := cosineSimilarity(queryEmbedding, embedding)
		
		candidates = append(candidates, candidate{
			result:     r,
			similarity: similarity,
		})
	}
	
	// Сортируем по убыванию similarity
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].similarity > candidates[j].similarity
	})
	
	// Берем top-N
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	
	results := []HybridSearchResult{}
	for rank, c := range candidates {
		c.result.SemanticScore = c.similarity
		c.result.SemanticRank = rank + 1
		results = append(results, c.result)
	}
	
	return results, nil
}

// getQueryEmbedding получает эмбеддинг запроса
func (se *SearchEngine) getQueryEmbedding(ctx context.Context, query string) ([]float32, error) {
	if se.embeddingModel == "" {
		return nil, fmt.Errorf("embedding model not configured")
	}
	
	// TODO: Интегрировать с Ollama embeddings или другим сервисом
	// Пока возвращаем ошибку
	return nil, fmt.Errorf("embedding generation not implemented yet")
}

// prepareFTS5Query подготавливает запрос для FTS5
func (se *SearchEngine) prepareFTS5Query(query string) string {
	// Нормализуем и очищаем
	normalized := normalize(query)
	
	// Убираем стоп-слова
	stopWords := []string{"и", "или", "в", "на", "с", "по", "для", "о", "об", "к", "от"}
	words := strings.Fields(normalized)
	filtered := []string{}
	
	for _, word := range words {
		if len(word) < 2 {
			continue
		}
		isStop := false
		for _, stop := range stopWords {
			if word == stop {
				isStop = true
				break
			}
		}
		if !isStop {
			filtered = append(filtered, word)
		}
	}
	
	// Объединяем через OR для широкого поиска
	if len(filtered) == 0 {
		return normalized
	}
	
	return strings.Join(filtered, " OR ")
}


// =========================================================
// RRF FUSION AND RANKING
// =========================================================

// combineWithRRF объединяет результаты через Reciprocal Rank Fusion
func (se *SearchEngine) combineWithRRF(lexical, semantic []HybridSearchResult) []HybridSearchResult {
	resultMap := make(map[int64]*HybridSearchResult)
	
	// Добавляем лексические результаты
	for i := range lexical {
		lexical[i].RRFScore = 1.0 / float64(se.rrfK+lexical[i].LexicalRank)
		resultMap[lexical[i].FragmentID] = &lexical[i]
	}
	
	// Добавляем семантические результаты
	for i := range semantic {
		rrfContribution := 1.0 / float64(se.rrfK+semantic[i].SemanticRank)
		
		if existing, ok := resultMap[semantic[i].FragmentID]; ok {
			// Фрагмент есть в обоих списках - суммируем RRF scores
			existing.SemanticScore = semantic[i].SemanticScore
			existing.SemanticRank = semantic[i].SemanticRank
			existing.RRFScore += rrfContribution
		} else {
			// Только семантический результат
			semantic[i].RRFScore = rrfContribution
			resultMap[semantic[i].FragmentID] = &semantic[i]
		}
	}
	
	// Собираем результаты
	combined := make([]HybridSearchResult, 0, len(resultMap))
	for _, r := range resultMap {
		combined = append(combined, *r)
	}
	
	// Сортируем по RRF score
	sort.Slice(combined, func(i, j int) bool {
		return combined[i].RRFScore > combined[j].RRFScore
	})
	
	log.Printf("  🔀 RRF: combined %d unique results (k=%d)", len(combined), se.rrfK)
	
	return combined
}

// deepRanking выполняет глубокое ранжирование с учетом контекста
func (se *SearchEngine) deepRanking(pool *CandidatePool) []HybridSearchResult {
	for i := range pool.Candidates {
		// Базовая оценка из RRF
		baseScore := pool.Candidates[i].RRFScore
		
		// Контекстная оценка (анафора, история диалога)
		contextBoost := se.calculateContextRelevance(&pool.Candidates[i], pool.Query, pool.DialogContext)
		
		// Оценка метаданных (специальность, секция, дата)
		metadataBoost := se.calculateMetadataRelevance(&pool.Candidates[i], pool.Query)
		
		// Итоговая комбинированная оценка
		pool.Candidates[i].ContextRelevance = contextBoost
		pool.Candidates[i].CombinedScore = baseScore * (1.0 + contextBoost + metadataBoost)
	}
	
	// Сортируем по итоговой оценке
	sort.Slice(pool.Candidates, func(i, j int) bool {
		return pool.Candidates[i].CombinedScore > pool.Candidates[j].CombinedScore
	})
	
	return pool.Candidates
}

// calculateContextRelevance вычисляет контекстную релевантность
func (se *SearchEngine) calculateContextRelevance(result *HybridSearchResult, query string, dialogContext *DialogContext) float64 {
	if dialogContext == nil || len(dialogContext.History) == 0 {
		return 0.0
	}
	
	boost := 0.0
	queryNorm := normalize(query)
	textNorm := normalize(result.Text)
	
	// 1. Разрешение анафоры через контекст
	anaphoricWords := []string{"это", "там", "такой", "такая", "её", "его", "их", "а сколько", "а какие", "а когда", "а где"}
	for _, word := range anaphoricWords {
		if strings.Contains(queryNorm, word) {
			// Проверяем связь с предыдущей темой
			if dialogContext.CurrentTopic != "" {
				topicKeywords := getTopicKeywords(dialogContext.CurrentTopic)
				for _, kw := range topicKeywords {
					if strings.Contains(textNorm, kw) {
						boost += 0.2
						break
					}
				}
			}
			
			// Проверяем связь с entities из истории
			for _, entity := range dialogContext.LastEntities {
				entityNorm := normalize(entity)
				if strings.Contains(textNorm, entityNorm) {
					boost += 0.15
				}
			}
			break
		}
	}
	
	// 2. Связь со специальностью из контекста
	if result.SpecialtyCode != nil {
		if lastSpecCode, ok := dialogContext.LastEntities["specialty_code"]; ok {
			if *result.SpecialtyCode == lastSpecCode {
				boost += 0.3
			}
		}
	}
	
	return boost
}

// calculateMetadataRelevance вычисляет релевантность по метаданным
func (se *SearchEngine) calculateMetadataRelevance(result *HybridSearchResult, query string) float64 {
	boost := 0.0
	queryNorm := normalize(query)
	
	// 1. Совпадение специальности
	if result.SpecialtyCode != nil {
		// Проверяем явное упоминание кода в запросе
		if strings.Contains(queryNorm, *result.SpecialtyCode) {
			boost += 0.4
		}
		
		// Проверяем упоминание названия специальности
		specialtyNames := map[string]string{
			"54.02.01": "дизайн",
			"44.02.02": "преподавание",
			"40.02.04": "юриспруденц",
		}
		if name, ok := specialtyNames[*result.SpecialtyCode]; ok {
			if strings.Contains(queryNorm, name) {
				boost += 0.3
			}
		}
	}
	
	// 2. Совпадение заголовка секции
	if result.SectionTitle != nil {
		sectionNorm := normalize(*result.SectionTitle)
		queryWords := strings.Fields(queryNorm)
		sectionWords := strings.Fields(sectionNorm)
		
		matchCount := 0
		for _, qw := range queryWords {
			if len(qw) < 3 {
				continue
			}
			for _, sw := range sectionWords {
				if strings.Contains(sw, qw) || strings.Contains(qw, sw) {
					matchCount++
					break
				}
			}
		}
		
		if matchCount > 0 && len(queryWords) > 0 {
			boost += 0.2 * float64(matchCount) / float64(len(queryWords))
		}
	}
	
	return boost
}

// getTopicKeywords возвращает ключевые слова темы
func getTopicKeywords(topic string) []string {
	keywords := map[string][]string{
		"payment":   {"стоимость", "цена", "оплат", "деньги", "рубл"},
		"admission": {"поступ", "прием", "документ", "экзамен", "зачисл"},
		"staff":     {"преподава", "учитель", "директор", "педагог", "препод", "сотрудник", "руководитель", "завуч"},
		"contacts":  {"телефон", "адрес", "контакт"},
		"specialty": {"специальность", "направлен", "профес"},
	}
	
	if kws, ok := keywords[topic]; ok {
		return kws
	}
	return []string{}
}


// =========================================================
// HELPER FUNCTIONS
// =========================================================

// normalizeBM25Score нормализует BM25 score
func (se *SearchEngine) normalizeBM25Score(score float64) float64 {
	// BM25 обычно возвращает отрицательные значения
	// Преобразуем в положительный диапазон [0, 1]
	// Типичные значения: от -10 до 0
	normalized := 1.0 / (1.0 + math.Exp(score/5.0))
	return normalized
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

// GetSearchMode возвращает режим поиска
func (se *SearchEngine) GetSearchMode() string {
	if se.embeddingModel != "" {
		return "hybrid"
	}
	return "lexical"
}

// =========================================================
// INTEGRATION WITH MAIN SEARCH
// =========================================================

// SearchMaterialsWithAdvancedEngine использует продвинутый гибридный поиск
func SearchMaterialsWithAdvancedEngine(ctx context.Context, db *sql.DB, cfg Config, queries []SearchQuery, dialogContext *DialogContext) ([]SearchResult, error) {
	// Создаем улучшенный поисковый движок
	searchEngine, err := NewSearchEngine(db, cfg)
	if err != nil {
		log.Printf("Warning: Advanced search not available, using fallback: %v", err)
		return SearchMaterialsWithContext(ctx, cfg, queries, dialogContext)
	}

	log.Printf("🚀 Using ADVANCED SEARCH ENGINE (mode: %s)", searchEngine.GetSearchMode())
	allResults := []SearchResult{}
	
	for _, query := range queries {
		// Выполняем продвинутый поиск
		hybridResults, err := searchEngine.Search(ctx, query.Text, dialogContext, 10)
		if err != nil {
			log.Printf("Warning: advanced search failed: %v", err)
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

			// Добавляем расширенные метаданные
			if hr.SpecialtyCode != nil {
				sr.Metadata["specialty_code"] = *hr.SpecialtyCode
			}
			if hr.SectionTitle != nil {
				sr.Metadata["section_title"] = *hr.SectionTitle
			}
			if hr.ParentTitles != nil {
				sr.Metadata["parent_titles"] = *hr.ParentTitles
			}
			sr.Metadata["fragment_id"] = fmt.Sprintf("%d", hr.FragmentID)
			sr.Metadata["version"] = fmt.Sprintf("%d", hr.Version)
			sr.Metadata["lexical_score"] = fmt.Sprintf("%.3f", hr.LexicalScore)
			sr.Metadata["semantic_score"] = fmt.Sprintf("%.3f", hr.SemanticScore)
			sr.Metadata["rrf_score"] = fmt.Sprintf("%.3f", hr.RRFScore)
			sr.Metadata["context_boost"] = fmt.Sprintf("%.3f", hr.ContextRelevance)

			allResults = append(allResults, sr)
		}
	}

	// Финальная дедупликация
	allResults = deduplicateSearchResults(allResults)
	
	// Top-5 для минимизации токенов
	maxResults := 5
	if len(allResults) > maxResults {
		allResults = allResults[:maxResults]
	}

	log.Printf("✅ ADVANCED SEARCH COMPLETE: returning %d results", len(allResults))
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

// extractKeywords извлекает ключевые слова из текста
func extractKeywords(text string) []string {
	words := strings.Fields(text)
	keywords := []string{}
	
	for _, word := range words {
		if len(word) >= 4 { // Минимальная длина ключевого слова
			keywords = append(keywords, word)
		}
	}
	
	return keywords
}

// SetupEmbeddings настраивает модель эмбеддингов (заглушка для совместимости)
func (se *SearchEngine) SetupEmbeddings(ctx context.Context, modelName string) error {
	log.Printf("SetupEmbeddings called with model: %s", modelName)
	se.embeddingModel = modelName
	
	// Тестируем получение эмбеддинга
	_, err := se.getQueryEmbedding(ctx, "test")
	if err != nil {
		return fmt.Errorf("embedding model not available: %w", err)
	}
	
	// Сохраняем конфигурацию
	_, err = se.db.Exec(`INSERT OR REPLACE INTO search_config (key, value) VALUES ('embedding_model', ?)`, modelName)
	if err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	
	log.Printf("✅ Embedding model configured: %s", modelName)
	return nil
}

// GenerateEmbeddings генерирует эмбеддинги для фрагментов (заглушка для совместимости)
func (se *SearchEngine) GenerateEmbeddings(ctx context.Context, batchSize int) error {
	if se.embeddingModel == "" {
		return fmt.Errorf("embedding model not configured")
	}
	
	log.Printf("GenerateEmbeddings: model=%s, batchSize=%d", se.embeddingModel, batchSize)
	
	// TODO: Реальная генерация эмбеддингов
	return fmt.Errorf("embedding generation not implemented yet - integrate with Ollama or other service")
}

// deduplicateResults удаляет дубликаты из результатов поиска
func deduplicateResults(results []SearchResult) []SearchResult {
	seen := make(map[string]bool)
	unique := []SearchResult{}
	
	for _, r := range results {
		key := r.Fragment
		if len(key) > 100 {
			key = key[:100]
		}
		
		if !seen[key] {
			seen[key] = true
			unique = append(unique, r)
		}
	}
	
	return unique
}





// performLexicalSearch выполняет лексический поиск по FTS5
func performLexicalSearch(ctx context.Context, db *sql.DB, query string, limit int) ([]SearchResult, error) {
	// Расширяем запрос синонимами
	expandedQuery := expandQueryWithSynonyms(query)
	
	// Выполняем FTS5 запрос
	sqlQuery := `
		SELECT 
			rowid,
			content,
			url,
			title,
			specialty_code,
			section_title,
			parent_titles,
			rank
		FROM fragments
		WHERE fragments MATCH ?
		ORDER BY rank
		LIMIT ?
	`
	
	rows, err := db.QueryContext(ctx, sqlQuery, expandedQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var results []SearchResult
	for rows.Next() {
		var (
			rowid         int64
			content       string
			url           string
			title         string
			specialtyCode sql.NullString
			sectionTitle  sql.NullString
			parentTitles  sql.NullString
			rank          float64
		)
		
		if err := rows.Scan(&rowid, &content, &url, &title, &specialtyCode, &sectionTitle, &parentTitles, &rank); err != nil {
			return nil, err
		}
		
		metadata := make(map[string]string)
		if specialtyCode.Valid {
			metadata["specialty_code"] = specialtyCode.String
		}
		if sectionTitle.Valid {
			metadata["section"] = sectionTitle.String
		}
		if parentTitles.Valid {
			metadata["hierarchy"] = parentTitles.String
		}
		
		results = append(results, SearchResult{
			Fragment:   content,
			FragmentID: rowid,
			Source: Source{
				Name: title,
				URL:  url,
			},
			Relevance: -rank, // FTS5 rank is negative, convert to positive
			Metadata:  metadata,
		})
	}
	
	return results, rows.Err()
}

// reciprocalRankFusion объединяет результаты из разных источников используя RRF
func reciprocalRankFusion(lexical, semantic []SearchResult, k int) []SearchResult {
	// Карта для хранения объединенных результатов
	scoreMap := make(map[string]*SearchResult)
	rrfScores := make(map[string]float64)
	
	// Обрабатываем лексические результаты
	for rank, result := range lexical {
		key := result.Source.URL + "|" + result.Fragment
		rrfScores[key] = 1.0 / float64(rank+k)
		scoreMap[key] = &result
	}
	
	// Добавляем семантические результаты
	for rank, result := range semantic {
		key := result.Source.URL + "|" + result.Fragment
		rrfScores[key] += 1.0 / float64(rank+k)
		if scoreMap[key] == nil {
			scoreMap[key] = &result
		}
	}
	
	// Создаем финальный список с RRF скорами
	var fused []SearchResult
	for key, result := range scoreMap {
		result.Relevance = rrfScores[key]
		fused = append(fused, *result)
	}
	
	// Сортируем по убыванию RRF скора
	sort.Slice(fused, func(i, j int) bool {
		return fused[i].Relevance > fused[j].Relevance
	})
	
	return fused
}

// expandQueryWithSynonyms расширяет запрос синонимами для улучшения поиска
func expandQueryWithSynonyms(query string) string {
	synonyms := getSynonymMap()
	
	words := strings.Fields(strings.ToLower(query))
	expanded := make([]string, 0, len(words)*2)
	
	for _, word := range words {
		expanded = append(expanded, word)
		if syns, ok := synonyms[word]; ok {
			for _, syn := range syns {
				if syn != word {
					expanded = append(expanded, syn)
				}
			}
		}
	}
	
	return strings.Join(expanded, " OR ")
}

// getSynonymMap возвращает словарь синонимов (используется из advanced_search.go)
func getSynonymMap() map[string][]string {
	return map[string][]string{
		"бакалавр":     {"бакалавриат", "bachelor"},
		"бакалавриат":  {"бакалавр", "bachelor"},
		"магистр":      {"магистратура", "master"},
		"магистратура": {"магистр", "master"},
		"аспирант":     {"аспирантура", "phd"},
		"аспирантура":  {"аспирант", "phd"},
		"очная":        {"дневная", "очное"},
		"дневная":      {"очная", "дневное"},
		"заочная":      {"заочное"},
		"платное":      {"коммерция", "коммерческое"},
		"коммерция":    {"платное", "коммерческое"},
		"бюджет":       {"бесплатно", "бюджетное"},
		"бюджетное":    {"бесплатно", "бюджет"},
		"бесплатно":    {"бюджет", "бюджетное"},
		"поступление":  {"прием", "приемная", "абитуриент"},
		"прием":        {"поступление", "приемная"},
		"стоимость":    {"цена", "оплата", "стоит"},
		"цена":         {"стоимость", "оплата"},
		
		// Сленговые синонимы
		"доки":         {"документы", "документ", "бумаги"},
		"документы":    {"доки", "документ"},
		"бабки":        {"рубли", "деньги", "стоимость", "цена", "оплата"},
		"бабок":        {"рублей", "денег", "стоимость", "цена"},
		"рубли":        {"деньги", "стоимость"},
		"тусить":       {"проводить время", "досуг", "отдых", "развлечения"},
		"тусовка":      {"досуг", "отдых", "развлечения"},
		"препод":       {"преподаватель", "учитель"},
		"преподаватель": {"препод", "учитель"},
		"пары":         {"занятия", "уроки", "лекции"},
		"занятия":      {"пары", "уроки", "лекции"},
	}
}

