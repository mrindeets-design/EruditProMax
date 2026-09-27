package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// =========================================================
// CACHE SYSTEM
// =========================================================

const promptVersion = "v2" // Increment when system prompt changes

// CacheKey представляет ключ кэша с контекстом
type CacheKey struct {
	Question      string   `json:"question"`
	ContextDialog []string `json:"context_dialog,omitempty"`
	Specialty     string   `json:"specialty,omitempty"`
	Group         string   `json:"group,omitempty"`
	AcademicYear  string   `json:"academic_year,omitempty"`
	ResolvedDate  string   `json:"resolved_date,omitempty"`
	Model         string   `json:"model"`
	PromptVersion string   `json:"prompt_version"`
}

// Hash возвращает SHA256 хеш канонического JSON представления ключа
func (k *CacheKey) Hash() string {
	data, _ := json.Marshal(k)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// CacheEntry представляет закэшированный ответ
type CacheEntry struct {
	ID            int64
	Answer        string
	Sources       []Source
	CreatedAt     time.Time
	InvalidatedAt *time.Time
	HitCount      int
}

// CacheManager управляет кэшем ответов
type CacheManager struct {
	db *sql.DB

	// In-flight deduplication
	inflightMu sync.Mutex
	inflight   map[string]*inflightRequest
}

type inflightRequest struct {
	done    chan struct{}
	answer  string
	sources []Source
	err     error
}

func NewCacheManager(db *sql.DB) *CacheManager {
	return &CacheManager{
		db:       db,
		inflight: make(map[string]*inflightRequest),
	}
}

// NormalizeQuestion нормализует вопрос для кэша
func NormalizeQuestion(q string) string {
	q = strings.TrimSpace(q)
	q = strings.ToLower(q)
	q = strings.ReplaceAll(q, "ё", "е")
	q = strings.Join(strings.Fields(q), " ")
	return q
}

// BuildCacheKey создаёт ключ кэша из вопроса и контекста
func BuildCacheKey(question, model string, context []string) CacheKey {
	return CacheKey{
		Question:      NormalizeQuestion(question),
		ContextDialog: context,
		Model:         model,
		PromptVersion: promptVersion,
	}
}



// Get пытается получить ответ из кэша
func (cm *CacheManager) Get(key CacheKey) (*CacheEntry, error) {
	keyHash := key.Hash()

	var keyID int64
	err := cm.db.QueryRow(`SELECT id FROM cache_keys WHERE key_hash = ?`, keyHash).Scan(&keyID)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var entry CacheEntry
	var sourcesJSON string
	var invalidatedAt sql.NullString

	err = cm.db.QueryRow(`
		SELECT id, answer, sources_json, created_at, invalidated_at, hit_count
		FROM cache_entries
		WHERE key_id = ? AND invalidated_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1
	`, keyID).Scan(&entry.ID, &entry.Answer, &sourcesJSON, &entry.CreatedAt, &invalidatedAt, &entry.HitCount)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Проверяем зависимости
	valid, err := cm.checkDependencies(entry.ID)
	if err != nil {
		return nil, err
	}
	if !valid {
		// Используем отдельную горутину для обновления статуса, чтобы не блокировать
		go cm.db.Exec(`UPDATE cache_entries SET invalidated_at = datetime('now') WHERE id = ?`, entry.ID)
		return nil, nil
	}

	if err := json.Unmarshal([]byte(sourcesJSON), &entry.Sources); err != nil {
		return nil, err
	}

	// Асинхронно обновляем метрики, чтобы не блокировать основной запрос
	go func() {
		cm.db.Exec(`UPDATE cache_entries SET hit_count = hit_count + 1, last_hit_at = datetime('now') WHERE id = ?`, entry.ID)
		RecordStat(cm.db, "cache_hit", 1)
	}()

	return &entry, nil
}

// checkDependencies проверяет актуальность зависимостей
func (cm *CacheManager) checkDependencies(entryID int64) (bool, error) {
	rows, err := cm.db.Query(`
		SELECT cd.fragment_id, cd.recorded_version, f.version_number
		FROM cache_dependencies cd
		JOIN fragments f ON f.id = cd.fragment_id
		WHERE cd.cache_entry_id = ?
	`, entryID)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var fragID, recordedVer, currentVer int64
		if err := rows.Scan(&fragID, &recordedVer, &currentVer); err != nil {
			return false, err
		}
		if recordedVer != currentVer {
			log.Printf("Cache stale: fragment %d v%d != v%d", fragID, currentVer, recordedVer)
			return false, nil
		}
	}

	return true, rows.Err()
}

// Put сохраняет ответ в кэш
func (cm *CacheManager) Put(key CacheKey, answer string, sources []Source, fragmentIDs []int64) error {
	keyHash := key.Hash()

	tx, err := cm.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var keyID int64
	err = tx.QueryRow(`SELECT id FROM cache_keys WHERE key_hash = ?`, keyHash).Scan(&keyID)
	if err == sql.ErrNoRows {
		res, err := tx.Exec(`
			INSERT INTO cache_keys (key_hash, question, model, prompt_version)
			VALUES (?, ?, ?, ?)
		`, keyHash, key.Question, key.Model, key.PromptVersion)
		if err != nil {
			return err
		}
		keyID, _ = res.LastInsertId()
	} else if err != nil {
		return err
	}

	// Проверяем версии фрагментов
	fragmentVersions := make(map[int64]int64)
	if len(fragmentIDs) > 0 {
		placeholders := strings.Repeat("?,", len(fragmentIDs))
		placeholders = placeholders[:len(placeholders)-1]

		query := fmt.Sprintf(`SELECT id, version_number FROM fragments WHERE id IN (%s)`, placeholders)
		args := make([]interface{}, len(fragmentIDs))
		for i, id := range fragmentIDs {
			args[i] = id
		}

		rows, err := tx.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var id, ver int64
			if err := rows.Scan(&id, &ver); err != nil {
				return err
			}
			fragmentVersions[id] = ver
		}
	}

	sourcesJSON, _ := json.Marshal(sources)

	res, err := tx.Exec(`INSERT INTO cache_entries (key_id, answer, sources_json) VALUES (?, ?, ?)`,
		keyID, answer, string(sourcesJSON))
	if err != nil {
		return err
	}

	entryID, _ := res.LastInsertId()

	if len(fragmentVersions) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO cache_dependencies (cache_entry_id, fragment_id, recorded_version) VALUES (?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for fragID, ver := range fragmentVersions {
			if _, err := stmt.Exec(entryID, fragID, ver); err != nil {
				return err
			}
		}
	}

	// Записываем статистику внутри транзакции
	if _, err := tx.Exec(`INSERT INTO stats (metric_name, metric_value) VALUES (?, ?)`, "cache_miss", 1.0); err != nil {
		return err
	}

	return tx.Commit()
}

// Dedup объединяет одновременные запросы
func (cm *CacheManager) Dedup(keyHash string, fn func() (string, []Source, error)) (string, []Source, error) {
	cm.inflightMu.Lock()

	if req, exists := cm.inflight[keyHash]; exists {
		cm.inflightMu.Unlock()
		log.Printf("Cache dedup: waiting for %s", keyHash[:16])
		<-req.done
		return req.answer, req.sources, req.err
	}

	req := &inflightRequest{done: make(chan struct{})}
	cm.inflight[keyHash] = req
	cm.inflightMu.Unlock()

	answer, sources, err := fn()

	req.answer = answer
	req.sources = sources
	req.err = err
	close(req.done)

	cm.inflightMu.Lock()
	delete(cm.inflight, keyHash)
	cm.inflightMu.Unlock()

	return answer, sources, err
}
