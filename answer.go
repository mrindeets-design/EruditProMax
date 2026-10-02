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
// Логика работы: 1) Понимание контекста → 2) Поиск информации → 3) Генерация ответа
func GenerateAnswer(ctx context.Context, cfg Config, question string, dialogContext *DialogContext) (string, []Source, error) {
	startTime := time.Now()
	
	log.Printf("GenerateAnswer: START")
	
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
			return clarification, []Source{}, nil
		}
	}
	
	// 3. Обработка приветствий
	if intent.Type == "greeting" {
		return "Здравствуйте! Я Эрудит — помощник Воронежского колледжа «Номос». Могу рассказать о поступлении, специальностях, стоимости обучения, преподавателях и других вопросах. Чем могу помочь?", []Source{}, nil
	}
	
	log.Printf("GenerateAnswer: Before PrepareSearchQuery")
	
	// ШАГ 2: Поиск информации
	// 2.1 Подготовка поисковых запросов
	queries := PrepareSearchQuery(intent)
	log.Printf("SEARCH QUERIES: %d запросов", len(queries))
	
	log.Printf("GenerateAnswer: Before SearchMaterials")
	
	// 2.2 Поиск материалов в базе данных
	results, err := SearchMaterialsWithContext(ctx, cfg, queries, dialogContext)
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
		retryResults, err := RetrySearchWithContext(ctx, cfg, queries[0], 1, dialogContext)
		if err == nil && len(retryResults) > 0 {
			results = append(results, retryResults...)
			// Повторная дедупликация и сортировка
			results = deduplicateResults(results)
		}
	}
	
	if len(results) == 0 {
		return "К сожалению, на сайте колледжа я не нашёл информации по этому вопросу. Уточните, пожалуйста, в приёмной комиссии по телефону +7 (473) 271-35-36.", []Source{}, nil
	}
	
	// ШАГ 3: Генерация ответа
	// 3.1 Подготовка контекста для модели
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
	answer, err := askOllama(ctx, cfg, question, contextText, dialogContext)
	if err != nil {
		return "", nil, fmt.Errorf("ошибка Ollama: %w", err)
	}
	
	// 9. Обновление контекста диалога
	if dialogContext != nil {
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
		clarification := BuildClarificationQuestion(intent, dialogContext)
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
		return onChunk("Здравствуйте! Я Эрудит — помощник Воронежского колледжа «Номос». Могу рассказать о поступлении, специальностях, стоимости обучения, преподавателях и других вопросах. Чем могу помочь?")
	}
	
	queries := PrepareSearchQuery(intent)
	results, err := SearchMaterialsWithContext(ctx, cfg, queries, dialogContext)
	if err != nil {
		return err
	}
	
	if len(queries) > 0 && !CheckSufficiency(queries[0], results) && len(results) < 3 {
		retryResults, _ := RetrySearchWithContext(ctx, cfg, queries[0], 1, dialogContext)
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
	err = streamOllama(ctx, cfg, question, contextText, dialogContext, onChunk)
	
	if dialogContext != nil {
		dialogContext.CurrentTopic = intent.Topic
		dialogContext.LastEntities = intent.Entities
		dialogContext.LastSources = sources
	}
	
	elapsed := time.Since(startTime)
	log.Printf("STREAM COMPLETE: %v", elapsed)
	
	return err
}
