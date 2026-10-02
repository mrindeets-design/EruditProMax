package main

import (
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open test DB: %v", err)
	}
	
	// Применяем только необходимую схему для тестов кеша
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			kind TEXT DEFAULT 'html'
		);
		
		CREATE TABLE IF NOT EXISTS pages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id INTEGER NOT NULL,
			url TEXT NOT NULL UNIQUE,
			content_hash TEXT NOT NULL,
			clean_text TEXT,
			fetched_at TEXT NOT NULL,
			last_check_at TEXT NOT NULL,
			last_success_at TEXT,
			FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE
		);
		
		CREATE TABLE IF NOT EXISTS fragments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			page_id INTEGER NOT NULL,
			chunk_index INTEGER NOT NULL,
			text TEXT NOT NULL,
			text_hash TEXT NOT NULL,
			version_number INTEGER NOT NULL DEFAULT 1,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE,
			UNIQUE(page_id, chunk_index)
		);
		
		CREATE TABLE IF NOT EXISTS cache_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key_hash TEXT NOT NULL UNIQUE,
			question TEXT NOT NULL,
			context_dialog TEXT NOT NULL,
			entities TEXT NOT NULL,
			model TEXT NOT NULL,
			prompt_version INTEGER NOT NULL,
			index_version INTEGER NOT NULL
		);
		
		CREATE TABLE IF NOT EXISTS cache_entries (
			cache_key TEXT PRIMARY KEY,
			question TEXT NOT NULL,
			answer TEXT NOT NULL,
			sources_json TEXT NOT NULL,
			fragment_ids_json TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_accessed_at TEXT NOT NULL DEFAULT (datetime('now')),
			hit_count INTEGER NOT NULL DEFAULT 0
		);
		
		CREATE TABLE IF NOT EXISTS cache_dependencies (
			cache_key TEXT NOT NULL,
			fragment_id INTEGER NOT NULL,
			fragment_version INTEGER NOT NULL,
			PRIMARY KEY (cache_key, fragment_id),
			FOREIGN KEY (cache_key) REFERENCES cache_entries(cache_key) ON DELETE CASCADE
		);
		
		CREATE INDEX IF NOT EXISTS idx_cache_created ON cache_entries(created_at);
		CREATE INDEX IF NOT EXISTS idx_cache_accessed ON cache_entries(last_accessed_at);
		CREATE INDEX IF NOT EXISTS idx_fragments_version ON fragments(id, version_number);
		CREATE INDEX IF NOT EXISTS idx_cache_keys_hash ON cache_keys(key_hash);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}
	_, err = db.Exec(`INSERT INTO sources (url, name, kind) VALUES ('http://test.com', 'Test', 'html')`)
	if err != nil {
		t.Fatalf("Failed to insert test source: %v", err)
	}
	_, err = db.Exec(`INSERT INTO pages (source_id, url, content_hash, clean_text, fetched_at, last_check_at, last_success_at) VALUES (1, 'http://test.com/page1', 'hash1', 'Test page content', datetime('now'), datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("Failed to insert test page: %v", err)
	}
	_, err = db.Exec(`INSERT INTO fragments (page_id, chunk_index, text, text_hash, version_number) VALUES (1, 0, 'Test fragment 1', 'fhash1', 1), (1, 1, 'Test fragment 2', 'fhash2', 1)`)
	if err != nil {
		t.Fatalf("Failed to insert test fragments: %v", err)
	}
	return db
}

func TestCacheKeyGeneration(t *testing.T) {
	key1 := BuildCacheKey("Какая стоимость обучения?", "llama3.2:3b", []string{"prev"}, map[string]string{"specialty": "09.02.07"}, 1)
	key2 := BuildCacheKey("Какая стоимость обучения?", "llama3.2:3b", []string{"prev"}, map[string]string{"specialty": "09.02.07"}, 1)
	if key1.Hash() != key2.Hash() {
		t.Errorf("Same inputs should produce same hash")
	}
	key3 := BuildCacheKey("Другой вопрос", "llama3.2:3b", []string{"prev"}, map[string]string{"specialty": "09.02.07"}, 1)
	if key1.Hash() == key3.Hash() {
		t.Error("Different questions should produce different hashes")
	}
	key4 := BuildCacheKey("Какая стоимость обучения?", "llama3.2:3b", []string{"prev"}, map[string]string{"specialty": "11.02.02"}, 1)
	if key1.Hash() == key4.Hash() {
		t.Error("Different entities should produce different hashes")
	}
	key5 := BuildCacheKey("Какая стоимость обучения?", "llama3.2:3b", []string{"prev"}, map[string]string{"specialty": "09.02.07"}, 2)
	if key1.Hash() == key5.Hash() {
		t.Error("Different index versions should produce different hashes")
	}
}

func TestCacheReadWrite(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	cm := NewCacheManager(db)
	key := BuildCacheKey("Тестовый вопрос", "llama3.2:3b", nil, nil, 1)
	cached, err := cm.Get(key)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if cached != nil {
		t.Error("Expected cache miss, got hit")
	}
	answer := "Тестовый ответ"
	sources := []Source{{URL: "http://test.com", Name: "Test"}}
	fragmentIDs := []int64{1, 2}
	err = cm.Put(key, answer, sources, fragmentIDs)
	if err != nil {
		t.Fatalf("Put error: %v", err)
	}
	cached, err = cm.Get(key)
	if err != nil {
		t.Fatalf("Get error after put: %v", err)
	}
	if cached == nil {
		t.Fatal("Expected cache hit, got miss")
	}
	if cached.Answer != answer {
		t.Errorf("Answer mismatch: got %s, want %s", cached.Answer, answer)
	}
	if len(cached.Sources) != 1 {
		t.Errorf("Sources count mismatch: got %d, want 1", len(cached.Sources))
	}
	if len(cached.FragmentIDs) != 2 {
		t.Errorf("FragmentIDs count mismatch: got %d, want 2", len(cached.FragmentIDs))
	}
}

func TestCacheInvalidationOnFragmentChange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	cm := NewCacheManager(db)
	key := BuildCacheKey("Вопрос", "llama3.2:3b", nil, nil, 1)
	answer := "Ответ"
	sources := []Source{{URL: "http://test.com", Name: "Test"}}
	fragmentIDs := []int64{1}
	err := cm.Put(key, answer, sources, fragmentIDs)
	if err != nil {
		t.Fatalf("Put error: %v", err)
	}
	cached, _ := cm.Get(key)
	if cached == nil {
		t.Fatal("Expected cache hit")
	}
	_, err = db.Exec(`UPDATE fragments SET version_number = 2 WHERE id = 1`)
	if err != nil {
		t.Fatalf("Failed to update fragment version: %v", err)
	}
	cached, _ = cm.Get(key)
	if cached != nil {
		t.Error("Expected cache miss after fragment version change")
	}
}

func TestCacheInvalidationOnFragmentDelete(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	cm := NewCacheManager(db)
	key := BuildCacheKey("Вопрос", "llama3.2:3b", nil, nil, 1)
	answer := "Ответ"
	sources := []Source{{URL: "http://test.com", Name: "Test"}}
	fragmentIDs := []int64{1}
	err := cm.Put(key, answer, sources, fragmentIDs)
	if err != nil {
		t.Fatalf("Put error: %v", err)
	}
	_, err = db.Exec(`DELETE FROM fragments WHERE id = 1`)
	if err != nil {
		t.Fatalf("Failed to delete fragment: %v", err)
	}
	cached, _ := cm.Get(key)
	if cached != nil {
		t.Error("Expected cache miss after fragment deletion")
	}
}

func TestCacheExpiration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	cm := NewCacheManager(db)
	key := BuildCacheKey("Вопрос", "llama3.2:3b", nil, nil, 1)
	err := cm.Put(key, "Ответ", []Source{{URL: "http://test.com", Name: "Test"}}, []int64{1})
	if err != nil {
		t.Fatalf("Put error: %v", err)
	}
	_, err = db.Exec(`UPDATE cache_entries SET created_at = datetime('now', '-8 days')`)
	if err != nil {
		t.Fatalf("Failed to update created_at: %v", err)
	}
	cached, _ := cm.Get(key)
	if cached != nil {
		t.Error("Expected cache miss after expiration")
	}
}

func TestCacheSkipsErrors(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	cm := NewCacheManager(db)
	key := BuildCacheKey("Вопрос", "llama3.2:3b", nil, nil, 1)
	sources := []Source{{URL: "http://test.com", Name: "Test"}}
	fragmentIDs := []int64{1}
	err := cm.Put(key, "К сожалению, не удалось найти информацию", sources, fragmentIDs)
	if err != nil {
		t.Fatalf("Put should not error on skip: %v", err)
	}
	cached, _ := cm.Get(key)
	if cached != nil {
		t.Error("Error responses should not be cached")
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}
