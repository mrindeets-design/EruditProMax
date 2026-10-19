package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// min возвращает минимум двух чисел
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// FallbackData структура для загрузки fallback данных из JSON
type FallbackData map[string]struct {
	Title       string                 `json:"title"`
	Content     string                 `json:"content,omitempty"`
	Specialties map[string]interface{} `json:"specialties,omitempty"`
}

var fallbackDataCache FallbackData

// loadFallbackData загружает fallback данные из JSON файла
func loadFallbackData() error {
	data, err := os.ReadFile("fallback_data.json")
	if err != nil {
		log.Printf("⚠️ Fallback data file not found: %v", err)
		return err
	}

	if err := json.Unmarshal(data, &fallbackDataCache); err != nil {
		log.Printf("❌ Failed to parse fallback data: %v", err)
		return err
	}

	log.Printf("✅ Fallback data loaded: %d categories", len(fallbackDataCache))
	return nil
}

// getFallbackAnswer пытается найти ответ в fallback данных
func getFallbackAnswer(question string) (string, bool) {
	if len(fallbackDataCache) == 0 {
		log.Printf("⚠️ FALLBACK: cache is empty")
		return "", false
	}

	q := strings.ToLower(question)
	log.Printf("🔍 FALLBACK: checking question (length=%d, runes=%d)", len(q), len([]rune(q)))
	log.Printf("🔍 FALLBACK: first 50 chars: %v", []rune(q)[:min(50, len([]rune(q)))])

	// Проверяем категории в порядке приоритета (более специфичные первыми)

	// 1. Вступительные экзамены (приоритет - может конфликтовать с all_specialties)
	if data, ok := fallbackDataCache["entrance_exams"]; ok {
		if (containsAny(q, "экзамен", "огэ", "егэ", "сдавать") &&
			containsAny(q, "поступ", "нужн", "надо", "треб")) ||
			(containsAny(q, "вступительн", "испытан") &&
				containsAny(q, "творческ", "дизайн", "поступ", "специальност", "предусмотр")) {
			return data.Content, true
		}
	}

	// 2. Куда подавать документы (специфичный вопрос)
	if data, ok := fallbackDataCache["documents_where_to_submit"]; ok {
		if (containsAny(q, "куда", "где") &&
			containsAny(q, "нести", "подать", "подавать", "отнести", "сдать", "принес") &&
			containsAny(q, "документ", "доки", "бумаг")) ||
			(containsAny(q, "адрес", "место") && containsAny(q, "прием", "документ", "доки")) {
			return data.Content, true
		}
	}

	// 3. Директор
	if data, ok := fallbackDataCache["director"]; ok {
		if containsAny(q, "директор", "руководител", "заведующ") {
			return data.Content, true
		}
	}

	// 4. Скидки
	if data, ok := fallbackDataCache["discounts"]; ok {
		if containsAny(q, "скидк", "льгот", "снижени", "дешевле") {
			return data.Content, true
		}
	}

	// 5. Сроки обучения
	if data, ok := fallbackDataCache["study_duration"]; ok {
		if containsAny(q, "срок", "длительност", "долго", "продолжительност") &&
			containsAny(q, "обучен", "учи") {
			if data.Specialties != nil {
				var result strings.Builder
				result.WriteString("Сроки обучения по специальностям колледжа «Номос»:\n\n")
				for code, spec := range data.Specialties {
					specMap := spec.(map[string]interface{})
					result.WriteString(fmt.Sprintf("**%s %s**\n", code, specMap["name"]))
					result.WriteString(fmt.Sprintf("- На базе 9 классов: %s\n", specMap["after_9"]))
					result.WriteString(fmt.Sprintf("- На базе 11 классов: %s\n\n", specMap["after_11"]))
				}
				return result.String(), true
			}
		}
	}

	// 6. Сроки приема документов
	if data, ok := fallbackDataCache["admission_dates"]; ok {
		if containsAny(q, "когда", "срок") && containsAny(q, "прием", "подать", "документ", "доки") {
			return data.Content, true
		}
	}

	// 7. Какие документы для поступления
	if data, ok := fallbackDataCache["admission_documents"]; ok {
		if (containsAny(q, "документ", "доки", "бумаг") &&
			(containsAny(q, "поступ", "прием", "подав", "нужн", "надо", "треб"))) &&
			!containsAny(q, "куда", "где", "нести", "отнести") {
			log.Printf("✅ FALLBACK HIT: admission_documents")
			return data.Content, true
		}
	}

	// 8. Материнский капитал
	if data, ok := fallbackDataCache["maternal_capital"]; ok {
		if containsAny(q, "материнск", "капитал", "мск") {
			log.Printf("✅ FALLBACK HIT: maternal_capital")
			return data.Content, true
		}
	}

	// 9. Рассрочка оплаты
	if data, ok := fallbackDataCache["installment_payment"]; ok {
		if containsAny(q, "рассрочк", "помесяч", "семестр", "частям") ||
			(containsAny(q, "оплат", "платить", "плачу") && containsAny(q, "рассроч", "месяц")) {
			log.Printf("✅ FALLBACK HIT: installment_payment")
			return data.Content, true
		}
	}

	// 10. Проходной балл
	if data, ok := fallbackDataCache["passing_score"]; ok {
		if containsAny(q, "проходн", "балл", "средний") && containsAny(q, "аттестат", "поступ", "надо") {
			return data.Content, true
		}
	}

	// 11. Спортивные секции
	if data, ok := fallbackDataCache["sports_clubs"]; ok {
		if containsAny(q, "спорт", "секци", "кружк", "клуб", "досуг", "тусить") {
			return data.Content, true
		}
	}

	// 12. Места для досуга
	if data, ok := fallbackDataCache["leisure_facilities"]; ok {
		if containsAny(q, "досуг", "отдых", "перерыв", "пара", "буфет", "тусить") {
			return data.Content, true
		}
	}

	// 13. Стоимость обучения
	if data, ok := fallbackDataCache["tuition_all_specialties"]; ok {
		if containsAny(q, "стоимост", "цена", "оплат", "бабк", "платить", "денег", "рубл") &&
			!containsAny(q, "материнск", "капитал", "рассрочк") {
			if data.Specialties != nil {
				var result strings.Builder
				result.WriteString("Стоимость обучения в колледже «Номос» (2026 год):\n\n")
				for code, spec := range data.Specialties {
					specMap := spec.(map[string]interface{})
					result.WriteString(fmt.Sprintf("**%s %s** — %s\n", code, specMap["name"], specMap["cost_description"]))
				}
				result.WriteString("\nОплата производится помесячно или по семестрам согласно договору.")
				return result.String(), true
			}
		}
	}

	// 13a. Практика для студентов
	if data, ok := fallbackDataCache["student_practice"]; ok {
		if containsAny(q, "практик") && (containsAny(q, "есть", "ли", "студент", "проходят", "где", "как", "организ", "вид") ||
			strings.Contains(q, "?")) {
			return data.Content, true
		}
	}

	// 14. Контактная информация
	if data, ok := fallbackDataCache["contact_info"]; ok {
		if containsAny(q, "телефон", "тел", "звонить", "позвонить") ||
			containsAny(q, "email", "почт", "емейл", "e-mail", "мейл", "адрес электронн") ||
			containsAny(q, "контакт", "связаться", "написать") {
			return data.Content, true
		}
	}

	// 15. Все специальности (общий вопрос - проверяем последним)
	if data, ok := fallbackDataCache["all_specialties"]; ok {
		if containsAny(q, "специальност", "направлен", "программ") &&
			containsAny(q, "как", "все", "список", "перечисл") {
			return data.Content, true
		}
	}

	// УДАЛЕНЫ fallback категории для:
	// - college_history (есть на сайте)
	// - unknown_person (мешает поиску преподавателей)
	// - teachers_info (мешает поиску преподавателей)
	// - general_info_missing (слишком широкий)
	
	// Эти категории перехватывали запросы ДО поиска в БД

	return "", false
}

