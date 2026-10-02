package main

import (
	"context"
	"log"
	"sort"
	"strings"
	"unicode"
)

// =========================================================
// SEARCH & RETRIEVAL
// =========================================================

// SearchQuery представляет поисковый запрос
type SearchQuery struct {
	Text       string
	Topic      string
	Entities   map[string]string
	Conditions []string
	Intent     Intent
}

// SearchResult представляет результат поиска
type SearchResult struct {
	Fragment   string
	FragmentID int64 // ID фрагмента в БД для отслеживания версий
	Source     Source
	Relevance  float64
	Date       string
	Metadata   map[string]string
}

// PrepareSearchQuery преобразует интент в поисковый запрос
func PrepareSearchQuery(intent Intent) []SearchQuery {
	queries := []SearchQuery{}
	
	mainQuery := SearchQuery{
		Text:       intent.Question,
		Topic:      intent.Topic,
		Entities:   intent.Entities,
		Conditions: intent.Conditions,
		Intent:     intent,
	}
	
	queries = append(queries, mainQuery)
	
	// Для составных вопросов добавляем подзапросы
	if containsAny(intent.Question, "и", "также", "ещё", "еще") {
		// Разделяем на части
		parts := splitCompoundQuestion(intent.Question)
		for _, part := range parts {
			if part != intent.Question {
				subIntent := UnderstandIntent(part, nil)
				subQuery := SearchQuery{
					Text:       part,
					Topic:      subIntent.Topic,
					Entities:   subIntent.Entities,
					Conditions: subIntent.Conditions,
					Intent:     subIntent,
				}
				queries = append(queries, subQuery)
			}
		}
	}
	
	return queries
}

// splitCompoundQuestion разделяет составной вопрос на части
func splitCompoundQuestion(question string) []string {
	separators := []string{" и ", ", ", "; "}
	parts := []string{question}
	
	for _, sep := range separators {
		newParts := []string{}
		for _, part := range parts {
			if strings.Contains(part, sep) {
				split := strings.Split(part, sep)
				newParts = append(newParts, split...)
			} else {
				newParts = append(newParts, part)
			}
		}
		parts = newParts
	}
	
	result := []string{}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if len(trimmed) > 10 {
			result = append(result, trimmed)
		}
	}
	
	return result
}

// SearchMaterials ищет релевантные материалы
func SearchMaterials(ctx context.Context, cfg Config, queries []SearchQuery) ([]SearchResult, error) {
	return SearchMaterialsWithContext(ctx, cfg, queries, nil)
}

// SearchMaterialsWithContext ищет релевантные материалы с учетом контекста диалога
func SearchMaterialsWithContext(ctx context.Context, cfg Config, queries []SearchQuery, dialogContext *DialogContext) ([]SearchResult, error) {
	allResults := []SearchResult{}
	
	for _, query := range queries {
		sources := selectRelevantSources(cfg, query)
		
		pages := []CachedPage{}
		for _, src := range sources {
			// Используем существующую функцию fetchPage
			page, err := fetchPage(ctx, src, cfg.CacheTime)
			if err != nil {
				log.Printf("Не удалось загрузить %s: %v", src.URL, err)
				continue
			}
			pages = append(pages, page)
		}
		
		results := extractRelevantFragmentsWithContext(query, pages, dialogContext)
		allResults = append(allResults, results...)
	}
	
	allResults = deduplicateResults(allResults)
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Relevance > allResults[j].Relevance
	})
	
	return allResults, nil
}

