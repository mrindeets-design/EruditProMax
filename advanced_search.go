package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
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
		minScoreThreshold: 0.01, // ИСПРАВЛЕНО: порог для чистого RRF (max ~0.033)
		rrfK:              60,   // Стандартное значение для RRF
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
	
	return rows.Err()
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
// ИСПРАВЛЕНО: гарантирует сохранение TOP-результатов по score
func (se *SearchEngine) selectEvidentialFragments(results []HybridSearchResult, query string) []HybridSearchResult {
	if len(results) == 0 {
		return results
	}
	
	// ГАРАНТИЯ: всегда сохраняем TOP-4 результата по score (независимо от фильтров)
	topN := 4
	if len(results) < topN {
		topN = len(results)
	}
	
	// Создаём множество для отслеживания уже включённых фрагментов
	seen := make(map[int64]bool)
	evidential := []HybridSearchResult{}
	
	// ШАГ 1: Добавляем TOP-N результатов (гарантированно)
	for i := 0; i < topN; i++ {
		evidential = append(evidential, results[i])
		seen[results[i].FragmentID] = true
	}
	log.Printf("  ✅ Evidence: guaranteed top-%d by score", topN)
	
	// ШАГ 2: Дополнительно проверяем остальные на наличие ключевых слов
	// (только если нужны дополнительные фрагменты)
	if topN < len(results) {
		queryNorm := normalize(query)
		queryWords := extractKeywords(queryNorm)
		
		additionalCount := 0
		for i := topN; i < len(results) && additionalCount < 2; i++ {
			r := results[i]
			if seen[r.FragmentID] {
				continue
			}
			
			textNorm := normalize(r.Text)
			
			// Проверяем наличие ключевых слов запроса
			matchCount := 0
			for _, kw := range queryWords {
				if strings.Contains(textNorm, kw) {
					matchCount++
				}
			}
			
			// Добавляем если содержит хотя бы 40% ключевых слов
			if len(queryWords) > 0 && float64(matchCount)/float64(len(queryWords)) >= 0.4 {
				evidential = append(evidential, r)
				seen[r.FragmentID] = true
				additionalCount++
			}
		}
		
		if additionalCount > 0 {
			log.Printf("  ➕ Evidence: +%d additional keyword-matched", additionalCount)
		}
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
	// Guard: пустой запрос
	if strings.TrimSpace(query) == "" {
		return []HybridSearchResult{}, nil
	}
	
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
			f.version_number as version,
			f.created_at as indexed_at,
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
		
		r.LexicalScore = se.normalizeBM25Score(score)
		r.LexicalRank = rank
		rank++
		
		results = append(results, r)
	}
	
	return results, rows.Err()
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
			f.version_number as version,
			f.created_at as indexed_at,
			e.embedding_blob
		FROM fragments f
		JOIN embeddings e ON f.id = e.fragment_id
		LEFT JOIN pages p ON f.page_id = p.id
		WHERE e.model_name = ? AND e.embedding_blob IS NOT NULL
	`
	
	rows, err := se.db.QueryContext(ctx, sqlQuery, se.embeddingModel)
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
// extractProperNounsFromQuery извлекает имена собственные из запроса
// (копия функции из search.go, но для использования в advanced_search)
func extractProperNounsFromQuery(text string) []string {
	words := strings.Fields(text)
	properNouns := []string{}

	// Служебные слова, которые НЕ являются именами собственными
	serviceWords := map[string]bool{
		"расскажи": true, "подскажи": true, "скажи": true, "привет": true, "здравствуйте": true,
		"кто": true, "что": true, "где": true, "когда": true, "почему": true, "как": true,
		"какой": true, "какая": true, "какие": true, "чей": true, "чья": true,
	}

	for idx, word := range words {
		// Очищаем от пунктуации
		cleaned := strings.Trim(word, ".,!?;:()[]{}\"'")
		if len(cleaned) == 0 {
			continue
		}

		// Проверяем, начинается ли с заглавной буквы
		firstRune := []rune(cleaned)[0]
		if unicode.IsUpper(firstRune) {
			// Проверяем, не является ли это служебным словом
			cleanedLower := strings.ToLower(cleaned)
			if serviceWords[cleanedLower] {
				continue
			}

			// Пропускаем первое слово если оно служебное
			if idx == 0 && serviceWords[cleanedLower] {
				continue
			}

			properNouns = append(properNouns, cleaned)
		}
	}

	return properNouns
}


func (se *SearchEngine) prepareFTS5Query(query string) string {
	// УЛУЧШЕНО: Сначала извлекаем имена собственные (до нормализации)
	properNouns := extractProperNounsFromQuery(query)
	
	// Нормализуем и очищаем
	normalized := normalize(query)
	
	// Guard: пустой запрос (не должно происходить, но на всякий случай)
	if normalized == "" {
		return normalized // вернём пустоту, guard выше обработает
	}
	
	// Обрабатываем коды специальностей (XX.XX.XX)
	// Заменяем точки на пробелы для FTS поиска
	codePattern := regexp.MustCompile(`\d{2}\.\d{2}\.\d{2}`)
	if codePattern.MatchString(normalized) {
		// Найдем все коды
		matches := codePattern.FindAllString(normalized, -1)
		for _, match := range matches {
			// Заменяем точки на пробелы: "40.02.04" -> "40 02 04"
			withSpaces := strings.ReplaceAll(match, ".", " ")
			// Оборачиваем в кавычки для phrase search
			quoted := fmt.Sprintf(`"%s"`, withSpaces)
			normalized = strings.ReplaceAll(normalized, match, quoted)
		}
	}
	
	// Убираем стоп-слова
	stopWords := []string{"и", "или", "в", "на", "с", "по", "для", "о", "об", "к", "от"}
	words := strings.Fields(normalized)
	filtered := []string{}
	
	for _, word := range words {
		// Сохраняем кавычки и их содержимое как единый токен
		if strings.HasPrefix(word, `"`) || strings.Contains(word, `"`) {
			filtered = append(filtered, word)
			continue
		}
		
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
			// Безопасно экранируем для FTS5
			filtered = append(filtered, sanitizeFTSToken(word))
		}
	}
	
	// НОВОЕ: Добавляем prefix-поиск для имён собственных (фамилий)
	// Это позволяет находить "Колесникова" при поиске "Колесниковой"
	for _, proper := range properNouns {
		properLower := strings.ToLower(proper)
		// Добавляем стемминг/prefix поиск: "Колесников*"
		if len(properLower) >= 4 {
			// Берём основу (минус 2 последних символа для учета окончаний)
			stem := properLower[:len(properLower)-2]
			if len(stem) >= 3 {
				filtered = append(filtered, fmt.Sprintf("%s*", sanitizeFTSToken(stem)))
			}
		}
	}
	
	// Объединяем через OR для широкого поиска
	if len(filtered) == 0 {
		safe := sanitizeFTSToken(normalized)
		return safe
	}
	
	return strings.Join(filtered, " OR ")
}

// sanitizeFTSToken безопасно обрабатывает токен для FTS5 MATCH
// Предотвращает injection и обрабатывает спецсимволы
func sanitizeFTSToken(token string) string {
	if token == "" {
		return ""
	}
	
	// Удаляем FTS операторы в начале
	token = strings.TrimLeft(token, "+-")
	
	// Экранируем кавычки внутри токена
	token = strings.ReplaceAll(token, `"`, `""`)
	
	// Проверяем нужно ли обернуть в кавычки
	// Коды специальностей (54.02.01), составные слова, дефисы
	needsQuotes := strings.ContainsAny(token, ".-/ ")
	
	if needsQuotes {
		return `"` + token + `"`
	}
	
	return token
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

// ScoreBonus представляет разбивку бонусов для отладки
type ScoreBonus struct {
	Base             float64
	SpecialtyMatch   float64 // текущая специальность из запроса
	SpecialtyContext float64 // специальность из истории диалога
	SectionMatch     float64
	TopicMatch       float64
	CategoryMatch    float64 // НОВОЕ: бонус за категориальное соответствие
	RecencyBonus     float64
	URLMultiplier    float64 // URL-based scoring multiplier
	Total            float64
}

// calculateFinalScore вычисляет итоговую оценку с бонусами (централизовано)
func (se *SearchEngine) calculateFinalScore(result *HybridSearchResult, query string, dialogContext *DialogContext) ScoreBonus {
	bonus := ScoreBonus{
		Base: result.RRFScore, // Начинаем с RRF как базы
		URLMultiplier: 1.0,    // По умолчанию нейтральный множитель
	}
	
	queryNorm := normalize(query)
	textNorm := normalize(result.Text)
	
	// ПРИОРИТЕТ 1: Текущая специальность из запроса (явное упоминание)
	if result.SpecialtyCode != nil {
		specialtyCode := *result.SpecialtyCode
		
		// Проверяем явное упоминание кода в запросе (до нормализации!)
		if strings.Contains(query, specialtyCode) || strings.Contains(queryNorm, strings.ReplaceAll(specialtyCode, ".", " ")) {
			bonus.SpecialtyMatch = 0.4 // Сильный бонус за точное совпадение
		} else {
			// Проверяем упоминание названия специальности
			specialtyNames := map[string]string{
				"54.02.01": "дизайн",
				"44.02.02": "преподавание",
				"40.02.04": "юриспруденц",
				"40.02.01": "социальн", // Право и организация социального обеспечения
			}
			if name, ok := specialtyNames[specialtyCode]; ok {
				if strings.Contains(queryNorm, name) {
					bonus.SpecialtyMatch = 0.3 // Средний бонус за название
				}
			}
			
			// Дополнительные синонимы для специальностей
			if specialtyCode == "44.02.02" && (strings.Contains(queryNorm, "учитель") || strings.Contains(queryNorm, "педагог")) {
				bonus.SpecialtyMatch = 0.3
			}
			if specialtyCode == "40.02.04" && (strings.Contains(queryNorm, "юрист") || strings.Contains(queryNorm, "право")) {
				bonus.SpecialtyMatch = 0.3
			}
		}
	}
	
	// ПРИОРИТЕТ 2: Специальность из контекста диалога (только если нет текущей)
	if bonus.SpecialtyMatch == 0.0 && dialogContext != nil && result.SpecialtyCode != nil {
		if lastSpecCode, ok := dialogContext.LastEntities["specialty_code"]; ok {
			if *result.SpecialtyCode == lastSpecCode {
				bonus.SpecialtyContext = 0.2 // Меньший бонус за контекстную специальность
			}
		}
	}
	
	// Совпадение заголовка секции
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
			bonus.SectionMatch = 0.15 * float64(matchCount) / float64(len(queryWords))
		}
	}
	
	// НОВОЕ: Категориальный бонус (приоритет для целевой категории)
	// Если фрагмент из категории, соответствующей интенту, даем бонус
	if result.URL != "" {
		// Детектируем категорию фрагмента по URL
		fragmentCategory := DetectCategory(result.URL)
		
		// Определяем ожидаемую категорию на основе запроса
		expectedCategory := mapQueryToCategory(query)
		
		if expectedCategory != "" && fragmentCategory != nil && fragmentCategory.CategoryID == expectedCategory {
			bonus.CategoryMatch = 0.20 // Приоритет целевой категории
			log.Printf("  🎯 Category match bonus: %s", fragmentCategory.CategoryID)
		}
	}
	
	// Совпадение с текущей темой диалога
	if dialogContext != nil && dialogContext.CurrentTopic != "" {
		topicKeywords := getTopicKeywords(dialogContext.CurrentTopic)
		for _, kw := range topicKeywords {
			if strings.Contains(textNorm, kw) {
				bonus.TopicMatch = 0.15
				break
			}
		}
	}
	
	// URL-based scoring multiplier (анти-Homepage Bias)
	bonus.URLMultiplier = calculateURLMultiplier(result.URL, query)
	
	// Итоговая оценка (сначала суммируем все бонусы, затем применяем URL multiplier)
	baseScore := bonus.Base + bonus.SpecialtyMatch + bonus.SpecialtyContext + 
	              bonus.SectionMatch + bonus.TopicMatch + bonus.CategoryMatch + bonus.RecencyBonus
	
	bonus.Total = baseScore * bonus.URLMultiplier
	
	return bonus
}