// buildUserPrompt создаёт user prompt с историей диалога и контекстом
func buildUserPrompt(question, contextText string, dialogContext *DialogContext) string {
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

	return promptBuilder.String()
}

// =========================================================
// ANSWER CONTEXT
// =========================================================

type answerContext struct {
	Intent         Intent
	ContextText    string
	Sources        []Source
	FragmentIDs    []int64 // ID использованных фрагментов для кеша
	DirectReply    string  // Для приветствий и уточнений
	ShouldGenerate bool    // false если есть DirectReply
}

// =========================================================
// ANSWER GENERATION
// =========================================================

// prepareAnswerContext выполняет общую логику подготовки: понимание намерения, поиск материалов
// Возвращает либо готовый ответ (уточнение/приветствие), либо контекст для генерации
func prepareAnswerContext(ctx context.Context, cfg Config, question string, dialogContext *DialogContext) (*answerContext, error) {
	// DEBUG: Проверяем кодировку вопроса
	log.Printf("🔍 prepareAnswerContext: question length=%d, runes=%d", len(question), len([]rune(question)))
	log.Printf("🔍 prepareAnswerContext: first 20 runes: %v", []rune(question)[:min(20, len([]rune(question)))])

	// ШАГ 1: Понимание контекста вопроса
	intent := UnderstandIntent(question, dialogContext)
	log.Printf("INTENT: type=%s topic=%s entities=%v confidence=%.2f",
		intent.Type, intent.Topic, intent.Entities, intent.Confidence)

	// 2. Проверка неоднозначности
	// Не спрашиваем уточнение, если это ответ на предыдущее уточнение
	if intent.IsAmbiguous && intent.Type != "clarification" {
		clarification := BuildClarificationQuestion(intent, dialogContext)
		if clarification != "" {
			// Сохраняем контекст для будущего уточнения
			if dialogContext != nil {
				dialogContext.PendingQuestion = question
				dialogContext.ExpectedParameter = "specialty" // Чаще всего требуется специальность
				// Сохраняем тему вместе с другими данными
				if dialogContext.PartialInfo == nil {
					dialogContext.PartialInfo = make(map[string]string)
				}
				// Копируем entities
				for k, v := range intent.Entities {
					dialogContext.PartialInfo[k] = v
				}
				// Сохраняем тему для восстановления после уточнения
				dialogContext.PartialInfo["topic"] = intent.Topic
				log.Printf("ASKING CLARIFICATION: topic='%s' entities=%v", intent.Topic, intent.Entities)
			}
			return &answerContext{
				Intent:         intent,
				DirectReply:    clarification,
				ShouldGenerate: false,
			}, nil
		}
	}

	// 3. Обработка приветствий
	if intent.Type == "greeting" {
		return &answerContext{
			Intent:         intent,
			DirectReply:    "Здравствуйте! Я Эрудит — помощник Воронежского колледжа «Номос». Чем могу помочь?",
			ShouldGenerate: false,
		}, nil
	}

	if (intent.Topic == "payment" || isTuitionQuestion(intent.Question)) &&
		intent.Entities["specialty_code"] != "" {
		answer, found, err := loadSpecialtyTuition(ctx, globalDB, cfg.NOMOSBase, intent.Entities["specialty_code"])
		if err != nil {
			return nil, fmt.Errorf("ошибка чтения стоимости обучения: %w", err)
		}
		if found {
			return &answerContext{
				Intent:         intent,
				DirectReply:    answer.Text,
				Sources:        []Source{answer.Source},
				ShouldGenerate: false,
			}, nil
		}
	}

	// ШАГ 2: Поиск информации
	// 2.1 Подготовка поисковых запросов
	queries := PrepareSearchQuery(intent)
	log.Printf("SEARCH QUERIES: %d запросов", len(queries))

	log.Printf("GenerateAnswer: Before SearchMaterials")

	// 2.2 Поиск материалов с использованием продвинутого гибридного поиска
	results, err := SearchMaterialsWithAdvancedEngine(ctx, globalDB, cfg, queries, dialogContext)
	if err != nil {
		return nil, fmt.Errorf("ошибка поиска: %w", err)
	}

	log.Printf("SEARCH RESULTS: найдено %d фрагментов", len(results))

	// ПЕРВЫЙ: Проверяем fallback ДО проверки результатов поиска
	if fallbackAnswer, found := getFallbackAnswer(question); found {
		log.Printf("✅ FALLBACK DATA HIT (early check): question='%s'", question)
		fallbackSource := Source{
			Name: "База знаний колледжа",
			URL:  "https://college-nomos.ru",
			Kind: "fallback",
		}
		return &answerContext{
			Intent:         intent,
			DirectReply:    fallbackAnswer,
			Sources:        []Source{fallbackSource},
			ShouldGenerate: false,
		}, nil
	}

	// 6. Проверка достаточности
	sufficient := false
	if len(results) > 0 && len(queries) > 0 {
		sufficient = CheckSufficiency(queries[0], results)
	}

	if !sufficient && len(results) < 3 && len(queries) > 0 {
		// Повторный поиск
		log.Printf("Результаты недостаточны, выполняем повторный поиск")
		retryResults, err := RetrySearchWithContext(ctx, cfg, queries[0], 1, dialogContext)
		if err == nil && len(retryResults) > 0 {
			results = append(results, retryResults...)
			// Повторная дедупликация и сортировка
			results = deduplicateResults(results)
		}
	}

	if len(results) == 0 {
		// ПРИОРИТЕТ: Проверяем fallback данные перед "не найдено"
		if fallbackAnswer, found := getFallbackAnswer(question); found {
			log.Printf("✅ FALLBACK DATA HIT (no search results): question='%s'", question)
			fallbackSource := Source{
				Name: "База знаний колледжа",
				URL:  "https://college-nomos.ru",
				Kind: "fallback",
			}
			return &answerContext{
				Intent:         intent,
				DirectReply:    fallbackAnswer,
				Sources:        []Source{fallbackSource},
				ShouldGenerate: false,
			}, nil
		}

		return &answerContext{
			Intent:         intent,
			DirectReply:    "К сожалению, на сайте колледжа я не нашёл информации по этому вопросу. Уточните, пожалуйста, в приёмной комиссии по телефону +7 (473) 271-35-36.",
			ShouldGenerate: false,
		}, nil
	}

	// ШАГ 3: Подготовка контекста для модели
	// РЕФАКТОРИНГ: Жёсткое ограничение на 3-5 фрагментов (было 8)
	contextText, sources, fragmentIDs := buildContextFromResults(results, 5)
	log.Printf("CONTEXT: %d символов из %d источников, %d фрагментов", len(contextText), len(sources), len(fragmentIDs))

	// DEBUG: Логируем первые 500 символов контекста для отладки
	if len(contextText) > 0 {
		preview := contextText
		if len(preview) > 500 {
			preview = preview[:500]
		}
		log.Printf("CONTEXT PREVIEW: %s...", preview)
	}

	return &answerContext{
		Intent:         intent,
		ContextText:    contextText,
		Sources:        sources,
		FragmentIDs:    fragmentIDs,
		ShouldGenerate: true,
	}, nil
}

