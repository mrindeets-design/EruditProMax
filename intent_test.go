package main

import (
	"testing"
)

// TestGreetingDetection проверяет различение приветствий от приветствий с вопросом
func TestGreetingDetection(t *testing.T) {
	tests := []struct {
		name         string
		question     string
		wantType     string
		wantTopic    string
		description  string
	}{
		{
			name:         "pure_greeting",
			question:     "Здравствуйте",
			wantType:     "greeting",
			wantTopic:    "general",
			description:  "Чистое приветствие без вопроса",
		},
		{
			name:         "greeting_with_exclamation",
			question:     "Привет!",
			wantType:     "greeting",
			wantTopic:    "general",
			description:  "Приветствие с восклицательным знаком",
		},
		{
			name:         "greeting_with_question",
			question:     "Здравствуйте, какие документы нужны для поступления?",
			wantType:     "question",
			wantTopic:    "admission",
			description:  "Приветствие + вопрос должно распознаваться как вопрос",
		},
		{
			name:         "greeting_with_question_word",
			question:     "Добрый день, подскажите про стоимость",
			wantType:     "question",
			wantTopic:    "payment",
			description:  "Приветствие + 'подскажите' = вопрос",
		},
		{
			name:         "greeting_with_need",
			question:     "Привет, нужна информация о поступлении",
			wantType:     "question",
			wantTopic:    "admission",
			description:  "Приветствие + 'нужна' = вопрос",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := UnderstandIntent(tt.question, nil)
			
			if intent.Type != tt.wantType {
				t.Errorf("%s: Type = %v, want %v", tt.description, intent.Type, tt.wantType)
			}
			
			if intent.Topic != tt.wantTopic {
				t.Errorf("%s: Topic = %v, want %v", tt.description, intent.Topic, tt.wantTopic)
			}
		})
	}
}

// TestFirstClarification проверяет обработку первого вопроса с уточнением специальности
func TestFirstClarification(t *testing.T) {
	tests := []struct {
		name              string
		question          string
		wantAmbiguous     bool
		wantClarification string
		description       string
	}{
		{
			name:              "payment_without_specialty",
			question:          "Какая стоимость обучения?",
			wantAmbiguous:     true,
			wantClarification: "Уточните, пожалуйста, о какой специальности вы спрашиваете",
			description:       "Вопрос о стоимости без специальности требует уточнения",
		},
		{
			name:              "payment_with_specialty",
			question:          "Какая стоимость обучения на дизайне?",
			wantAmbiguous:     false,
			wantClarification: "",
			description:       "Вопрос о стоимости с специальностью не требует уточнения",
		},
		{
			name:              "general_payment_question",
			question:          "Есть ли бюджетные места?",
			wantAmbiguous:     false,
			wantClarification: "",
			description:       "Общий вопрос 'есть ли' не требует уточнения",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			context := &DialogContext{
				History:      []DialogTurn{},
				CurrentTopic: "",
				LastEntities: make(map[string]string),
			}
			
			intent := UnderstandIntent(tt.question, context)
			
			if intent.IsAmbiguous != tt.wantAmbiguous {
				t.Errorf("%s: IsAmbiguous = %v, want %v", tt.description, intent.IsAmbiguous, tt.wantAmbiguous)
			}
			
			if tt.wantAmbiguous {
				clarification := BuildClarificationQuestion(intent, context)
				if clarification == "" {
					t.Errorf("%s: expected clarification question, got empty string", tt.description)
				}
				// BuildClarificationQuestion не сохраняет PendingQuestion
				// Это делает answer.go после получения уточняющего вопроса
			}
		})
	}
}