// calculateURLMultiplier вычисляет множитель на основе типа URL страницы
// Цель: понизить приоритет главной страницы и повысить специализированные разделы
func calculateURLMultiplier(url string, query string) float64 {
	if url == "" {
		return 1.0
	}
	
	urlLower := strings.ToLower(url)
	queryLower := strings.ToLower(query)
	
	// ШТРАФ: Главная страница (корень домена с мусорными параметрами)
	// Примеры: https://college-nomos.ru/, https://college-nomos.ru/?month=10&year=2025
	if strings.HasSuffix(urlLower, "college-nomos.ru/") || 
	   strings.Contains(urlLower, "college-nomos.ru/?month=") ||
	   strings.Contains(urlLower, "college-nomos.ru/?year=") {
		return 0.15 // Очень сильный штраф за главную страницу
	}
	
	// БОНУС: Специализированные разделы (в порядке приоритета)
	
	// 1. Контакты и реквизиты (высокий приоритет для таких запросов)
	if strings.Contains(urlLower, "/contacts") || strings.Contains(urlLower, "/requisites") {
		if strings.Contains(queryLower, "контакт") || strings.Contains(queryLower, "адрес") ||
		   strings.Contains(queryLower, "телефон") || strings.Contains(queryLower, "email") ||
		   strings.Contains(queryLower, "реквизит") {
			return 2.0 // Очень сильный бонус при релевантности
		}
		return 1.5 // Средний бонус для контактов
	}
	
	// 2. Раздел поступления (abitur) - критически важный
	if strings.Contains(urlLower, "/abitur/") {
		if strings.Contains(queryLower, "поступ") || strings.Contains(queryLower, "абитур") ||
		   strings.Contains(queryLower, "документ") || strings.Contains(queryLower, "прием") ||
		   strings.Contains(queryLower, "зачисл") || strings.Contains(queryLower, "стоимость") {
			return 2.0
		}
		return 1.6
	}
	
	// 3. Преподаватели - ПРИОРИТЕТ для страницы /teachers/
	// Это единая чистая страница со списком всех преподавателей и их предметов
	if strings.Contains(urlLower, "/teachers/") || strings.Contains(urlLower, "/teachers") {
		// Запросы о преподавателях - максимальный приоритет
		if strings.Contains(queryLower, "кто так") || 
		   strings.Contains(queryLower, "преподава") || 
		   strings.Contains(queryLower, "преподаёт") ||
		   strings.Contains(queryLower, "учитель") ||
		   strings.Contains(queryLower, "педагог") ||
		   strings.Contains(queryLower, "директор") ||
		   strings.Contains(queryLower, "кто ведёт") ||
		   strings.Contains(queryLower, "кто ведет") ||
		   containsPersonName(query) {
			return 3.5 // МАКСИМАЛЬНЫЙ приоритет для /teachers/
		}
		return 2.5 // Высокий приоритет даже для общих запросов
	}
	
	// 4. Пересдачи и справки (/retake/) - критически важная страница для студентов
	if strings.Contains(urlLower, "/retake/") {
		if strings.Contains(queryLower, "пересдач") || strings.Contains(queryLower, "справк") ||
		   strings.Contains(queryLower, "направлен") || strings.Contains(queryLower, "пересдат") ||
		   strings.Contains(queryLower, "академическ") {
			return 2.5 // Очень высокий приоритет для запросов о пересдачах/справках
		}
		return 1.8
	}
	
	// 5. Сведения об образовательной организации (sveden) - понижаем приоритет
	// Теперь /teachers/ - основной источник для преподавателей
	if strings.Contains(urlLower, "/sveden/employees/prepodavateli/") {
		// Персональные страницы - средний приоритет (уступают /teachers/)
		if containsPersonName(query) {
			return 1.8
		}
		return 1.3
	}
	
	if strings.Contains(urlLower, "/sveden/teaching") {
		// Табличный список преподавателей - низкий приоритет (уступают /teachers/)
		return 1.2
	}
	
	if strings.Contains(urlLower, "/sveden/") {
		if strings.Contains(queryLower, "документ") || strings.Contains(queryLower, "лиценз") ||
		   strings.Contains(queryLower, "аккредит") || strings.Contains(queryLower, "устав") ||
		   strings.Contains(queryLower, "программ") {
			return 1.4
		}
		return 1.3
	}
	
	// 6. Студентам (students) - учебная информация
	if strings.Contains(urlLower, "/students/") {
		if strings.Contains(queryLower, "расписан") || strings.Contains(queryLower, "учеб") ||
		   strings.Contains(queryLower, "студент") || strings.Contains(queryLower, "сессия") {
			return 1.3
		}
		return 1.2
	}
	
	// 7. Жизнь колледжа (life, FAQ)
	if strings.Contains(urlLower, "/life/") {
		if strings.Contains(queryLower, "общежит") || strings.Contains(queryLower, "досуг") ||
		   strings.Contains(queryLower, "мероприят") || strings.Contains(queryLower, "кружок") {
			return 1.8
		}
		return 1.4
	}
	
	// 8. Новости и пресс-центр (более низкий приоритет для общих запросов)
	if strings.Contains(urlLower, "/press-center/") || strings.Contains(urlLower, "/news/") {
		if strings.Contains(queryLower, "новост") || strings.Contains(queryLower, "событи") {
			return 1.1
		}
		return 0.9 // Небольшой штраф для новостей при неявных запросах
	}
	
	// По умолчанию: нейтральный множитель для всех остальных страниц
	return 1.0
}