func isTuitionQuestion(question string) bool {
	return containsAny(normalize(question), "стоим", "цен", "оплат", "платн", "сколько стоит")
}

type specialtyTuitionAnswer struct {
	Text   string
	Source Source
}

var annualTuitionPattern = regexp.MustCompile(`(?i)стоимость обучения\s*:\s*([0-9][0-9 ]*)\s*руб(?:лей|ля|ль)?\s*в год`)

func loadSpecialtyTuition(ctx context.Context, db *sql.DB, nomosBase, specialtyCode string) (specialtyTuitionAnswer, bool, error) {
	if db == nil {
		return specialtyTuitionAnswer{}, false, nil
	}

	url := strings.TrimRight(nomosBase, "/") + "/abitur/postupayushchim/"
	var pageText, academicYear string
	err := db.QueryRowContext(ctx,
		"SELECT clean_text, academic_year FROM pages WHERE url = ? ORDER BY fetched_at DESC LIMIT 1",
		url,
	).Scan(&pageText, &academicYear)
	if err != nil {
		if err == sql.ErrNoRows {
			return specialtyTuitionAnswer{}, false, nil
		}
		return specialtyTuitionAnswer{}, false, err
	}

	section, ok := specialtySection(pageText, specialtyCode)
	if !ok {
		return specialtyTuitionAnswer{}, false, nil
	}

	match := annualTuitionPattern.FindStringSubmatch(section)
	if len(match) < 2 {
		return specialtyTuitionAnswer{}, false, nil
	}
	amount := strings.Join(strings.Fields(match[1]), " ")
	if amount == "" {
		return specialtyTuitionAnswer{}, false, nil
	}

	var answer strings.Builder
	fmt.Fprintf(&answer, "Стоимость обучения по специальности %s — %s рублей в год", specialtyCode, amount)
	if academicYear != "" {
		fmt.Fprintf(&answer, " на %s учебный год", academicYear)
	}
	answer.WriteByte('.')
	if strings.Contains(strings.ToLower(section), "оплата вносится помесячно") {
		answer.WriteString(" Оплата вносится помесячно.")
	}

	return specialtyTuitionAnswer{
		Text: answer.String(),
		Source: Source{
			Name: "Поступающим",
			URL:  url,
			Kind: "admission",
		},
	}, true, nil
}