// selectRelevantSources выбирает релевантные источники для запроса
// Использует scoring вместо жесткой фильтрации для максимальной гибкости
func selectRelevantSources(cfg Config, query SearchQuery) []Source {
	allSources := buildSources(cfg.NOMOSBase)
	
	// Scoring sources based on relevance
	type scoredSource struct {
		source Source
		score  int
	}
	
	scored := []scoredSource{}
	
	for _, src := range allSources {
		score := calculateSourceScore(src, query)
		// УЛУЧШЕНИЕ: берем все источники с score >= 0 для максимального покрытия
		if score >= 0 {
			scored = append(scored, scoredSource{src, score})
		}
	}
	
	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})
	
	// УЛУЧШЕНИЕ: увеличиваем количество источников для более широкого поиска
	relevant := []Source{}
	for i, s := range scored {
		// Берем топ-20 источников, или все с score >= 30 (понижен порог)
		if i < 20 || s.score >= 30 {
			relevant = append(relevant, s.source)
		}
	}
	
	// Always include at least 5 sources for fallback (увеличено с 3)
	if len(relevant) < 5 && len(allSources) > 0 {
		for _, src := range allSources {
			if len(relevant) >= 5 {
				break
			}
			alreadyAdded := false
			for _, r := range relevant {
				if r.URL == src.URL {
					alreadyAdded = true
					break
				}
			}
			if !alreadyAdded {
				relevant = append(relevant, src)
			}
		}
	}
	
	log.Printf("SELECTED SOURCES: %d из %d (top score: %d)", len(relevant), len(allSources), 
		func() int { if len(scored) > 0 { return scored[0].score } else { return 0 } }())
	
	// DEBUG: показываем топ-5 источников с их score
	for i := 0; i < len(scored) && i < 5; i++ {
		log.Printf("  [%d] score=%d: %s (%s)", i+1, scored[i].score, scored[i].source.Name, scored[i].source.Kind)
	}
	
	return relevant
}

// calculateSourceScore вычисляет релевантность источника для запроса
func calculateSourceScore(src Source, query SearchQuery) int {
	score := 10 // Базовый балл для всех источников
	
	q := normalize(query.Text)
	
	// Scoring by topic match
	switch query.Topic {
	case "staff":
		if src.Kind == "teachers" {
			score += 100
		}
		if src.Kind == "employees" {
			score += 90
		}
		if containsAny(q, "преподава", "учител", "педагог") && src.Kind == "general" {
			score += 30
		}
		
	case "payment":
		if src.Kind == "payment" {
			score += 100
		}
		if src.Kind == "admission" {
			score += 90 // Увеличено: страница "Поступающим" содержит актуальные цены
		}
		if src.Kind == "specialties" || src.Kind == "specialty_detail" {
			score += 70
		}
		
	case "admission":
		if src.Kind == "admission" {
			score += 100
		}
		if src.Kind == "specialties" {
			score += 60
		}
		
	case "practice":
		if src.Kind == "practice" {
			score += 100
		}
		if src.Kind == "education" {
			score += 50
		}
		
	case "schedule":
		if src.Kind == "schedule" {
			score += 100
		}
		if src.Kind == "education" {
			score += 40
		}
		
	case "documents":
		if src.Kind == "documents" {
			score += 100
		}
		if src.Kind == "admission" {
			score += 40
		}
		
	case "news":
		if src.Kind == "news" {
			score += 100
		}
		
	case "specialty":
		if src.Kind == "specialty_detail" {
			score += 100
		}
		if src.Kind == "specialties" {
			score += 80
		}
		if src.Kind == "admission" {
			score += 40
		}
		
	case "contacts":
		if src.Kind == "contacts" {
			score += 100
		}
		if src.Kind == "general" {
			score += 60
		}
	}
	
	// Специальные паттерны в тексте вопроса
	if containsAny(q, "практик", "стажировк") && src.Kind == "practice" {
		score += 80
	}
	
	if containsAny(q, "пересдач", "пересдат", "долг") && src.Kind == "retake" {
		score += 90
	}
	
	if containsAny(q, "журнал", "оценк", "успеваем") && src.Kind == "students" {
		score += 70
	}
	
	if containsAny(q, "сесси", "экзамен") && (src.Kind == "education" || src.Kind == "schedule") {
		score += 60
	}
	
	// Specific entity matches
	if specCode, ok := query.Entities["specialty_code"]; ok {
		if src.Kind == "specialty_detail" && strings.Contains(strings.ToLower(src.URL), strings.ToLower(specCode)) {
			score += 150 // High priority for exact specialty match
		}
	}
	
	return score
}


