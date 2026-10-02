package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const promptVersion = "v3"
const cacheMaxAge = 7 * 24 * time.Hour

type CacheKey struct {
	Question      string            `json:"question"`
	ContextDialog []string          `json:"context_dialog,omitempty"`
	Entities      map[string]string `json:"entities,omitempty"`
	ResolvedDate  string            `json:"resolved_date,omitempty"`
	Model         string            `json:"model"`
	PromptVersion string            `json:"prompt_version"`
	IndexVersion  int64             `json:"index_version"`
}

func (k *CacheKey) Hash() string {
	data, _ := json.Marshal(k)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

type CacheEntry struct {
	ID            int64
	Answer        string
	Sources       []Source
	FragmentIDs   []int64
	CreatedAt     time.Time
	InvalidatedAt *time.Time
	HitCount      int
}

type CacheManager struct {
	db         *sql.DB
	inflightMu sync.Mutex
	inflight   map[string]*inflightRequest
}

type inflightRequest struct {
	done        chan struct{}
	answer      string
	sources     []Source
	fragmentIDs []int64
	err         error
	ctx         context.Context
	cancel      context.CancelFunc
}

func NewCacheManager(db *sql.DB) *CacheManager {
	return &CacheManager{
		db:       db,
		inflight: make(map[string]*inflightRequest),
	}
}

func NormalizeQuestion(q string) string {
	q = strings.TrimSpace(q)
	q = strings.ToLower(q)
	q = strings.ReplaceAll(q, "ё", "е")
	q = strings.Join(strings.Fields(q), " ")
	return q
}

func BuildCacheKey(question, model string, context []string, entities map[string]string, indexVersion int64) CacheKey {
	return CacheKey{
		Question:      NormalizeQuestion(question),
		ContextDialog: context,
		Entities:      entities,
		Model:         model,
		PromptVersion: promptVersion,
		IndexVersion:  indexVersion,
	}
}

func GetIndexVersion(db *sql.DB) int64 {
	var version int64
	err := db.QueryRow(`SELECT COALESCE(MAX(version_number), 0) FROM fragments`).Scan(&version)
	if err != nil {
		log.Printf("GetIndexVersion error: %v", err)
		return 0
	}
	return version
}

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
	var fragmentIDsJSON string
	var createdAtStr string

	err = cm.db.QueryRow(`
		SELECT id, answer, sources_json, fragment_ids_json, created_at, hit_count
		FROM cache_entries
		WHERE key_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, keyID).Scan(&entry.ID, &entry.Answer, &sourcesJSON, &fragmentIDsJSON, &createdAtStr, &entry.HitCount)
	
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	entry.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAtStr)
	if err != nil {
		entry.CreatedAt, err = time.Parse("2006-01-02T15:04:05Z", createdAtStr)
		if err != nil {
			log.Printf("Cache: failed to parse created_at: %v", err)
			entry.CreatedAt = time.Now()
		}
	}

	if time.Since(entry.CreatedAt) > cacheMaxAge {
		log.Printf("Cache: expired entry %d (age: %v)", entry.ID, time.Since(entry.CreatedAt))
		return nil, nil
	}

	if err := json.Unmarshal([]byte(sourcesJSON), &entry.Sources); err != nil {
		log.Printf("Cache: failed to decode sources: %v", err)
		return nil, err
	}
	
	if err := json.Unmarshal([]byte(fragmentIDsJSON), &entry.FragmentIDs); err != nil {
		log.Printf("Cache: failed to decode fragmentIDs: %v", err)
		return nil, err
	}

	valid, actualFragmentIDs, err := cm.checkDependencies(entry.ID, entry.FragmentIDs)
	if err != nil {
		log.Printf("Cache: dependency check error: %v", err)
		return nil, err
	}
	
	if !valid {
		log.Printf("Cache: invalidated entry %d (dependencies changed)", entry.ID)
		return nil, nil
	}

	entry.FragmentIDs = actualFragmentIDs

	_, err = cm.db.Exec(`
		UPDATE cache_entries 
		SET last_accessed_at = datetime('now'), hit_count = hit_count + 1 
		WHERE id = ?
	`, entry.ID)
	if err != nil {
		log.Printf("Cache: failed to update hit stats: %v", err)
	}

	log.Printf("Cache hit: entry %d, %d hits, %d fragments", entry.ID, entry.HitCount+1, len(entry.FragmentIDs))
	return &entry, nil
}

func (cm *CacheManager) checkDependencies(entryID int64, fragmentIDs []int64) (bool, []int64, error) {
	if len(fragmentIDs) == 0 {
		log.Printf("Cache: entry %d has no dependencies - treating as stale", entryID)
		return false, nil, nil
	}

	rows, err := cm.db.Query(`
		SELECT fragment_id, recorded_version 
		FROM cache_dependencies 
		WHERE cache_entry_id = ?
	`, entryID)
	if err != nil {
		return false, nil, err
	}
	defer rows.Close()

	recordedVersions := make(map[int64]int64)
	for rows.Next() {
		var fragID, ver int64
		if err := rows.Scan(&fragID, &ver); err != nil {
			return false, nil, err
		}
		recordedVersions[fragID] = ver
	}

	if len(recordedVersions) == 0 {
		log.Printf("Cache: entry %d has no recorded dependencies", entryID)
		return false, nil, nil
	}

	placeholders := strings.Repeat("?,", len(fragmentIDs))
	placeholders = placeholders[:len(placeholders)-1]
	query := fmt.Sprintf(`SELECT id, version_number FROM fragments WHERE id IN (%s)`, placeholders)
	
	args := make([]interface{}, len(fragmentIDs))
	for i, id := range fragmentIDs {
		args[i] = id
	}
	
	rows, err = cm.db.Query(query, args...)
	if err != nil {
		return false, nil, err
	}
	defer rows.Close()

	currentVersions := make(map[int64]int64)
	for rows.Next() {
		var fragID, ver int64
		if err := rows.Scan(&fragID, &ver); err != nil {
			return false, nil, err
		}
		currentVersions[fragID] = ver
	}

	for _, fragID := range fragmentIDs {
		currentVer, exists := currentVersions[fragID]
		if !exists {
			log.Printf("Cache: fragment %d no longer exists", fragID)
			return false, nil, nil
		}
		
		recordedVer, recorded := recordedVersions[fragID]
		if !recorded {
			log.Printf("Cache: fragment %d not recorded in dependencies", fragID)
			return false, nil, nil
		}
		
		if currentVer != recordedVer {
			log.Printf("Cache: fragment %d version changed: %d -> %d", fragID, recordedVer, currentVer)
			return false, nil, nil
		}
	}

	return true, fragmentIDs, nil
}

func (cm *CacheManager) Put(key CacheKey, answer string, sources []Source, fragmentIDs []int64) error {
	if answer == "" || strings.Contains(answer, "К сожалению") || strings.Contains(answer, "не удалось") || strings.Contains(answer, "ошибка") {
		log.Printf("Cache: skipping error/incomplete answer")
		return nil
	}

	keyHash := key.Hash()
	tx, err := cm.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var keyID int64
	err = tx.QueryRow(`SELECT id FROM cache_keys WHERE key_hash = ?`, keyHash).Scan(&keyID)
	if err == sql.ErrNoRows {
		contextJSON, _ := json.Marshal(key.ContextDialog)
		entitiesJSON, _ := json.Marshal(key.Entities)
		if entitiesJSON == nil {
			entitiesJSON = []byte("{}")
		}
		
		res, err := tx.Exec(`INSERT INTO cache_keys (key_hash, question, context_dialog, entities, model, prompt_version, index_version) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			keyHash, key.Question, string(contextJSON), string(entitiesJSON), key.Model, key.PromptVersion, key.IndexVersion)
		if err != nil {
			return err
		}
		keyID, _ = res.LastInsertId()
	} else if err != nil {
		return err
	}

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
	fragmentIDsJSON, _ := json.Marshal(fragmentIDs)
	
	res, err := tx.Exec(`INSERT INTO cache_entries (key_id, answer, sources_json, fragment_ids_json) VALUES (?, ?, ?, ?)`, 
		keyID, answer, string(sourcesJSON), string(fragmentIDsJSON))
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
		log.Printf("Cache: saved entry %d with %d fragment dependencies", entryID, len(fragmentVersions))
	} else {
		log.Printf("Cache: WARNING - saved entry %d without fragment dependencies", entryID)
	}

	return tx.Commit()
}