func specialtySection(pageText, specialtyCode string) (string, bool) {
	textLower := strings.ToLower(pageText)
	codeLower := strings.ToLower(specialtyCode)
	heading := "поступление на специальность"

	for offset := 0; offset < len(textLower); {
		codeIndex := strings.Index(textLower[offset:], codeLower)
		if codeIndex < 0 {
			return "", false
		}
		codeIndex += offset

		sectionStart := strings.LastIndex(textLower[:codeIndex], heading)
		if sectionStart >= 0 {
			nextHeading := strings.Index(textLower[codeIndex+len(codeLower):], heading)
			sectionEnd := len(pageText)
			if nextHeading >= 0 {
				sectionEnd = codeIndex + len(codeLower) + nextHeading
			}
			section := pageText[sectionStart:sectionEnd]
			if annualTuitionPattern.MatchString(section) {
				return section, true
			}
		}
		offset = codeIndex + len(codeLower)
	}

	return "", false
}

func shouldFinalizeDirectReply(answerContext *answerContext) bool {
	if answerContext.Intent.Type == "greeting" || strings.Contains(answerContext.DirectReply, "не нашёл") {
		return true
	}
	return answerContext.Intent.Topic == "payment" &&
		len(answerContext.Sources) > 0 &&
		strings.Contains(answerContext.DirectReply, "Стоимость обучения")
}

// finalizeDialogTurn обновляет состояние диалога после получения ответа
// Вызывается ТОЛЬКО для успешно завершенных ходов
func finalizeDialogTurn(question, answer string, intent Intent, sources []Source, dialogContext *DialogContext) {
	if dialogContext == nil {
		return
	}

	// Обновляем контекст диалога
	dialogContext.CurrentTopic = intent.Topic

	// Инициализируем LastEntities, если он nil
	if dialogContext.LastEntities == nil {
		dialogContext.LastEntities = make(map[string]string)
	}

	log.Printf("CONTEXT UPDATE: Before update - LastEntities=%v, intent.Entities=%v", dialogContext.LastEntities, intent.Entities)

	// Объединяем сущности: сохраняем старые, если новые не переопределили их
	if len(intent.Entities) > 0 {
		// Если есть новые сущности, обновляем только их
		for k, v := range intent.Entities {
			dialogContext.LastEntities[k] = v
		}
	}
	// Если сущностей нет вообще, не трогаем LastEntities (сохраняем контекст)

	log.Printf("CONTEXT UPDATE: After update - LastEntities=%v", dialogContext.LastEntities)

	dialogContext.LastSources = sources

	turn := DialogTurn{
		UserMessage: question,
		BotReply:    answer,
		Intent:      intent,
		Sources:     sources,
		Timestamp:   time.Now().Format(time.RFC3339),
	}
	dialogContext.History = append(dialogContext.History, turn)

	// Ограничиваем историю последними 5 шагами
	if len(dialogContext.History) > 5 {
		dialogContext.History = dialogContext.History[len(dialogContext.History)-5:]
	}

	log.Printf("DIALOG: turn added, history size=%d, topic=%s", len(dialogContext.History), intent.Topic)
}

