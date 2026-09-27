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
func (sm *SessionManager) GetOrCreate(sessionID string) *UserSession {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	// Если sessionID пустой, создаем новую сессию
	if sessionID == "" {
		return sm.createNewSession()
	}
	
	// Проверяем существующую сессию
	if session, exists := sm.sessions[sessionID]; exists {
		session.LastAccessAt = time.Now()
		return session
	}
	
	// Создаем новую сессию с заданным ID
	session := &UserSession{
		ID:            sessionID,
		DialogContext: DialogContext{},
		CreatedAt:     time.Now(),
		LastAccessAt:  time.Now(),
	}
	sm.sessions[sessionID] = session
	
	return session
}

// createNewSession создает новую сессию с уникальным ID
func (sm *SessionManager) createNewSession() *UserSession {
	sessionID := generateSessionID()
	
	session := &UserSession{
		ID:            sessionID,
		DialogContext: DialogContext{},
		CreatedAt:     time.Now(),
		LastAccessAt:  time.Now(),
	}
	
	sm.sessions[sessionID] = session
	return session
}

// UpdateContext обновляет контекст диалога в сессии
func (sm *SessionManager) UpdateContext(sessionID string, context DialogContext) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	if session, exists := sm.sessions[sessionID]; exists {
		session.DialogContext = context
		session.LastAccessAt = time.Now()
	}
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
		if now.Sub(session.LastAccessAt) > sm.timeout {
			delete(sm.sessions, id)
		}
	}
}

// generateSessionID генерирует уникальный ID сессии
func generateSessionID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback на время
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(bytes)
}

// GetSessionCount возвращает количество активных сессий
func (sm *SessionManager) GetSessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}