func (cm *CacheManager) Dedup(ctx context.Context, keyHash string, fn func() (string, []Source, []int64, error)) (string, []Source, []int64, error) {
	cm.inflightMu.Lock()
	
	if req, exists := cm.inflight[keyHash]; exists {
		cm.inflightMu.Unlock()
		log.Printf("Cache dedup: waiting for %s", keyHash[:16])
		
		select {
		case <-req.done:
			return req.answer, req.sources, req.fragmentIDs, req.err
		case <-ctx.Done():
			log.Printf("Cache dedup: context cancelled while waiting for %s", keyHash[:16])
			return "", nil, nil, ctx.Err()
		}
	}

	reqCtx, cancel := context.WithCancel(ctx)
	req := &inflightRequest{
		done:   make(chan struct{}),
		ctx:    reqCtx,
		cancel: cancel,
	}
	cm.inflight[keyHash] = req
	cm.inflightMu.Unlock()

	answer, sources, fragmentIDs, err := fn()
	
	req.answer = answer
	req.sources = sources
	req.fragmentIDs = fragmentIDs
	req.err = err
	close(req.done)

	cm.inflightMu.Lock()
	delete(cm.inflight, keyHash)
	cm.inflightMu.Unlock()

	return answer, sources, fragmentIDs, err
}