// GenerateAnswer формирует ответ на вопрос (без streaming)
func GenerateAnswer(ctx context.Context, cfg Config, llmProvider LLMProvider, question string, dialogContext *DialogContext) (string, []Source, []int64, error) {
	startTime := time.Now()
	log.Printf("GenerateAnswer: START")

	// DEBUG: Проверяем кодировку вопроса
	log.Printf("🔍 GenerateAnswer: question length=%d, runes=%d", len(question), len([]rune(question)))
	log.Printf("🔍 GenerateAnswer: first 30 runes: %v", []rune(question)[:min(30, len([]rune(question)))])

	// ПРИОРИТЕТ 1: Проверяем fallback данные ПЕРВЫМ делом
	if fallbackAnswer, found := getFallbackAnswer(question); found {
		log.Printf("✅ FALLBACK DATA HIT: question='%s'", question)
		// Создаем фиктивный source для fallback данных
		fallbackSource := Source{
			Name: "База знаний колледжа",
			URL:  "https://college-nomos.ru",
			Kind: "fallback",
		}
		// Записываем в историю диалога
		if dialogContext != nil {
			intent := UnderstandIntent(question, dialogContext)
			finalizeDialogTurn(question, fallbackAnswer, intent, []Source{fallbackSource}, dialogContext)
		}
		return fallbackAnswer, []Source{fallbackSource}, nil, nil
	}

	// Подготовка контекста
	ansCtx, err := prepareAnswerContext(ctx, cfg, question, dialogContext)
	if err != nil {
		return "", nil, nil, err
	}

	// Если есть готовый ответ (приветствие/уточнение/не найдено)
	if !ansCtx.ShouldGenerate {
		// Для приветствий и "не найдено" тоже записываем в историю
		if shouldFinalizeDirectReply(ansCtx) {
			finalizeDialogTurn(question, ansCtx.DirectReply, ansCtx.Intent, ansCtx.Sources, dialogContext)
		}
		// Для уточнений НЕ записываем - ждем полного ответа
		elapsed := time.Since(startTime)
		log.Printf("DIRECT REPLY: %v", elapsed)
		return ansCtx.DirectReply, ansCtx.Sources, nil, nil
	}

	// Генерация ответа через LLM Provider
	log.Printf("GenerateAnswer: Before LLM generation")
	// ВАЖНО: Используем intent.Question, который содержит восстановленный полный вопрос
	// (например, "стоимость обучения по специальности дизайн" вместо "дизайн интересует")
	questionForLLM := ansCtx.Intent.Question
	if questionForLLM == "" {
		questionForLLM = question // fallback на исходный вопрос
	}
	log.Printf("QUESTION TO LLM: original='%s' intent='%s' using='%s'", question, ansCtx.Intent.Question, questionForLLM)

	// Формируем промпт (используем существующую логику)
	systemPrompt := buildAnswerInstructions(questionForLLM)
	userPrompt := buildUserPrompt(questionForLLM, ansCtx.ContextText, dialogContext)

	llmReq := LLMRequest{
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		Temperature:  0.2,
		MaxTokens:    predictionLimit(questionForLLM),
	}

	llmResp, err := llmProvider.Generate(ctx, llmReq)
	if err != nil {
		fallback := llmUnavailableMessage(err)
		if !errors.Is(err, ErrLLMRateLimited) {
			var ok bool
			fallback, ok = evidenceFallbackAnswer(questionForLLM, ansCtx.ContextText,
				"Сейчас ИИ-модель временно недоступна. Вот наиболее релевантные сведения с сайта колледжа:")
			if !ok {
				log.Printf("LLM UNAVAILABLE: no sufficiently relevant evidence for fallback: %v", err)
				fallback = "Сейчас ИИ-модель временно недоступна, а в найденных материалах недостаточно точных сведений для надежного ответа. Попробуйте задать вопрос позже."
			}
		}
		log.Printf("LLM UNAVAILABLE: returning fallback response: %v", err)
		finalizeDialogTurn(question, fallback, ansCtx.Intent, ansCtx.Sources, dialogContext)
		return fallback, ansCtx.Sources, nil, nil
	}

	answer := cleanLLMAnswer(llmResp.Content)
	answer = ensureCompleteAnswer(answer)

	log.Printf("GenerateAnswer: After LLM generation, answer length: %d", len(answer))

	// КРИТИЧЕСКИ ВАЖНО: Проверяем ответ против evidence перед возвратом
	verification := verifyAnswer(answer, ansCtx.ContextText, ansCtx.Sources)

	// ВРЕМЕННО: логируем результат верификации, но не отклоняем ответы
	if !verification.Passed {
		// Ответ содержит неподтверждённые утверждения
		log.Printf("VERIFICATION WARNING: %d unsupported claims (accepting anyway)", len(verification.UnsupportedClaims))
		for _, claim := range verification.UnsupportedClaims {
			log.Printf("  - Unsupported: type=%s value='%s'", claim.Type, claim.Value)
		}
		// НЕ отклоняем ответ - продолжаем
	} else {
		log.Printf("VERIFICATION PASSED: answer is safe")
	}

	// Обновляем историю диалога ТОЛЬКО с проверенным ответом
	finalizeDialogTurn(question, answer, ansCtx.Intent, ansCtx.Sources, dialogContext)

	elapsed := time.Since(startTime)
	log.Printf("ANSWER READY: %v", elapsed)

	return answer, ansCtx.Sources, ansCtx.FragmentIDs, nil
}

