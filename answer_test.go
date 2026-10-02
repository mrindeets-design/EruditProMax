package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestPrepareAnswerContext проверяет общую логику подготовки контекста
func TestPrepareAnswerContext(t *testing.T) {
	cfg := Config{
		OllamaURL:   "http://localhost:11434",
		OllamaModel: "llama3.1:8b",
	}
	
	tests := []struct {
		name              string
		question          string
		context           *DialogContext
		wantShouldGenerate bool
		wantDirectReply   string
		wantIntent        string
		skipMaterialCheck bool // Пропустить проверку материалов
	}{
		{
			name:              "Приветствие",
			question:          "Здравствуйте",
			context:           &DialogContext{},
			wantShouldGenerate: false,
			wantDirectReply:   "Здравствуйте! Я Эрудит",
			wantIntent:        "greeting",
		},
		{
			name:              "Уточнение специальности",
			question:          "Какая стоимость обучения?",
			context:           &DialogContext{LastEntities: make(map[string]string)},
			wantShouldGenerate: false,
			wantDirectReply:   "о какой специальности",
			wantIntent:        "question",
		},
		{
			name:     "Ответ на уточнение - Intent",
			question: "Дизайн",
			context: &DialogContext{
				PendingQuestion:   "Какая стоимость обучения?",
				ExpectedParameter: "specialty",
				PartialInfo:       map[string]string{"topic": "payment"},
				LastEntities:      make(map[string]string),
			},
			wantIntent:        "clarification",
			skipMaterialCheck: true, // Проверяем только Intent без загрузки материалов
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipMaterialCheck {
				// Проверяем только логику Intent без загрузки материалов
				intent := UnderstandIntent(tt.question, tt.context)
				if intent.Type != tt.wantIntent {
					t.Errorf("Intent.Type = %q, want %q", intent.Type, tt.wantIntent)
				}
				return
			}
			
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			
			ansCtx, err := prepareAnswerContext(ctx, cfg, tt.question, tt.context)
			if err != nil {
				t.Fatalf("prepareAnswerContext error: %v", err)
			}
			
			if ansCtx.ShouldGenerate != tt.wantShouldGenerate {
				t.Errorf("ShouldGenerate = %v, want %v", ansCtx.ShouldGenerate, tt.wantShouldGenerate)
			}
			
			if tt.wantDirectReply != "" && !strings.Contains(ansCtx.DirectReply, tt.wantDirectReply) {
				t.Errorf("DirectReply = %q, want to contain %q", ansCtx.DirectReply, tt.wantDirectReply)
			}
			
			if ansCtx.Intent.Type != tt.wantIntent {
				t.Errorf("Intent.Type = %q, want %q", ansCtx.Intent.Type, tt.wantIntent)
			}
		})
	}
}

// TestFinalizeDialogTurn проверяет обновление состояния диалога
func TestFinalizeDialogTurn(t *testing.T) {
	tests := []struct {
		name             string
		question         string
		answer           string
		intent           Intent
		sources          []Source
		initialHistory   []DialogTurn
		wantHistorySize  int
		wantTopic        string
	}{
		{
			name:     "Первый ход",
			question: "Здравствуйте",
			answer:   "Привет!",
			intent: Intent{
				Type:     "greeting",
				Topic:    "",
				Entities: map[string]string{},
			},
			sources:         []Source{},
			initialHistory:  []DialogTurn{},
			wantHistorySize: 1,
			wantTopic:       "",
		},
		{
			name:     "Добавление в существующую историю",
			question: "Какие специальности?",
			answer:   "У нас есть дизайн, юриспруденция...",
			intent: Intent{
				Type:     "question",
				Topic:    "specialties",
				Entities: map[string]string{},
			},
			sources: []Source{{Name: "Специальности", URL: "https://example.com"}},
			initialHistory: []DialogTurn{
				{UserMessage: "Здравствуйте", BotReply: "Привет!"},
			},
			wantHistorySize: 2,
			wantTopic:       "specialties",
		},
		{
			name:     "Ограничение истории до 5 записей",
			question: "Новый вопрос",
			answer:   "Новый ответ",
			intent: Intent{
				Type:     "question",
				Topic:    "other",
				Entities: map[string]string{},
			},
			sources: []Source{},
			initialHistory: []DialogTurn{
				{UserMessage: "Q1", BotReply: "A1"},
				{UserMessage: "Q2", BotReply: "A2"},
				{UserMessage: "Q3", BotReply: "A3"},
				{UserMessage: "Q4", BotReply: "A4"},
				{UserMessage: "Q5", BotReply: "A5"},
			},
			wantHistorySize: 5, // Должно остаться 5, а не 6
			wantTopic:       "other",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &DialogContext{
				History:      tt.initialHistory,
				LastEntities: make(map[string]string),
			}
			
			finalizeDialogTurn(tt.question, tt.answer, tt.intent, tt.sources, ctx)
			
			if len(ctx.History) != tt.wantHistorySize {
				t.Errorf("History size = %d, want %d", len(ctx.History), tt.wantHistorySize)
			}
			
			if ctx.CurrentTopic != tt.wantTopic {
				t.Errorf("CurrentTopic = %q, want %q", ctx.CurrentTopic, tt.wantTopic)
			}
			
			// Проверяем, что последний элемент содержит наш вопрос
			if len(ctx.History) > 0 {
				lastTurn := ctx.History[len(ctx.History)-1]
				if lastTurn.UserMessage != tt.question {
					t.Errorf("Last turn UserMessage = %q, want %q", lastTurn.UserMessage, tt.question)
				}
				if lastTurn.BotReply != tt.answer {
					t.Errorf("Last turn BotReply = %q, want %q", lastTurn.BotReply, tt.answer)
				}
			}
		})
	}
}