// extractRelevantFragments извлекает релевантные фрагменты из страниц
func extractRelevantFragments(query SearchQuery, pages []CachedPage) []SearchResult {
	return extractRelevantFragmentsWithContext(query, pages, nil)
}

// extractRelevantFragmentsWithContext извлекает релевантные фрагменты с учетом контекста диалога
func extractRelevantFragmentsWithContext(query SearchQuery, pages []CachedPage, dialogContext *DialogContext) []SearchResult {
	results := []SearchResult{}
	
	log.Printf("EXTRACT FRAGMENTS: query=%s, pages=%d", query.Text, len(pages))
	
	// Если есть контекст диалога, логируем его
	if dialogContext != nil {
		log.Printf("EXTRACT FRAGMENTS: dialogContext.CurrentTopic=%s, LastEntities=%v", 
			dialogContext.CurrentTopic, dialogContext.LastEntities)
	}
	
	for _, page := range pages {
		paragraphs := splitIntoSemanticChunks(page.Text)
		
		log.Printf("EXTRACT FRAGMENTS: source=%s, text_length=%d, chunks=%d", 
			page.Source.Name, len(page.Text), len(paragraphs))
		
		for i, para := range paragraphs {
			relevance := calculateRelevanceWithContext(query, para, dialogContext)
			
			// Логируем первые 3 фрагмента для отладки
			if i < 3 {
				preview := para
				if len(preview) > 100 {
					preview = preview[:100]
				}
				log.Printf("CHUNK[%d] relevance=%.3f: %s...", i, relevance, preview)
			}
			
			// ОТЛАДКА: ищем фрагменты со словом "дизайн"
			if strings.Contains(strings.ToLower(para), "дизайн") {
				preview := para
				if len(preview) > 150 {
					preview = preview[:150]
				}
				log.Printf("DESIGN FRAGMENT FOUND (relevance=%.3f) from %s: %s...", relevance, page.Source.Name, preview)
			}
			
			// Снижен порог с 0.1 до 0.05
			if relevance > 0.05 {
				result := SearchResult{
					Fragment:  para,
					Source:    page.Source,
					Relevance: relevance,
					Date:      page.PublishedDate,
					Metadata:  map[string]string{},
				}
				results = append(results, result)
				
				// DEBUG: Логируем найденные фрагменты с высокой релевантностью
				if relevance > 0.5 {
					preview := para
					if len(preview) > 150 {
						preview = preview[:150]
					}
					log.Printf("HIGH RELEVANCE FRAGMENT (%.2f) from %s: %s...", relevance, page.Source.Name, preview)
				}
			}
		}
	}
	
	log.Printf("EXTRACT FRAGMENTS: found %d relevant fragments", len(results))
	
	return results
}

// splitIntoSemanticChunks разбивает текст на смысловые фрагменты
func splitIntoSemanticChunks(text string) []string {
	// Проверяем, это страница с преподавателями
	isTeachersPage := strings.Contains(strings.ToLower(text), "преподава") && 
		strings.Contains(text, "@college-nomos.ru")
	
	if isTeachersPage {
		return splitTeacherChunks(text)
	}
	
	paragraphs := strings.Split(text, "\n\n")
	chunks := []string{}
	currentChunk := ""
	
	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		
		if len(para) < 50 && currentChunk != "" {
			currentChunk += "\n" + para
			continue
		}
		
		if currentChunk != "" {
			chunks = append(chunks, currentChunk)
		}
		
		currentChunk = para
		
		if len(para) > 1000 {
			sentences := strings.Split(para, ". ")
			currentChunk = ""
			for _, sent := range sentences {
				sent = strings.TrimSpace(sent)
				if sent == "" {
					continue
				}
				
				if len(currentChunk)+len(sent) > 800 {
					if currentChunk != "" {
						chunks = append(chunks, currentChunk)
					}
					currentChunk = sent
				} else {
					if currentChunk != "" {
						currentChunk += ". " + sent
					} else {
						currentChunk = sent
					}
				}
			}
		}
	}
	
	if currentChunk != "" {
		chunks = append(chunks, currentChunk)
	}
	
	return chunks
}

