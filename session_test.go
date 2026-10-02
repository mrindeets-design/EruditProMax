package main

import (
	"sync"
	"testing"
	"time"
)

// TestSessionValidation проверяет валидацию session_id
func TestSessionValidation(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		valid bool
	}{
		{"Empty ID", "", false},
		{"Short ID", "a", false},
		{"Invalid hex", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", false},
		{"Valid 32-char hex", "0123456789abcdef0123456789abcdef", true},
		{"Too long", "0123456789abcdef0123456789abcdef00", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidSessionID(tt.id)
			if result != tt.valid {
				t.Errorf("isValidSessionID(%q) = %v, want %v", tt.id, result, tt.valid)
			}
		})
	}
}

// TestSessionCreation проверяет создание новых сессий
func TestSessionCreation(t *testing.T) {
	sm := NewSessionManager()

	// Тест 1: Пустой ID должен создать новую сессию
	sid1, ctx1 := sm.GetOrCreate("")
	if sid1 == "" {
		t.Error("Expected non-empty session ID")
	}
	if !isValidSessionID(sid1) {
		t.Errorf("Generated session ID %q is invalid", sid1)
	}
	if len(ctx1.History) != 0 {
		t.Error("New session should have empty context")
	}

	// Тест 2: Короткий ID должен создать новую сессию
	sid2, _ := sm.GetOrCreate("a")
	if sid2 == "" || sid2 == "a" {
		t.Errorf("Short ID should be replaced, got %q", sid2)
	}

	// Тест 3: Неизвестный валидный ID должен создать новую сессию с серверным ID
	sid3, _ := sm.GetOrCreate("0123456789abcdef0123456789abcdef")
	if sid3 == "" {
		t.Error("Should create new session for unknown ID")
	}
}

// TestSessionContinuation проверяет продолжение существующей сессии
func TestSessionContinuation(t *testing.T) {
	sm := NewSessionManager()

	// Создаем сессию
	sid1, ctx1 := sm.GetOrCreate("")
	
	// Модифицируем контекст
	ctx1.CurrentTopic = "admission"
	ctx1.PendingQuestion = "test question"
	sm.UpdateContext(sid1, ctx1)

	// Получаем ту же сессию
	sid2, ctx2 := sm.GetOrCreate(sid1)
	
	if sid2 != sid1 {
		t.Errorf("Session ID changed: %q -> %q", sid1, sid2)
	}
	if ctx2.CurrentTopic != "admission" {
		t.Errorf("Context not preserved: got topic %q", ctx2.CurrentTopic)
	}
	if ctx2.PendingQuestion != "test question" {
		t.Errorf("Context not preserved: got pending %q", ctx2.PendingQuestion)
	}
}

// TestConcurrentSessionAccess проверяет параллельный доступ к одной сессии
func TestConcurrentSessionAccess(t *testing.T) {
	sm := NewSessionManager()

	// Создаем сессию
	sid, ctx := sm.GetOrCreate("")
	ctx.CurrentTopic = "initial"
	sm.UpdateContext(sid, ctx)

	// Параллельные запросы к одной сессии
	var wg sync.WaitGroup
	iterations := 10
	wg.Add(iterations)

	for i := 0; i < iterations; i++ {
		go func(n int) {
			defer wg.Done()
			
			// Получаем контекст
			_, localCtx := sm.GetOrCreate(sid)
			
			// Модифицируем локальную копию
			localCtx.History = append(localCtx.History, DialogTurn{
				UserMessage: "question",
				BotReply:    "answer",
			})
			
			// Сохраняем
			sm.UpdateContext(sid, localCtx)
		}(i)
	}

	wg.Wait()

	// Проверяем финальное состояние
	_, finalCtx := sm.GetOrCreate(sid)
	if len(finalCtx.History) == 0 {
		t.Error("Expected history to be updated")
	}
	
	// Сессия не должна быть повреждена
	if finalCtx.CurrentTopic != "initial" {
		t.Errorf("Original topic changed to %q", finalCtx.CurrentTopic)
	}
}