// containsPersonName проверяет наличие ФИО в запросе (простая эвристика)
func containsPersonName(query string) bool {
	// Ищем паттерн: 2-3 слова с заглавной буквы подряд (возможно ФИО)
	words := strings.Fields(query)
	capitalized := 0
	
	for _, word := range words {
		// Убираем знаки препинания
		word = strings.Trim(word, ".,!?;:")
		if len(word) > 2 && len(word) < 20 {
			// Проверяем что слово начинается с заглавной буквы (кириллица или латиница)
			firstRune := []rune(word)[0]
			if (firstRune >= 'А' && firstRune <= 'Я') || (firstRune >= 'A' && firstRune <= 'Z') {
				capitalized++
				if capitalized >= 2 { // Как минимум Имя Фамилия
					return true
				}
			} else {
				capitalized = 0 // Сброс счетчика если слово не с заглавной
			}
		}
	}
	
	return false
}

// mapQueryToCategory сопоставляет запрос с ожидаемой категорией
func mapQueryToCategory(query string) string {
	queryLower := strings.ToLower(query)
	
	// Приоритет 1: Специальности (самый высокий приоритет)
	if strings.Contains(queryLower, "дизайн") {
		return "abitur_specialty_design"
	}
	if strings.Contains(queryLower, "юриспруденц") || strings.Contains(queryLower, "юрист") || strings.Contains(queryLower, "право") {
		return "abitur_specialty_law"
	}
	if strings.Contains(queryLower, "преподаван") || strings.Contains(queryLower, "учитель") || strings.Contains(queryLower, "педагог") {
		return "abitur_specialty_teacher"
	}
	
	// Приоритет 2: Финансы и оплата
	if strings.Contains(queryLower, "стоимость") || strings.Contains(queryLower, "оплат") || 
	   strings.Contains(queryLower, "цена") || strings.Contains(queryLower, "сколько стоит") ||
	   strings.Contains(queryLower, "платн") || strings.Contains(queryLower, "деньги") {
		return "finance_paid_edu"
	}
	
	// Приоритет 3: Поступление и абитуриенты
	if strings.Contains(queryLower, "поступ") || strings.Contains(queryLower, "абитур") ||
	   strings.Contains(queryLower, "прием") || strings.Contains(queryLower, "зачисл") {
		// Если упомянута специальность, вернем специальность
		if strings.Contains(queryLower, "дизайн") {
			return "abitur_specialty_design"
		}
		if strings.Contains(queryLower, "юрист") || strings.Contains(queryLower, "право") {
			return "abitur_specialty_law"
		}
		if strings.Contains(queryLower, "учитель") || strings.Contains(queryLower, "педагог") {
			return "abitur_specialty_teacher"
		}
		return "abitur_postupayushchim"
	}
	
	// Приоритет 4: Документы для поступления
	if strings.Contains(queryLower, "документ") && (strings.Contains(queryLower, "поступ") || 
	   strings.Contains(queryLower, "нужн") || strings.Contains(queryLower, "подать")) {
		return "abitur_postupayushchim"
	}
	
	// Приоритет 5: Специальности (общий вопрос)
	if strings.Contains(queryLower, "специальность") || strings.Contains(queryLower, "направлен") ||
	   strings.Contains(queryLower, "профес") || strings.Contains(queryLower, "кем работать") {
		return "abitur_specialties"
	}
	
	// Приоритет 6: Практика
	if strings.Contains(queryLower, "практик") || strings.Contains(queryLower, "стажировк") {
		return "students_practice"
	}
	
	// Приоритет 7: Расписание
	if strings.Contains(queryLower, "расписан") || strings.Contains(queryLower, "когда занятия") ||
	   strings.Contains(queryLower, "график") || strings.Contains(queryLower, "пар") {
		return "students_schedule"
	}
	
	// Приоритет 8: Преподаватели и руководство
	if strings.Contains(queryLower, "преподава") || strings.Contains(queryLower, "учитель") ||
	   strings.Contains(queryLower, "кто ведет") || strings.Contains(queryLower, "кто преподает") ||
	   strings.Contains(queryLower, "педагог") {
		return "staff_teachers"
	}
	if strings.Contains(queryLower, "директор") || strings.Contains(queryLower, "руководител") ||
	   strings.Contains(queryLower, "администрац") {
		return "staff_managers"
	}
	
	// Приоритет 9: Контакты
	if strings.Contains(queryLower, "контакт") || strings.Contains(queryLower, "телефон") ||
	   strings.Contains(queryLower, "адрес") || strings.Contains(queryLower, "email") ||
	   strings.Contains(queryLower, "как связаться") || strings.Contains(queryLower, "где находится") {
		return "general_contacts"
	}
	
	// Приоритет 10: Документы колледжа
	if strings.Contains(queryLower, "устав") || strings.Contains(queryLower, "лицензи") ||
	   strings.Contains(queryLower, "аккредитац") {
		return "sveden_document"
	}
	
	// Приоритет 11: Образовательные программы
	if strings.Contains(queryLower, "учебн") && (strings.Contains(queryLower, "план") || 
	   strings.Contains(queryLower, "программ") || strings.Contains(queryLower, "стандарт")) {
		return "sveden_education"
	}
	
	// Приоритет 12: Вакантные места
	if strings.Contains(queryLower, "вакантн") || strings.Contains(queryLower, "сколько мест") ||
	   strings.Contains(queryLower, "бюджетн") {
		return "sveden_vacant"
	}
	
	// Приоритет 13: Материально-техническое обеспечение
	if strings.Contains(queryLower, "кабинет") || strings.Contains(queryLower, "спортзал") ||
	   strings.Contains(queryLower, "библиотек") || strings.Contains(queryLower, "оборудован") {
		return "sveden_objects"
	}
	
	// Приоритет 14: Основные сведения
	if strings.Contains(queryLower, "учредител") || strings.Contains(queryLower, "юридическ") {
		return "sveden_common"
	}
	
	// Приоритет 15: О колледже
	if strings.Contains(queryLower, "истори") || strings.Contains(queryLower, "когда основ") ||
	   strings.Contains(queryLower, "о колледж") {
		return "general_about"
	}
	
	// Приоритет 16: Новости
	if strings.Contains(queryLower, "новост") || strings.Contains(queryLower, "событи") ||
	   strings.Contains(queryLower, "мероприят") {
		return "general_news"
	}
	
	// Приоритет 17: Выпускники
	if strings.Contains(queryLower, "выпускн") || strings.Contains(queryLower, "диплом") ||
	   strings.Contains(queryLower, "вкр") || strings.Contains(queryLower, "защит") {
		return "students_graduates"
	}
	
	return "" // Категория не определена
}