// splitTeacherChunks разбивает страницу преподавателей на фрагменты по преподавателям
func splitTeacherChunks(text string) []string {
	chunks := []string{}
	
	// Разбиваем по именам преподавателей (ФИО в формате "Фамилия Имя Отчество")
	// Ищем паттерн: заглавная буква после \n, затем еще слова с заглавными буквами
	lines := strings.Split(text, "\n")
	currentChunk := ""
	
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		// Проверяем, является ли строка ФИО (3 слова, каждое с заглавной буквы, кириллица)
		words := strings.Fields(line)
		isName := false
		
		if len(words) == 3 {
			allCapitalized := true
			allCyrillic := true
			
			for _, word := range words {
				if len(word) == 0 {
					allCapitalized = false
					break
				}
				
				// Проверяем, что первая буква заглавная
				firstRune := []rune(word)[0]
				if !unicode.IsUpper(firstRune) {
					allCapitalized = false
					break
				}
				
				// Проверяем, что это кириллица
				hasCyrillic := false
				for _, r := range word {
					if unicode.Is(unicode.Cyrillic, r) {
						hasCyrillic = true
						break
					}
				}
				if !hasCyrillic {
					allCyrillic = false
					break
				}
			}
			
			isName = allCapitalized && allCyrillic
		}
		
		// Если нашли ФИО и есть накопленный чанк, сохраняем его
		if isName && currentChunk != "" && len(currentChunk) > 100 {
			chunks = append(chunks, currentChunk)
			currentChunk = line
		} else {
			if currentChunk != "" {
				currentChunk += "\n" + line
			} else {
				currentChunk = line
			}
		}
		
		// Также сохраняем чанк, если он стал слишком большим
		if len(currentChunk) > 1500 {
			chunks = append(chunks, currentChunk)
			currentChunk = ""
		}
		
		// Для последней строки
		if i == len(lines)-1 && currentChunk != "" {
			chunks = append(chunks, currentChunk)
		}
	}
	
	return chunks
}

// calculateRelevance вычисляет релевантность фрагмента запросу
func calculateRelevance(query SearchQuery, fragment string) float64 {
	return calculateRelevanceWithContext(query, fragment, nil)
}