// TestClarificationStateConsistency проверяет обработку уточнений в обоих режимах
func TestClarificationStateConsistency(t *testing.T) {
	cfg := Config{
		OllamaURL:   "http://localhost:11434",
		OllamaModel: "llama3.1:8b",
	}
	
	question := "Какая стоимость обучения?"
	
	// Тест 1: GenerateAnswer должен запросить уточнение
	ctx1 := &DialogContext{
		History:      []DialogTurn{},
		LastEntities: make(map[string]string),
	}
	
	clarification1, _, _, err := GenerateAnswer(context.Background(), cfg, question, ctx1)
	if err != nil {
		t.Fatalf("GenerateAnswer error: %v", err)
	}
	
	// Тест 2: StreamAnswer должен запросить уточнение
	ctx2 := &DialogContext{
		History:      []DialogTurn{},
		LastEntities: make(map[string]string),
	}
	
	var streamedClarification strings.Builder
	err = StreamAnswer(context.Background(), cfg, question, ctx2,
		func(chunk string) error {
			streamedClarification.WriteString(chunk)
			return nil
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("StreamAnswer error: %v", err)
	}
	
	// Оба должны вернуть уточняющий вопрос
	if !strings.Contains(clarification1, "специальность") && !strings.Contains(clarification1, "Уточните") {
		t.Errorf("GenerateAnswer должен запросить уточнение, got: %q", clarification1)
	}
	
	if !strings.Contains(streamedClarification.String(), "специальность") && !strings.Contains(streamedClarification.String(), "Уточните") {
		t.Errorf("StreamAnswer должен запросить уточнение, got: %q", streamedClarification.String())
	}
	
	// Проверяем, что PendingQuestion и PartialInfo сохранены в обоих случаях
	if ctx1.PendingQuestion == "" {
		t.Error("GenerateAnswer не сохранил PendingQuestion")
	}
	if ctx2.PendingQuestion == "" {
		t.Error("StreamAnswer не сохранил PendingQuestion")
	}
	
	if ctx1.PartialInfo == nil || ctx1.PartialInfo["topic"] == "" {
		t.Error("GenerateAnswer не сохранил topic в PartialInfo")
	}
	if ctx2.PartialInfo == nil || ctx2.PartialInfo["topic"] == "" {
		t.Error("StreamAnswer не сохранил topic в PartialInfo")
	}
	
	// История НЕ должна содержать уточнение
	if len(ctx1.History) != 0 {
		t.Errorf("GenerateAnswer не должен записывать уточнение в историю, got %d turns", len(ctx1.History))
	}
	if len(ctx2.History) != 0 {
		t.Errorf("StreamAnswer не должен записывать уточнение в историю, got %d turns", len(ctx2.History))
	}
	
	t.Logf("✓ Состояние уточнений согласовано между режимами")
}