// deepRanking выполняет глубокое ранжирование с учетом контекста
func (se *SearchEngine) deepRanking(pool *CandidatePool) []HybridSearchResult {
	for i := range pool.Candidates {
		// Вычисляем итоговую оценку с бонусами
		scoreBonus := se.calculateFinalScore(&pool.Candidates[i], pool.Query, pool.DialogContext)
		pool.Candidates[i].CombinedScore = scoreBonus.Total
		pool.Candidates[i].ContextRelevance = scoreBonus.TopicMatch
		
		// Логируем разбивку для отладки
		if scoreBonus.SpecialtyMatch > 0 || scoreBonus.SpecialtyContext > 0 || scoreBonus.SectionMatch > 0 || scoreBonus.URLMultiplier != 1.0 {
			log.Printf("  🎯 Score [frag=%d]: base=%.3f spec=%.3f+%.3f sec=%.3f topic=%.3f ×url=%.2fx → %.3f",
				pool.Candidates[i].FragmentID, scoreBonus.Base, 
				scoreBonus.SpecialtyMatch, scoreBonus.SpecialtyContext,
				scoreBonus.SectionMatch, scoreBonus.TopicMatch, 
				scoreBonus.URLMultiplier, scoreBonus.Total)
		}
	}
	
	// Стабильная сортировка по итоговой оценке
	sort.SliceStable(pool.Candidates, func(i, j int) bool {
		// Сначала по оценке
		if pool.Candidates[i].CombinedScore != pool.Candidates[j].CombinedScore {
			return pool.Candidates[i].CombinedScore > pool.Candidates[j].CombinedScore
		}
		// При равной оценке - по FragmentID (детерминизм)
		return pool.Candidates[i].FragmentID < pool.Candidates[j].FragmentID
	})
	
	// ANTI-HOMEPAGE BIAS: Если есть специализированные страницы, фильтруем главную
	hasSpecializedPages := false
	for _, cand := range pool.Candidates {
		urlLower := strings.ToLower(cand.URL)
		if strings.Contains(urlLower, "/contacts") || strings.Contains(urlLower, "/abitur/") ||
		   strings.Contains(urlLower, "/sveden/") || strings.Contains(urlLower, "/life/") ||
		   strings.Contains(urlLower, "/students/") || strings.Contains(urlLower, "/requisites") ||
		   strings.Contains(urlLower, "/teachers/") || strings.Contains(urlLower, "/retake/") {
			hasSpecializedPages = true
			break
		}
	}
	
	if hasSpecializedPages {
		// Удаляем главную страницу из результатов
		filtered := make([]HybridSearchResult, 0, len(pool.Candidates))
		for _, cand := range pool.Candidates {
			urlLower := strings.ToLower(cand.URL)
			isHomepage := strings.HasSuffix(urlLower, "college-nomos.ru/") ||
			             strings.Contains(urlLower, "college-nomos.ru/?month=") ||
			             strings.Contains(urlLower, "college-nomos.ru/?year=")
			
			if !isHomepage {
				filtered = append(filtered, cand)
			}
		}
		return filtered
	}
	
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
		log.Printf("🔍 SEARCH ENGINE: Searching for query='%s'", query.Text)
		hybridResults, err := searchEngine.Search(ctx, query.Text, dialogContext, 10)
		if err != nil {
			log.Printf("Warning: advanced search failed: %v", err)
			// Fallback на старый метод
			return SearchMaterialsWithContext(ctx, cfg, queries, dialogContext)
		}
		log.Printf("🔍 SEARCH ENGINE: Found %d results for query='%s'", len(hybridResults), query.Text)

		// Конвертируем HybridSearchResult в SearchResult
		for i, hr := range hybridResults {
			log.Printf("  📄 Result #%d: score=%.3f url=%s text_len=%d", i+1, hr.CombinedScore, hr.URL, len(hr.Text))
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