// calculateRelevanceWithContext вычисляет релевантность с учетом контекста диалога
func calculateRelevanceWithContext(query SearchQuery, fragment string, dialogContext *DialogContext) float64 {
	fragNorm := normalize(fragment)
	score := 0.0
	
	keywords := extractKeywords(query.Text)
	
	// DEBUG: Логируем извлеченные ключевые слова для первого фрагмента
	if len(keywords) > 0 {
		fragPreview := fragNorm
		if len(fragPreview) > 100 {
			fragPreview = fragPreview[:100] + "..."
		}
		log.Printf("RELEVANCE: query='%s' -> keywords=%v, fragment='%s'", query.Text, keywords, fragPreview)
	}
	
	// Применяем стемминг к ключевым словам и словам фрагмента
	stemmedKeywords := make([]string, len(keywords))
	for i, kw := range keywords {
		stemmedKeywords[i] = stem(kw)
	}
	
	// Стеммируем слова фрагмента
	fragWords := strings.Fields(fragNorm)
	stemmedFragWords := make(map[string]bool)
	for _, word := range fragWords {
		stemmedFragWords[stem(word)] = true
	}
	
	matchedKeywords := 0
	for i, kwStem := range stemmedKeywords {
		if stemmedFragWords[kwStem] {
			matchedKeywords++
			// УЛУЧШЕНИЕ: увеличиваем вес ключевых слов
			score += 0.25 // было 0.2
			log.Printf("RELEVANCE: matched keyword '%s' (stem: '%s')", keywords[i], kwStem)
		} else {
			// Попробуем также точное совпадение без стемминга (для коротких слов)
			if strings.Contains(fragNorm, keywords[i]) {
				matchedKeywords++
				score += 0.25
				log.Printf("RELEVANCE: matched keyword '%s' (exact)", keywords[i])
			}
		}
	}
	
	// Бонус за полное совпадение всех ключевых слов (например, все 3 слова ФИО)
	if len(keywords) >= 3 && matchedKeywords == len(keywords) {
		score += 0.5 // Значительный бонус за точное совпадение
	}
	
	// УЛУЧШЕНИЕ: бонус даже за частичное совпадение ключевых слов
	if len(keywords) > 0 {
		kwRatio := float64(matchedKeywords) / float64(len(keywords))
		if kwRatio >= 0.5 { // хотя бы половина слов совпала
			score += 0.3 * kwRatio
		}
	}
	
	// Проверяем entities из текущего запроса
	for _, entity := range query.Entities {
		entityNorm := normalize(entity)
		if strings.Contains(fragNorm, entityNorm) {
			score += 0.3
		}
	}
	
	// НОВОЕ: Бонус за совпадение с entities из контекста диалога
	if dialogContext != nil && len(dialogContext.LastEntities) > 0 {
		for key, entity := range dialogContext.LastEntities {
			entityNorm := normalize(entity)
			if strings.Contains(fragNorm, entityNorm) {
				// Даем бонус, особенно для specialty
				if key == "specialty" || key == "specialty_code" {
					score += 0.4 // Большой бонус за совпадение специальности из контекста
					log.Printf("RELEVANCE BOOST: found context entity '%s'='%s' in fragment", key, entity)
				} else {
					score += 0.2
				}
			}
		}
	}
	
	for _, cond := range query.Conditions {
		condNorm := normalize(cond)
		if strings.Contains(fragNorm, condNorm) {
			score += 0.2
		}
	}
	
	// Определяем тему - сначала из запроса, потом из контекста
	effectiveTopic := query.Topic
	if effectiveTopic == "general" && dialogContext != nil && dialogContext.CurrentTopic != "" {
		effectiveTopic = dialogContext.CurrentTopic
	}
	
	topicKeywords := getTopicKeywords(effectiveTopic)
	matchedTopicKW := 0
	for _, kw := range topicKeywords {
		if strings.Contains(fragNorm, kw) {
			matchedTopicKW++
		}
	}
	if len(topicKeywords) > 0 {
		// УЛУЧШЕНИЕ: увеличиваем вес темы
		score += 0.4 * float64(matchedTopicKW) / float64(len(topicKeywords)) // было 0.3
	}
	
	if effectiveTopic != "general" {
		hasTopicMatch := false
		for _, kw := range topicKeywords {
			if strings.Contains(fragNorm, kw) {
				hasTopicMatch = true
				break
			}
		}
		if !hasTopicMatch {
			score *= 0.5
		}
	}
	
	return score
}

// extractKeywords извлекает ключевые слова из вопроса
func extractKeywords(question string) []string {
	q := normalize(question)
	
	stopWords := []string{
		"как", "что", "где", "когда", "почему", "какой", "какая", "какие",
		"кто", "сколько", "чего", "можно", "нужно", "есть", "быть",
		"это", "этот", "эта", "эти", "мне", "меня", "вас", "вам", "для", "или", "на", "по", "из", "от", "до", "под",
	}
	
	words := strings.Fields(q)
	keywords := []string{}
	
	log.Printf("EXTRACT_KEYWORDS: question len=%d, normalized len=%d, words=%d", len(question), len(q), len(words))
	
	for _, word := range words {
		if len(word) < 3 {
			continue
		}
		
		isStopWord := false
		for _, sw := range stopWords {
			if word == sw {
				isStopWord = true
				break
			}
		}
		
		if !isStopWord {
			keywords = append(keywords, word)
		}
	}
	
	log.Printf("EXTRACT_KEYWORDS: result=%v (from %d words)", keywords, len(words))
	
	return keywords
}