// TestClarificationResolution проверяет разрешение уточнения
func TestClarificationResolution(t *testing.T) {
	tests := []struct {
		name            string
		pendingQuestion string
		pendingTopic    string
		userAnswer      string
		wantType        string
		wantTopic       string
		wantSpecialty   string
		wantQuestion    string
		description     string
	}{
		{
			name:            "specialty_clarification",
			pendingQuestion: "Какая стоимость обучения?",
			pendingTopic:    "payment",
			userAnswer:      "Дизайн",
			wantType:        "clarification",
			wantTopic:       "payment",
			wantSpecialty:   "дизайн",
			wantQuestion:    "Какая стоимость обучения? по специальности дизайн",
			description:     "Ответ 'Дизайн' на уточнение специальности",
		},
		{
			name:            "specialty_clarification_lowercase",
			pendingQuestion: "Сколько стоит обучение?",
			pendingTopic:    "payment",
			userAnswer:      "юриспруденция",
			wantType:        "clarification",
			wantTopic:       "payment",
			wantSpecialty:   "юриспруденция",
			wantQuestion:    "Сколько стоит обучение? по специальности юриспруденция",
			description:     "Ответ 'юриспруденция' сохраняет тему payment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			context := &DialogContext{
				History:           []DialogTurn{},
				CurrentTopic:      "",
				LastEntities:      make(map[string]string),
				PendingQuestion:   tt.pendingQuestion,
				ExpectedParameter: "specialty",
				PartialInfo: map[string]string{
					"topic": tt.pendingTopic,
				},
			}
			
			intent := UnderstandIntent(tt.userAnswer, context)
			
			if intent.Type != tt.wantType {
				t.Errorf("%s: Type = %v, want %v", tt.description, intent.Type, tt.wantType)
			}
			
			if intent.Topic != tt.wantTopic {
				t.Errorf("%s: Topic = %v, want %v (should restore from PartialInfo)", tt.description, intent.Topic, tt.wantTopic)
			}
			
			if intent.Entities["specialty"] != tt.wantSpecialty {
				t.Errorf("%s: specialty = %v, want %v", tt.description, intent.Entities["specialty"], tt.wantSpecialty)
			}
			
			if intent.Question != tt.wantQuestion {
				t.Errorf("%s: Question = %v, want %v", tt.description, intent.Question, tt.wantQuestion)
			}
			
			// Проверяем, что ожидание очищено
			if context.PendingQuestion != "" {
				t.Errorf("%s: PendingQuestion should be cleared after resolution", tt.description)
			}
			if context.ExpectedParameter != "" {
				t.Errorf("%s: ExpectedParameter should be cleared after resolution", tt.description)
			}
		})
	}
}

// TestContinuationAfterClarification проверяет продолжение диалога после уточнения
func TestContinuationAfterClarification(t *testing.T) {
	tests := []struct {
		name          string
		previousTopic string
		previousSpec  string
		question      string
		wantTopic     string
		wantSpecialty string
		description   string
	}{
		{
			name:          "continue_with_payment",
			previousTopic: "admission",
			previousSpec:  "дизайн",
			question:      "А стоимость?",
			wantTopic:     "payment",
			wantSpecialty: "дизайн",
			description:   "'А стоимость?' наследует специальность из контекста",
		},
		{
			name:          "continue_with_budget",
			previousTopic: "payment",
			previousSpec:  "юриспруденция",
			question:      "А есть бюджет?",
			wantTopic:     "admission",
			wantSpecialty: "юриспруденция",
			description:   "'А есть бюджет?' наследует специальность",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			context := &DialogContext{
				History: []DialogTurn{
					{
						UserMessage: "Предыдущий вопрос",
						Intent: Intent{
							Topic: tt.previousTopic,
							Entities: map[string]string{
								"specialty": tt.previousSpec,
							},
						},
					},
				},
				CurrentTopic: tt.previousTopic,
				LastEntities: map[string]string{
					"specialty": tt.previousSpec,
				},
			}
			
			intent := UnderstandIntent(tt.question, context)
			
			if intent.Topic != tt.wantTopic {
				t.Errorf("%s: Topic = %v, want %v", tt.description, intent.Topic, tt.wantTopic)
			}
			
			if intent.Entities["specialty"] != tt.wantSpecialty {
				t.Errorf("%s: specialty = %v, want %v (should inherit from context)", tt.description, intent.Entities["specialty"], tt.wantSpecialty)
			}
		})
	}
}