func evidenceFallbackAnswer(question, contextText, introduction string) (string, bool) {
	stopWords := map[string]struct{}{
		"а": {}, "без": {}, "бы": {}, "был": {}, "была": {}, "были": {},
		"быть": {}, "в": {}, "во": {}, "вы": {}, "где": {}, "для": {},
		"до": {}, "его": {}, "ее": {}, "её": {}, "если": {}, "есть": {},
		"же": {}, "за": {}, "и": {}, "из": {}, "или": {}, "им": {},
		"их": {}, "как": {}, "какая": {}, "какие": {}, "какой": {},
		"когда": {}, "кто": {}, "ли": {}, "мне": {}, "на": {}, "над": {},
		"не": {}, "него": {}, "нее": {}, "ни": {}, "но": {},
		"о": {}, "об": {}, "от": {}, "по": {}, "под": {}, "при": {},
		"про": {}, "с": {}, "со": {}, "так": {}, "такая": {}, "также": {},
		"такой": {}, "там": {}, "те": {}, "то": {}, "чем": {}, "что": {},
		"эта": {}, "это": {}, "этот": {}, "я": {},
	}
	queryTerms := make(map[string]struct{})
	for _, token := range tokenize(question) {
		token = stem(token)
		if len([]rune(token)) < 3 {
			continue
		}
		if _, stop := stopWords[token]; !stop {
			queryTerms[token] = struct{}{}
		}
	}
	minimumMatches := (len(queryTerms) + 1) / 2
	nameTerms := namedEntityTerms(question)
	if len(nameTerms) >= 2 {
		minimumMatches = len(queryTerms)
	}
	documentListQuestion := containsAny(normalize(question),
		"какие документы", "нужны документы", "документы для поступления", "что нужно для поступления")
	if containsAny(normalize(question), "сколько", "стоим", "цен", "оплат", "плат") {
		queryTerms["стоим"] = struct{}{}
		queryTerms["цен"] = struct{}{}
		queryTerms["рубл"] = struct{}{}
	}
	if len(queryTerms) == 0 {
		return "", false
	}

	type candidate struct {
		text  string
		score int
		order int
	}
	var candidates []candidate
	seenSentences := make(map[string]struct{})
	fragmentOrder := 0
	for _, fragment := range strings.Split(contextText, "=== Фрагмент") {
		if fragment == "" {
			continue
		}
		fragmentOrder++
		var content strings.Builder
		for _, line := range strings.Split(fragment, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "===") ||
				strings.HasPrefix(line, "релевантность:") ||
				strings.HasPrefix(line, "Источник:") ||
				strings.HasPrefix(line, "Дата:") {
				continue
			}
			if content.Len() > 0 {
				content.WriteByte('\n')
			}
			content.WriteString(line)
		}

		for _, sentence := range splitEvidenceSentences(content.String()) {
			sentence = strings.TrimSpace(sentence)
			if len([]rune(sentence)) < 20 {
				continue
			}
			normalizedSentence := normalize(sentence)
			if strings.Contains(sentence, "?") ||
				strings.Contains(normalizedSentence, "здравствуйте") ||
				strings.HasSuffix(normalizedSentence, "см.") ||
				containsAny(normalizedSentence, "нужна ли", "нужно ли", "можно ли") ||
				strings.Count(sentence, "(") > strings.Count(sentence, ")") {
				continue
			}
			if len(nameTerms) >= 2 && !containsAllTokens(sentence, nameTerms) {
				continue
			}
			if documentListQuestion && !containsAny(normalizedSentence,
				"заявлен", "аттест", "паспорт", "свидетельств", "справк", "снилс",
				"копи", "фото", "документ об образован", "перечень документ", "оригинал") {
				continue
			}
			if _, seen := seenSentences[normalizedSentence]; seen {
				continue
			}
			matched := 0
			for _, token := range tokenize(sentence) {
				word := stem(token)
				for queryTerm := range queryTerms {
					if word == queryTerm ||
						(len([]rune(queryTerm)) >= 5 && (strings.Contains(word, queryTerm) || strings.Contains(queryTerm, word))) {
						matched++
						break
					}
				}
			}
			if matched < minimumMatches {
				continue
			}
			score := matched*10 - fragmentOrder
			if informativeWords := len(tokenize(sentence)) - matched; informativeWords > 0 {
				score += min(informativeWords, 4)
			}
			if len([]rune(sentence)) > 500 {
				score -= 2
			}
			candidates = append(candidates, candidate{text: sentence, score: score, order: fragmentOrder})
			seenSentences[normalizedSentence] = struct{}{}
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].order < candidates[j].order
		}
		return candidates[i].score > candidates[j].score
	})
	if len(candidates) == 0 {
		return "", false
	}

	var answer strings.Builder
	answer.WriteString(introduction)
	maxSentences := 4
	maxAnswerRunes := 1400
	selectedSentences := make([]string, 0, maxSentences)
	for i := 0; i < len(candidates) && i < maxSentences; i++ {
		sentence := candidates[i].text
		normalizedSentence := strings.Trim(normalize(sentence), " .,!?:;")
		duplicate := false
		for _, selected := range selectedSentences {
			normalizedSelected := strings.Trim(normalize(selected), " .,!?:;")
			if strings.Contains(normalizedSelected, normalizedSentence) ||
				strings.Contains(normalizedSentence, normalizedSelected) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		entry := "\n\n• " + sentence
		if len([]rune(answer.String()+entry)) > maxAnswerRunes {
			if i == 0 {
				return "", false
			}
			break
		}
		answer.WriteString(entry)
		selectedSentences = append(selectedSentences, sentence)
	}
	return answer.String(), true
}

