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

// GenerateAnswer формирует ответ на вопрос
func GenerateAnswer(ctx context.Context, cfg Config, question string, dialogContext *DialogContext) (string, []Source, error) {
	startTime := time.Now()
	
	log.Printf("GenerateAnswer: START")
	
	// 1. Понимание вопроса
	intent := UnderstandIntent(question, dialogContext)
	log.Printf("INTENT: type=%s topic=%s entities=%v confidence=%.2f", 
		intent.Type, intent.Topic, intent.Entities, intent.Confidence)
	
	// 2. Проверка неоднозначности
	// Не спрашиваем уточнение, если это ответ на предыдущее уточнение
	if intent.IsAmbiguous && intent.Type != "clarification" {
		clarification := BuildClarificationQuestion(intent)
		if clarification != "" {
			// Сохраняем контекст для будущего уточнения
			if dialogContext != nil {
				dialogContext.PendingQuestion = question
				dialogContext.ExpectedParameter = "specialty" // Чаще всего требуется специальность
				dialogContext.PartialInfo = intent.Entities
			}
			return clarification, []Source{}, nil
		}
	}
	
	// 3. Обработка приветствий
	if intent.Type == "greeting" {
		return "Здравствуйте! Я бот колледжа НОМОС. Могу ответить на вопросы о поступлении, специальностях, стоимости обучения и других темах. Задавайте ваш вопрос!", []Source{}, nil
	}
	
	log.Printf("GenerateAnswer: Before PrepareSearchQuery")
	
	// 4. Подготовка поисковых запросов
	queries := PrepareSearchQuery(intent)
	log.Printf("SEARCH QUERIES: %d запросов", len(queries))
	
	log.Printf("GenerateAnswer: Before SearchMaterials")
	
	// 5. Поиск материалов
	results, err := SearchMaterials(ctx, cfg, queries)
	if err != nil {
		return "", nil, fmt.Errorf("ошибка поиска: %w", err)
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
		retryResults, err := RetrySearch(ctx, cfg, queries[0], 1)
		if err == nil && len(retryResults) > 0 {
			results = append(results, retryResults...)
			// Повторная дедупликация и сортировка
			results = deduplicateResults(results)
		}
	}
	
	if len(results) == 0 {
		return "К сожалению, на сайте колледжа я не нашёл информации по этому вопросу. Уточните, пожалуйста, в приёмной комиссии по телефону +7 (473) 271-35-36.", []Source{}, nil
	}
	
	// 7. Подготовка контекста для модели
	// УЛУЧШЕНИЕ: увеличиваем количество фрагментов для контекста
	contextText, sources := buildContextFromResults(results, 8) // было 5
	log.Printf("CONTEXT: %d символов из %d источников", len(contextText), len(sources))
	
	// DEBUG: Логируем первые 500 символов контекста для отладки
	if len(contextText) > 0 {
		preview := contextText
		if len(preview) > 500 {
			preview = preview[:500]
		}
		log.Printf("CONTEXT PREVIEW: %s...", preview)
	}
	
	// 8. Генерация ответа через Ollama
	answer, err := askOllama(ctx, cfg, question, contextText)
	if err != nil {
		return "", nil, fmt.Errorf("ошибка Ollama: %w", err)
	}
	
	// 9. Обновление контекста диалога
	if dialogContext != nil {
		dialogContext.CurrentTopic = intent.Topic
		dialogContext.LastEntities = intent.Entities
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
	}
	
	elapsed := time.Since(startTime)
	log.Printf("ANSWER READY: %v", elapsed)
	
	return answer, sources, nil
}

// buildContextFromResults собирает контекст из результатов поиска
func buildContextFromResults(results []SearchResult, maxFragments int) (string, []Source) {
	if len(results) > maxFragments {
		results = results[:maxFragments]
	}
	
	var sb strings.Builder
	sourcesMap := make(map[string]Source)
	
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
	}
	
	// Преобразуем map в slice
	sources := []Source{}
	for _, src := range sourcesMap {
		sources = append(sources, src)
	}
	
	return sb.String(), sources
}

// StreamAnswer генерирует ответ с поддержкой streaming
func StreamAnswer(ctx context.Context, cfg Config, question string, dialogContext *DialogContext, onChunk func(string) error, onSources func([]Source) error) error {
	// Используем тот же процесс, но с streaming от Ollama
	startTime := time.Now()
	
	intent := UnderstandIntent(question, dialogContext)
	log.Printf("STREAM INTENT: type=%s topic=%s", intent.Type, intent.Topic)
	
	// Не спрашиваем уточнение, если это ответ на предыдущее уточнение
	if intent.IsAmbiguous && intent.Type != "clarification" {
		clarification := BuildClarificationQuestion(intent)
		if clarification != "" {
			// Сохраняем контекст для будущего уточнения
			if dialogContext != nil {
				dialogContext.PendingQuestion = question
				dialogContext.ExpectedParameter = "specialty"
				dialogContext.PartialInfo = intent.Entities
			}
			return onChunk(clarification)
		}
	}
	
	if intent.Type == "greeting" {
		return onChunk("Здравствуйте! Я бот колледжа НОМОС. Могу ответить на вопросы о поступлении, специальностях, стоимости обучения и других темах. Задавайте ваш вопрос!")
	}
	
	queries := PrepareSearchQuery(intent)
	results, err := SearchMaterials(ctx, cfg, queries)
	if err != nil {
		return err
	}
	
	if len(queries) > 0 && !CheckSufficiency(queries[0], results) && len(results) < 3 {
		retryResults, _ := RetrySearch(ctx, cfg, queries[0], 1)
		if len(retryResults) > 0 {
			results = append(results, retryResults...)
			results = deduplicateResults(results)
		}
	}
	
	if len(results) == 0 {
		return onChunk("К сожалению, на сайте колледжа я не нашёл информации по этому вопросу. Уточните, пожалуйста, в приёмной комиссии.")
	}
	
	contextText, sources := buildContextFromResults(results, 8) // УЛУЧШЕНИЕ: было 5
	
	// Отправляем источники сразу
	if onSources != nil {
		onSources(sources)
	}
	
	// Streaming запрос к Ollama
	err = streamOllama(ctx, cfg, question, contextText, onChunk)
	
	if dialogContext != nil {
		dialogContext.CurrentTopic = intent.Topic
		dialogContext.LastEntities = intent.Entities
		dialogContext.LastSources = sources
	}
	
	elapsed := time.Since(startTime)
	log.Printf("STREAM COMPLETE: %v", elapsed)
	
	return err
}