// TestTopicChangeDuringClarification проверяет смену темы во время ожидания уточнения
func TestTopicChangeDuringClarification(t *testing.T) {
	tests := []struct {
		name               string
		pendingQuestion    string
		pendingTopic       string
		newQuestion        string
		wantType           string
		wantPendingCleared bool
		description        string
	}{
		{
			name:               "explicit_topic_change",
			pendingQuestion:    "Какая стоимость?",
			pendingTopic:       "payment",
			newQuestion:        "Другой вопрос: расскажите про практику",
			wantType:           "topic_change",
			wantPendingCleared: true,
			description:        "Явная смена темы очищает ожидание уточнения",
		},
		{
			name:               "new_question_during_pending",
			pendingQuestion:    "Какая стоимость?",
			pendingTopic:       "payment",
			newQuestion:        "Где находится колледж?",
			wantType:           "question",
			wantPendingCleared: true,
			description:        "Новый вопрос во время ожидания отменяет уточнение",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			context := &DialogContext{
				History:           []DialogTurn{},
				CurrentTopic:      tt.pendingTopic,
				LastEntities:      make(map[string]string),
				PendingQuestion:   tt.pendingQuestion,
				ExpectedParameter: "specialty",
				PartialInfo: map[string]string{
					"topic": tt.pendingTopic,
				},
			}
			
			intent := UnderstandIntent(tt.newQuestion, context)
			
			if intent.Type != tt.wantType {
				t.Errorf("%s: Type = %v, want %v", tt.description, intent.Type, tt.wantType)
			}
			
			if tt.wantPendingCleared {
				if context.PendingQuestion != "" {
					t.Errorf("%s: PendingQuestion should be cleared, got '%s'", tt.description, context.PendingQuestion)
				}
				if context.ExpectedParameter != "" {
					t.Errorf("%s: ExpectedParameter should be cleared, got '%s'", tt.description, context.ExpectedParameter)
				}
				if context.PartialInfo != nil {
					t.Errorf("%s: PartialInfo should be cleared, got %v", tt.description, context.PartialInfo)
				}
			}
		})
	}
}

// TestTopicPreservationAfterClarification проверяет, что тема не перезаписывается после уточнения
func TestTopicPreservationAfterClarification(t *testing.T) {
	context := &DialogContext{
		History:      []DialogTurn{},
		CurrentTopic: "",
		LastEntities: make(map[string]string),
	}
	
	// Шаг 1: Задаем вопрос о стоимости
	intent1 := UnderstandIntent("Какая стоимость обучения?", context)
	if !intent1.IsAmbiguous {
		t.Error("First question should be ambiguous")
	}
	if intent1.Topic != "payment" {
		t.Errorf("Topic should be 'payment', got '%s'", intent1.Topic)
	}
	
	// Симулируем сохранение состояния (как это делает answer.go)
	context.PendingQuestion = "Какая стоимость обучения?"
	context.ExpectedParameter = "specialty"
	context.PartialInfo = map[string]string{
		"topic": intent1.Topic,
	}
	
	// Шаг 2: Отвечаем на уточнение
	intent2 := UnderstandIntent("Дизайн", context)
	if intent2.Type != "clarification" {
		t.Errorf("Should be clarification, got '%s'", intent2.Type)
	}
	if intent2.Topic != "payment" {
		t.Errorf("Topic should remain 'payment' after clarification, got '%s'", intent2.Topic)
	}
	if intent2.Entities["specialty"] != "дизайн" {
		t.Errorf("Specialty should be 'дизайн', got '%s'", intent2.Entities["specialty"])
	}
	
	// Проверяем, что ожидание очищено
	if context.PendingQuestion != "" {
		t.Error("PendingQuestion should be cleared after clarification")
	}
}

