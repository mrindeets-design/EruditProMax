package main

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// SessionManager управляет пользовательскими сессиями
type SessionManager struct {
	sessions map[string]*UserSession
	mu       sync.RWMutex
	timeout  time.Duration
}

// UserSession представляет сессию пользователя
type UserSession struct {
	ID            string
	DialogContext DialogContext
	CreatedAt     time.Time
	LastAccessAt  time.Time
	mu            sync.Mutex // Защита DialogContext от параллельных изменений
}

// NewSessionManager создает менеджер сессий
func NewSessionManager() *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*UserSession),
		timeout:  30 * time.Minute,
	}
	
	// Запускаем очистку старых сессий
	go sm.cleanupLoop()
	
	return sm
}

// GetOrCreate получает существующую или создает новую сессию
// Возвращает копию DialogContext для безопасной работы
func (sm *SessionManager) GetOrCreate(clientSessionID string) (sessionID string, context DialogContext) {
	// Проверяем валидность клиентского ID
	if !isValidSessionID(clientSessionID) {
		// Генерируем новый серверный ID
		return sm.createNewSession()
	}
	
	// Сначала пробуем получить существующую сессию
	sm.mu.RLock()
	session, exists := sm.sessions[clientSessionID]
	sm.mu.RUnlock()
	
	if exists {
		// Обновляем время доступа и получаем копию контекста
		session.mu.Lock()
		session.LastAccessAt = time.Now()
		contextCopy := deepCopyDialogContext(session.DialogContext)
		session.mu.Unlock()
		
		return session.ID, contextCopy
	}
	
	// Сессия не найдена - создаем новую с серверным ID
	return sm.createNewSession()
}

// UpdateContext обновляет контекст диалога в сессии
func (sm *SessionManager) UpdateContext(sessionID string, context DialogContext) {
	sm.mu.RLock()
	session, exists := sm.sessions[sessionID]
	sm.mu.RUnlock()
	
	if !exists {
		return
	}
	
	session.mu.Lock()
	session.DialogContext = context
	session.LastAccessAt = time.Now()
	session.mu.Unlock()
}

// createNewSession создает новую сессию с уникальным ID
func (sm *SessionManager) createNewSession() (string, DialogContext) {
	sessionID := generateSessionID()
	
	session := &UserSession{
		ID:            sessionID,
		DialogContext: DialogContext{},
		CreatedAt:     time.Now(),
		LastAccessAt:  time.Now(),
	}
	
	sm.mu.Lock()
	sm.sessions[sessionID] = session
	sm.mu.Unlock()
	
	return sessionID, DialogContext{}
}

// cleanupLoop периодически удаляет устаревшие сессии
func (sm *SessionManager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		sm.cleanup()
	}
}

// cleanup удаляет устаревшие сессии
func (sm *SessionManager) cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	now := time.Now()
	for id, session := range sm.sessions {
		// Проверяем время доступа без блокировки сессии
		// (чтение time.Time атомарно)
		if now.Sub(session.LastAccessAt) > sm.timeout {
			delete(sm.sessions, id)
		}
	}
}

// isValidSessionID проверяет валидность session ID
func isValidSessionID(id string) bool {
	if id == "" {
		return false
	}
	// ID должен быть hex-строкой длиной 32 символа (16 байт)
	if len(id) != 32 {
		return false
	}
	// Проверяем, что это валидный hex
	_, err := hex.DecodeString(id)
	return err == nil
}

// generateSessionID генерирует криптографически безопасный ID сессии
func generateSessionID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Критическая ошибка - не должна произойти
		panic("failed to generate session ID: " + err.Error())
	}
	return hex.EncodeToString(bytes)
}

// deepCopyDialogContext создает глубокую копию DialogContext
func deepCopyDialogContext(dc DialogContext) DialogContext {
	copy := DialogContext{
		CurrentTopic:      dc.CurrentTopic,
		TopicChanged:      dc.TopicChanged,
		PendingQuestion:   dc.PendingQuestion,
		ExpectedParameter: dc.ExpectedParameter,
	}
	
	// Копируем History
	if dc.History != nil {
		copy.History = make([]DialogTurn, len(dc.History))
		for i, turn := range dc.History {
			copy.History[i] = DialogTurn{
				UserMessage: turn.UserMessage,
				BotReply:    turn.BotReply,
				Timestamp:   turn.Timestamp,
			}
			// Копируем Intent
			copy.History[i].Intent = Intent{
				Type:        turn.Intent.Type,
				Topic:       turn.Intent.Topic,
				Question:    turn.Intent.Question,
				IsAmbiguous: turn.Intent.IsAmbiguous,
				Confidence:  turn.Intent.Confidence,
			}
			if turn.Intent.Entities != nil {
				copy.History[i].Intent.Entities = make(map[string]string)
				for k, v := range turn.Intent.Entities {
					copy.History[i].Intent.Entities[k] = v
				}
			}
			if turn.Intent.Conditions != nil {
				copy.History[i].Intent.Conditions = make([]string, len(turn.Intent.Conditions))
				copyStringSlice(copy.History[i].Intent.Conditions, turn.Intent.Conditions)
			}
			if turn.Intent.Anaphora != nil {
				copy.History[i].Intent.Anaphora = make([]string, len(turn.Intent.Anaphora))
				copyStringSlice(copy.History[i].Intent.Anaphora, turn.Intent.Anaphora)
			}
			// Копируем Sources
			if turn.Sources != nil {
				copy.History[i].Sources = make([]Source, len(turn.Sources))
				for j, src := range turn.Sources {
					copy.History[i].Sources[j] = Source{
						Name: src.Name,
						URL:  src.URL,
					}
				}
			}
		}
	}
	
	// Копируем LastEntities
	if dc.LastEntities != nil {
		copy.LastEntities = make(map[string]string)
		for k, v := range dc.LastEntities {
			copy.LastEntities[k] = v
		}
	}
	
	// Копируем LastSources
	if dc.LastSources != nil {
		copy.LastSources = make([]Source, len(dc.LastSources))
		for i, src := range dc.LastSources {
			copy.LastSources[i] = Source{
				Name: src.Name,
				URL:  src.URL,
			}
		}
	}
	
	// Копируем PartialInfo
	if dc.PartialInfo != nil {
		copy.PartialInfo = make(map[string]string)
		for k, v := range dc.PartialInfo {
			copy.PartialInfo[k] = v
		}
	}
	
	return copy
}

// copyStringSlice копирует slice строк
func copyStringSlice(dst, src []string) {
	for i, v := range src {
		dst[i] = v
	}
}

// GetSessionCount возвращает количество активных сессий
func (sm *SessionManager) GetSessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}