func llmUnavailableMessage(err error) string {
	if errors.Is(err, ErrLLMRateLimited) {
		return "Gemini временно не отвечает: исчерпан лимит запросов API (HTTP 429). Подождите сброса квоты или проверьте лимиты и оплату проекта в Google AI Studio."
	}
	return "Сейчас ИИ-модель временно недоступна. Попробуйте задать вопрос позже."
}

func namedEntityTerms(question string) []string {
	questionWords := map[string]struct{}{
		"где": {}, "зачем": {}, "как": {}, "какая": {}, "какие": {},
		"какой": {}, "когда": {}, "кто": {}, "почему": {}, "что": {},
		"такой": {}, "такая": {}, "такое": {}, "такие": {},
		"чем": {}, "чья": {}, "чье": {}, "чьи": {}, "чей": {},
	}
	letterSeparators := regexp.MustCompile(`[^\p{L}-]+`)
	var names []string
	for _, word := range letterSeparators.Split(question, -1) {
		runes := []rune(word)
		if len(runes) == 0 || !unicode.IsUpper(runes[0]) {
			continue
		}
		if strings.ToUpper(word) == word {
			continue
		}
		if _, questionWord := questionWords[strings.ToLower(word)]; !questionWord {
			names = append(names, normalize(word))
		}
	}
	return names
}

func containsAllTokens(text string, expected []string) bool {
	tokens := make(map[string]struct{})
	for _, token := range tokenize(text) {
		tokens[token] = struct{}{}
	}
	for _, term := range expected {
		if _, found := tokens[term]; !found {
			return false
		}
	}
	return true
}

func splitEvidenceSentences(text string) []string {
	var sentences []string
	for _, line := range strings.Split(text, "\n") {
		start := 0
		for i := 0; i < len(line); i++ {
			if line[i] != '.' && line[i] != '!' && line[i] != '?' && line[i] != ';' {
				continue
			}
			if line[i] == '.' && i > 0 && line[i-1] >= '0' && line[i-1] <= '9' {
				continue
			}
			if i+1 < len(line) && (line[i+1] == ' ' || line[i+1] == '\t') {
				if sentence := strings.TrimSpace(line[start : i+1]); sentence != "" {
					sentences = append(sentences, sentence)
				}
				start = i + 1
			}
		}
		if sentence := strings.TrimSpace(line[start:]); sentence != "" {
			sentences = append(sentences, sentence)
		}
	}
	return sentences
}

// buildContextFromResults собирает контекст из результатов поиска с ограничением токенов
func buildContextFromResults(results []SearchResult, maxFragments int) (string, []Source, []int64) {
	// РЕФАКТОРИНГ: Жёсткое ограничение на количество фрагментов (3-5)
	// Это критически важно для снижения нагрузки на токен-лимиты LLM
	const hardMaxFragments = 5
	if maxFragments > hardMaxFragments {
		maxFragments = hardMaxFragments
	}

	if len(results) > maxFragments {
		results = results[:maxFragments]
	}

	var sb strings.Builder
	sourcesMap := make(map[string]Source)
	var fragmentIDs []int64

	// РЕФАКТОРИНГ: Подсчитываем приблизительное количество токенов
	// Примерно 1 токен = 4 символа для русского текста
	const maxTotalTokens = 1200
	const avgCharsPerToken = 4
	maxTotalChars := maxTotalTokens * avgCharsPerToken
	currentChars := 0

	for i, result := range results {
		// Проверяем лимит токенов перед добавлением фрагмента
		fragmentHeader := fmt.Sprintf("\n=== Фрагмент %d (релевантность: %.2f) ===\n", i+1, result.Relevance)
		fragmentSource := fmt.Sprintf("Источник: %s\n", result.Source.Name)
		fragmentDate := ""
		if result.Date != "" {
			fragmentDate = fmt.Sprintf("Дата: %s\n", result.Date)
		}

		estimatedChars := len(fragmentHeader) + len(fragmentSource) + len(fragmentDate) + len(result.Fragment)

		if currentChars+estimatedChars > maxTotalChars && i > 0 {
			log.Printf("⚠️ TOKEN LIMIT REACHED: stopping at fragment %d (chars: %d/%d)", i, currentChars, maxTotalChars)
			break
		}

		sb.WriteString(fragmentHeader)
		sb.WriteString(fragmentSource)
		if fragmentDate != "" {
			sb.WriteString(fragmentDate)
		}
		sb.WriteString(result.Fragment)
		sb.WriteString("\n")

		currentChars += estimatedChars

		// Собираем уникальные источники
		sourcesMap[result.Source.URL] = result.Source

		// Собираем ID фрагментов для кеша
		if result.FragmentID > 0 {
			fragmentIDs = append(fragmentIDs, result.FragmentID)
		}
	}

	// Преобразуем map в slice
	sources := []Source{}
	for _, src := range sourcesMap {
		sources = append(sources, src)
	}

	approxTokens := currentChars / avgCharsPerToken
	log.Printf("📊 CONTEXT STATS: %d fragments, ~%d tokens (%d chars), %d sources",
		len(fragmentIDs), approxTokens, currentChars, len(sources))

	return sb.String(), sources, fragmentIDs
}

