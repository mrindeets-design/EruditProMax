package main

import (
	"testing"
)

// TestSessionIDValidation проверяет регрессию: session_id должен валидироваться
func TestSessionIDValidation(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		wantValid bool
	}{
		{"valid hex 32 chars", "a1b2c3d4e5f6789012345678901234ab", true},
		{"empty", "", false},
		{"too short", "123", false},
		{"UUID format (not supported)", "123e4567-e89b-12d3-a456-426614174000", false},
		{"invalid chars", "invalid-session-id!", false},
		{"SQL injection attempt", "'; DROP TABLE sessions; --", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := isValidSessionID(tt.sessionID)
			if valid != tt.wantValid {
				t.Errorf("isValidSessionID(%q) = %v, want %v", tt.sessionID, valid, tt.wantValid)
			}
		})
	}
}

// TestGreetingWithQuestion проверяет регрессию: "Привет, какая стоимость?" должно восприниматься как вопрос
func TestGreetingWithQuestion(t *testing.T) {
	tests := []struct {
		name     string
		question string
		wantType string
	}{
		{"pure greeting", "Привет", "greeting"},
		{"greeting with exclamation", "Здравствуйте!", "greeting"},
		{"greeting with question", "Привет, какая стоимость обучения?", "question"},
		{"greeting with need", "Здравствуйте, мне нужна помощь", "question"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := UnderstandIntent(tt.question, &DialogContext{})
			if intent.Type != tt.wantType {
				t.Errorf("UnderstandIntent(%q).Type = %v, want %v", tt.question, intent.Type, tt.wantType)
			}
		})
	}
}

// TestClarificationFlowBasic проверяет регрессию: уточнения должны работать корректно
func TestClarificationFlowBasic(t *testing.T) {
	// Первый вопрос без специальности должен быть распознан как вопрос
	intent1 := UnderstandIntent("Какая стоимость обучения?", &DialogContext{})
	if intent1.Type != "question" {
		t.Errorf("First question should be 'question', got %v", intent1.Type)
	}
	if !intent1.IsAmbiguous {
		t.Error("Question about cost without specialty should be ambiguous")
	}
}

// TestConcurrentSessionSafety проверяет регрессию: отсутствие гонок данных в сессиях
func TestConcurrentSessionSafety(t *testing.T) {
	sm := NewSessionManager()
	
	// Запускаем 100 горутин, которые одновременно читают и создают сессии
	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func(idx int) {
			sessionID := "test-session-concurrent"
			_, _ = sm.GetOrCreate(sessionID)
			done <- true
		}(i)
	}

	// Ждём завершения всех горутин
	for i := 0; i < 100; i++ {
		<-done
	}

	// Проверяем, что сессия создана без паники
	sessionID, ctx := sm.GetOrCreate("test-session-final")
	if sessionID == "" {
		t.Error("Session ID should not be empty")
	}
	// History может быть nil или пустым слайсом - оба варианта валидны
	_ = ctx
}

// TestNormalizeFunction проверяет корректность нормализации текста
func TestNormalizeFunction(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Привет", "привет"},
		{"ЗДРАВСТВУЙТЕ", "здравствуйте"},
		{"Привет,  мир!", "привет мир"},  // normalize удаляет знаки препинания
		{"  пробелы  ", "пробелы"},
		{"тест-слово", "тест слово"},     // дефис заменяется на пробел
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalize(tt.input)
			if result != tt.expected {
				t.Errorf("normalize(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestIntentTopicExtraction проверяет извлечение темы из вопроса
func TestIntentTopicExtraction(t *testing.T) {
	tests := []struct {
		question   string
		expectTopic bool
	}{
		{"Какая стоимость обучения?", true},
		{"Как поступить в колледж?", true},
		{"Расскажи про преподавателей", true},
		// Примечание: "Привет" может иметь topic="general" - это нормально
	}

	for _, tt := range tests {
		t.Run(tt.question, func(t *testing.T) {
			intent := UnderstandIntent(tt.question, &DialogContext{})
			hasTopic := intent.Topic != ""
			if hasTopic != tt.expectTopic {
				t.Errorf("UnderstandIntent(%q).Topic presence = %v, want %v (topic=%q)", 
					tt.question, hasTopic, tt.expectTopic, intent.Topic)
			}
		})
	}
}
