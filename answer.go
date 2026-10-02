package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// =========================================================
// ANSWER GENERATION
// =========================================================

// answerContext содержит результат подготовки контекста для ответа
type answerContext struct {
	Intent        Intent
	ContextText   string
	Sources       []Source
	FragmentIDs   []int64 // ID использованных фрагментов для кеша
	DirectReply   string  // Для приветствий и уточнений
	ShouldGenerate bool   // false если есть DirectReply
}

// prepareAnswerContext выполняет общую логику подготовки: понимание намерения, поиск материалов
// Возвращает либо готовый ответ (уточнение/приветствие), либо контекст для генерации
func prepareAnswerContext(ctx context.Context, cfg Config, question string, dialogContext *DialogContext) (*answerContext, error) {
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
			DirectReply:    "Здравствуйте! Я Эрудит — помощник Воронежского колледжа «Номос». Могу рассказать о поступлении, специальностях, стоимости обучения, преподавателях и других вопросах. Чем могу помочь?",
			ShouldGenerate: false,
		}, nil
	}
	
	// ШАГ 2: Поиск информации
	// 2.1 Подготовка поисковых запросов
	queries := PrepareSearchQuery(intent)
	log.Printf("SEARCH QUERIES: %d запросов", len(queries))
	
	log.Printf("GenerateAnswer: Before SearchMaterials")
	
	// 2.2 Поиск материалов в базе данных (используем гибридный поиск если доступен)
	results, err := SearchMaterialsWithHybridEngine(ctx, globalDB, cfg, queries, dialogContext)
	if err != nil {
		return nil, fmt.Errorf("ошибка поиска: %w", err)
	}
	
	log.Printf("SEARCH RESULTS: найдено %d фрагментов", len(results))
	
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
		return &answerContext{
			Intent:         intent,
			DirectReply:    "К сожалению, на сайте колледжа я не нашёл информации по этому вопросу. Уточните, пожалуйста, в приёмной комиссии по телефону +7 (473) 271-35-36.",
			ShouldGenerate: false,
		}, nil
	}
	
	// ШАГ 3: Подготовка контекста для модели
	contextText, sources, fragmentIDs := buildContextFromResults(results, 8) // было 5
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
func GenerateAnswer(ctx context.Context, cfg Config, question string, dialogContext *DialogContext) (string, []Source, []int64, error) {
	startTime := time.Now()
	log.Printf("GenerateAnswer: START")
	
	// Подготовка контекста
	ansCtx, err := prepareAnswerContext(ctx, cfg, question, dialogContext)
	if err != nil {
		return "", nil, nil, err
	}
	
	// Если есть готовый ответ (приветствие/уточнение/не найдено)
	if !ansCtx.ShouldGenerate {
		// Для приветствий и "не найдено" тоже записываем в историю
		if ansCtx.Intent.Type == "greeting" || strings.Contains(ansCtx.DirectReply, "не нашёл") {
			finalizeDialogTurn(question, ansCtx.DirectReply, ansCtx.Intent, ansCtx.Sources, dialogContext)
		}
		// Для уточнений НЕ записываем - ждем полного ответа
		elapsed := time.Since(startTime)
		log.Printf("DIRECT REPLY: %v", elapsed)
		return ansCtx.DirectReply, ansCtx.Sources, nil, nil
	}
	
	// Генерация ответа через Ollama
	log.Printf("GenerateAnswer: Before AskOllama")
	answer, err := askOllama(ctx, cfg, question, ansCtx.ContextText, dialogContext)
	if err != nil {
		return "", nil, nil, fmt.Errorf("ошибка Ollama: %w", err)
	}
	log.Printf("GenerateAnswer: After AskOllama, answer length: %d", len(answer))
	
	// Обновляем историю диалога
	finalizeDialogTurn(question, answer, ansCtx.Intent, ansCtx.Sources, dialogContext)
	
	elapsed := time.Since(startTime)
	log.Printf("ANSWER READY: %v", elapsed)
	
	return answer, ansCtx.Sources, ansCtx.FragmentIDs, nil
}

// buildContextFromResults собирает контекст из результатов поиска
func buildContextFromResults(results []SearchResult, maxFragments int) (string, []Source, []int64) {
	if len(results) > maxFragments {
		results = results[:maxFragments]
	}
	
	var sb strings.Builder
	sourcesMap := make(map[string]Source)
	var fragmentIDs []int64
	
	for i, result := range results {
		sb.WriteString(fmt.Sprintf("\n=== Фрагмент %d (релевантность: %.2f) ===\n", i+1, result.Relevance))
		sb.WriteString(fmt.Sprintf("Источник: %s\n", result.Source.Name))
		if result.Date != "" {
			sb.WriteString(fmt.Sprintf("Дата: %s\n", result.Date))
		}
		sb.WriteString(result.Fragment)
		sb.WriteString("\n")
		
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
	
	return sb.String(), sources, fragmentIDs
}

// StreamAnswer генерирует ответ с поддержкой streaming
func StreamAnswer(ctx context.Context, cfg Config, question string, dialogContext *DialogContext, onChunk func(string) error, onSources func([]Source) error, onFragmentIDs func([]int64) error) error {
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
		if ansCtx.Intent.Type == "greeting" || strings.Contains(ansCtx.DirectReply, "не нашёл") {
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
	
	// Streaming запрос к Ollama
	log.Printf("StreamAnswer: Before streamOllama")
	err = streamOllama(ctx, cfg, question, ansCtx.ContextText, dialogContext, wrappedOnChunk)
	if err != nil {
		log.Printf("StreamAnswer: streamOllama error: %v", err)
		return err
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