// getTopicKeywords возвращает ключевые слова для темы
func getTopicKeywords(topic string) []string {
	topicKW := map[string][]string{
		"admission": {"поступ", "абитур", "прием", "зачисл", "экзамен", "документ"},
		"payment":   {"стоимость", "цена", "оплат", "рубл", "бюджет", "контракт"},
		"practice":  {"практик", "стажировк"},
		"schedule":  {"расписан", "график", "занят"},
		"contacts":  {"контакт", "телефон", "адрес", "email"},
		"staff":     {"преподава", "директор", "завуч", "педагог", "учитель"},  // Используем корни
		"specialty": {"специальность", "направлен", "профес"},
		"news":      {"новост", "событ", "мероприят"},
	}
	
	if kw, ok := topicKW[topic]; ok {
		return kw
	}
	return []string{}
}

// deduplicateResults удаляет дубликаты
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

// CheckSufficiency проверяет достаточность результатов
func CheckSufficiency(query SearchQuery, results []SearchResult) bool {
	if len(results) == 0 {
		return false
	}
	
	highRelevanceCount := 0
	for _, r := range results {
		if r.Relevance > 0.5 {
			highRelevanceCount++
		}
	}
	
	if highRelevanceCount >= 2 {
		return true
	}
	
	keywords := extractKeywords(query.Text)
	coveredKeywords := 0
	
	combinedText := ""
	for i, r := range results {
		if i >= 5 {
			break
		}
		combinedText += " " + normalize(r.Fragment)
	}
	
	for _, kw := range keywords {
		if strings.Contains(combinedText, kw) {
			coveredKeywords++
		}
	}
	
	return float64(coveredKeywords)/float64(len(keywords)) >= 0.6
}

// RetrySearch повторяет поиск с альтернативными формулировками
func RetrySearch(ctx context.Context, cfg Config, query SearchQuery, attempt int) ([]SearchResult, error) {
	return RetrySearchWithContext(ctx, cfg, query, attempt, nil)
}

// RetrySearchWithContext повторяет поиск с альтернативными формулировками с учетом контекста
func RetrySearchWithContext(ctx context.Context, cfg Config, query SearchQuery, attempt int, dialogContext *DialogContext) ([]SearchResult, error) {
	if attempt > 2 {
		return []SearchResult{}, nil
	}
	
	log.Printf("Повторный поиск (попытка %d) для: %s", attempt, query.Text)
	
	altQueries := generateAlternativeQueries(query)
	return SearchMaterialsWithContext(ctx, cfg, altQueries, dialogContext)
}

// generateAlternativeQueries генерирует альтернативные формулировки
func generateAlternativeQueries(query SearchQuery) []SearchQuery {
	alternatives := []SearchQuery{query}
	
	// УЛУЧШЕНИЕ: расширяем словарь синонимов
	synonyms := map[string][]string{
		"стоимость":  {"цена", "оплата", "сколько стоит", "платить", "рубл"},
		"поступить":  {"зачислиться", "подать документы", "стать студентом", "вступить"},
		"где":        {"адрес", "местонахождение", "как найти", "расположение"},
		"когда":      {"срок", "дата", "время", "период"},
		"специальность": {"направление", "профессия", "специализация", "факультет", "кафедра"},
		"практика":   {"стажировка", "практическая подготовка"},
		"преподаватель": {"педагог", "учитель", "препод", "преподает"},
		"расписание": {"график", "занятия", "уроки"},
		"документ":   {"бумаги", "справки", "аттестат"},
		"контакт":    {"телефон", "email", "связь", "связаться"},
		"обучение":   {"учеба", "образование", "учиться"},
		"колледж":    {"номос", "учебное заведение"},
		"бюджет":     {"бесплатно", "без оплаты", "бюджетное место"},
		"форма":      {"очно", "заочно", "дистанционно"},
		"директор":   {"руководитель", "глава", "директорат", "управление"},
		"факультет":  {"специальность", "направление", "отделение", "кафедра"},
	}
	
	qNorm := normalize(query.Text)
	
	for original, alts := range synonyms {
		if strings.Contains(qNorm, original) {
			for _, alt := range alts {
				altText := strings.Replace(qNorm, original, alt, 1)
				altQuery := query
				altQuery.Text = altText
				alternatives = append(alternatives, altQuery)
			}
		}
	}
	
	return alternatives
}