// StreamAnswer генерирует ответ с поддержкой streaming
func StreamAnswer(ctx context.Context, cfg Config, llmProvider LLMProvider, question string, dialogContext *DialogContext, onChunk func(string) error, onSources func([]Source) error, onFragmentIDs func([]int64) error) error {
	startTime := time.Now()
	log.Printf("StreamAnswer: START")

	// Подготовка контекста (та же логика, что и в GenerateAnswer)
	ansCtx, err := prepareAnswerContext(ctx, cfg, question, dialogContext)
	if err != nil {
		return err
	}

	// Если есть готовый ответ (приветствие/уточнение/не найдено)
	if !ansCtx.ShouldGenerate {
		// Отправляем готовый ответ через onChunk
		if err := onChunk(ansCtx.DirectReply); err != nil {
			return err
		}

		// Для приветствий и "не найдено" записываем в историю
		if shouldFinalizeDirectReply(ansCtx) {
			finalizeDialogTurn(question, ansCtx.DirectReply, ansCtx.Intent, ansCtx.Sources, dialogContext)
		}
		// Для уточнений НЕ записываем - ждем полного ответа

		elapsed := time.Since(startTime)
		log.Printf("STREAM DIRECT REPLY: %v", elapsed)
		return nil
	}

	// Отправляем источники сразу после подготовки контекста
	if onSources != nil && len(ansCtx.Sources) > 0 {
		if err := onSources(ansCtx.Sources); err != nil {
			return err
		}
	}

	// Отправляем fragmentIDs для кеширования
	if onFragmentIDs != nil && len(ansCtx.FragmentIDs) > 0 {
		if err := onFragmentIDs(ansCtx.FragmentIDs); err != nil {
			return err
		}
	}

	// Собираем ответ для записи в историю
	var collectedAnswer strings.Builder
	wrappedOnChunk := func(chunk string) error {
		collectedAnswer.WriteString(chunk)
		return onChunk(chunk)
	}

	// ВАЖНО: Используем intent.Question, который содержит восстановленный полный вопрос
	questionForLLM := ansCtx.Intent.Question
	if questionForLLM == "" {
		questionForLLM = question
	}

	// Формируем промпт
	systemPrompt := buildAnswerInstructions(questionForLLM)
	userPrompt := buildUserPrompt(questionForLLM, ansCtx.ContextText, dialogContext)

	llmReq := LLMRequest{
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		Temperature:  0.2,
		MaxTokens:    predictionLimit(questionForLLM),
	}

	// Streaming запрос к LLM Provider
	log.Printf("StreamAnswer: Before LLM streaming")
	err = llmProvider.Stream(ctx, llmReq, wrappedOnChunk)
	if err != nil {
		log.Printf("StreamAnswer: LLM stream error: %v", err)
		if collectedAnswer.Len() > 0 {
			return err
		}
		fallback := llmUnavailableMessage(err)
		if !errors.Is(err, ErrLLMRateLimited) {
			var ok bool
			fallback, ok = evidenceFallbackAnswer(questionForLLM, ansCtx.ContextText,
				"Сейчас ИИ-модель временно недоступна. Вот наиболее релевантные сведения с сайта колледжа:")
			if !ok {
				fallback = "Сейчас ИИ-модель временно недоступна, а в найденных материалах недостаточно точных сведений для надежного ответа. Попробуйте задать вопрос позже."
			}
		}
		if err := onChunk(fallback); err != nil {
			return err
		}
		finalizeDialogTurn(question, fallback, ansCtx.Intent, ansCtx.Sources, dialogContext)
		return nil
	}

	// ВАЖНО: обновляем историю ТОЛЬКО после успешного завершения потока
	answer := collectedAnswer.String()
	if answer != "" {
		finalizeDialogTurn(question, answer, ansCtx.Intent, ansCtx.Sources, dialogContext)
	}

	elapsed := time.Since(startTime)
	log.Printf("STREAM COMPLETE: %v", elapsed)

	return nil
}