// TestIndependentSessions проверяет независимость разных сессий
func TestIndependentSessions(t *testing.T) {
	sm := NewSessionManager()

	// Создаем две сессии
	sid1, ctx1 := sm.GetOrCreate("")
	sid2, ctx2 := sm.GetOrCreate("")

	if sid1 == sid2 {
		t.Error("Different calls should create different sessions")
	}

	// Модифицируем первую сессию
	ctx1.CurrentTopic = "session1"
	sm.UpdateContext(sid1, ctx1)

	// Модифицируем вторую сессию
	ctx2.CurrentTopic = "session2"
	sm.UpdateContext(sid2, ctx2)

	// Проверяем изоляцию
	_, check1 := sm.GetOrCreate(sid1)
	_, check2 := sm.GetOrCreate(sid2)

	if check1.CurrentTopic != "session1" {
		t.Errorf("Session 1 topic changed to %q", check1.CurrentTopic)
	}
	if check2.CurrentTopic != "session2" {
		t.Errorf("Session 2 topic changed to %q", check2.CurrentTopic)
	}
}

// TestDeepCopy проверяет глубокое копирование DialogContext
func TestDeepCopy(t *testing.T) {
	sm := NewSessionManager()

	// Создаем сессию с богатым контекстом
	sid, ctx := sm.GetOrCreate("")
	
	ctx.History = []DialogTurn{
		{
			UserMessage: "Q1",
			BotReply:    "A1",
			Sources:     []Source{{Name: "S1", URL: "url1"}},
		},
	}
	ctx.LastEntities = map[string]string{"key": "value"}
	ctx.PartialInfo = map[string]string{"info": "data"}
	
	sm.UpdateContext(sid, ctx)

	// Получаем копию
	_, copy1 := sm.GetOrCreate(sid)
	
	// Модифицируем копию
	copy1.History[0].UserMessage = "Modified"
	copy1.LastEntities["key"] = "changed"
	copy1.PartialInfo["info"] = "changed"

	// Получаем новую копию и проверяем, что оригинал не изменился
	_, copy2 := sm.GetOrCreate(sid)
	
	if copy2.History[0].UserMessage != "Q1" {
		t.Error("Original history was modified")
	}
	if copy2.LastEntities["key"] != "value" {
		t.Error("Original LastEntities was modified")
	}
	if copy2.PartialInfo["info"] != "data" {
		t.Error("Original PartialInfo was modified")
	}
}

// TestSessionCleanup проверяет очистку старых сессий
func TestSessionCleanup(t *testing.T) {
	sm := &SessionManager{
		sessions: make(map[string]*UserSession),
		timeout:  100 * time.Millisecond,
	}

	// Создаем сессию
	sid, _ := sm.createNewSession()
	
	if sm.GetSessionCount() != 1 {
		t.Errorf("Expected 1 session, got %d", sm.GetSessionCount())
	}

	// Ждем истечения таймаута
	time.Sleep(150 * time.Millisecond)

	// Запускаем cleanup
	sm.cleanup()

	if sm.GetSessionCount() != 0 {
		t.Errorf("Expected 0 sessions after cleanup, got %d", sm.GetSessionCount())
	}

	// Попытка получить удаленную сессию должна создать новую
	sid2, _ := sm.GetOrCreate(sid)
	if sid2 == sid {
		t.Error("Old session should not be reused")
	}
}

// TestGenerateSessionID проверяет генерацию уникальных ID
func TestGenerateSessionID(t *testing.T) {
	ids := make(map[string]bool)
	iterations := 1000

	for i := 0; i < iterations; i++ {
		id := generateSessionID()
		
		if !isValidSessionID(id) {
			t.Errorf("Generated invalid ID: %q", id)
		}
		
		if ids[id] {
			t.Errorf("Duplicate ID generated: %q", id)
		}
		
		ids[id] = true
	}

	if len(ids) != iterations {
		t.Errorf("Expected %d unique IDs, got %d", iterations, len(ids))
	}
}
