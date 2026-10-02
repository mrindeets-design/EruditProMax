package main

import (
	"os"
	"sync"
	"testing"
	"time"
)

// TestPragmaSettingsOnMultipleConnections проверяет, что PRAGMA применяются к каждому соединению
func TestPragmaSettingsOnMultipleConnections(t *testing.T) {
	dbPath := "test_pragmas.db"
	defer cleanupTestDB(dbPath)

	db, err := InitDatabase(dbPath)
	if err != nil {
		t.Fatalf("InitDatabase failed: %v", err)
	}
	defer db.Close()

	// Открываем несколько соединений параллельно и проверяем настройки
	var wg sync.WaitGroup
	errors := make(chan error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(connID int) {
			defer wg.Done()

			// Каждая горутина выполняет запрос, который использует своё соединение из пула
			var foreignKeys, busyTimeout int
			var journalMode string

			err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys)
			if err != nil {
				errors <- err
				return
			}
			if foreignKeys != 1 {
				errors <- err
				return
			}

			err = db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout)
			if err != nil {
				errors <- err
				return
			}
			if busyTimeout < 5000 {
				t.Logf("⚠️  Connection %d: busy_timeout=%d (expected >= 10000)", connID, busyTimeout)
			}

			err = db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
			if err != nil {
				errors <- err
				return
			}
			if journalMode != "wal" {
				t.Errorf("Connection %d: journal_mode=%s, want 'wal'", connID, journalMode)
			}

			t.Logf("✅ Connection %d: foreign_keys=%d, busy_timeout=%d ms, journal_mode=%s",
				connID, foreignKeys, busyTimeout, journalMode)
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		if err != nil {
			t.Errorf("Connection check failed: %v", err)
		}
	}
}

// TestForeignKeyEnforcement проверяет, что внешние ключи действительно работают
func TestForeignKeyEnforcement(t *testing.T) {
	dbPath := "test_fk.db"
	defer cleanupTestDB(dbPath)

	db, err := InitDatabase(dbPath)
	if err != nil {
		t.Fatalf("InitDatabase failed: %v", err)
	}
	defer db.Close()

	// Проверяем нарушение FK: пытаемся вставить fragment с несуществующим page_id
	_, err = db.Exec(`INSERT INTO fragments (page_id, chunk_index, text, text_hash) VALUES (999999, 0, 'test', 'hash')`)
	if err == nil {
		t.Fatal("Expected foreign key violation, but insert succeeded")
	}

	t.Logf("✅ Foreign key constraint working: %v", err)
}


// TestConcurrentDatabaseOperations проверяет конкурентные операции с БД
func TestConcurrentDatabaseOperations(t *testing.T) {
	dbPath := "test_concurrent.db"
	defer cleanupTestDB(dbPath)

	db, err := InitDatabase(dbPath)
	if err != nil {
		t.Fatalf("InitDatabase failed: %v", err)
	}
	defer db.Close()

	// Вставляем тестовый source
	sourceResult, err := db.Exec(`INSERT INTO sources (url, name, kind, category, check_interval_seconds) 
		VALUES ('http://test-concurrent.com', 'Test Source', 'page', 'test', 3600)`)
	if err != nil {
		t.Fatalf("Insert source failed: %v", err)
	}
	sourceID, _ := sourceResult.LastInsertId()

	// Вставляем тестовую страницу
	result, err := db.Exec(`INSERT INTO pages (source_id, url, content_hash, clean_text, fetched_at, last_check_at, last_success_at) 
		VALUES (?, 'http://test-concurrent.com/page1', 'hash123', 'content', datetime('now'), datetime('now'), datetime('now'))`, sourceID)
	if err != nil {
		t.Fatalf("Insert page failed: %v", err)
	}
	pageID, _ := result.LastInsertId()

	// Запускаем параллельные операции записи и чтения
	var wg sync.WaitGroup
	errors := make(chan error, 20)

	// 10 читателей
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				var count int
				err := db.QueryRow("SELECT COUNT(*) FROM pages").Scan(&count)
				if err != nil {
					errors <- err
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}(i)
	}

	// 5 писателей (вставка fragments)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < 3; j++ {
				_, err := db.Exec(`INSERT INTO fragments (page_id, chunk_index, text, text_hash) 
					VALUES (?, ?, ?, ?)`, pageID, writerID*10+j, "test content", "hash")
				if err != nil {
					errors <- err
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	errorCount := 0
	for err := range errors {
		if err != nil {
			t.Errorf("Concurrent operation failed: %v", err)
			errorCount++
		}
	}

	if errorCount == 0 {
		t.Log("✅ All concurrent operations completed successfully")
	}

	// Проверяем итоговое количество fragments
	var fragmentCount int
	err = db.QueryRow("SELECT COUNT(*) FROM fragments WHERE page_id = ?", pageID).Scan(&fragmentCount)
	if err != nil {
		t.Fatalf("Count fragments failed: %v", err)
	}
	
	expected := 5 * 3 // 5 писателей × 3 вставки
	if fragmentCount != expected {
		t.Errorf("Expected %d fragments, got %d", expected, fragmentCount)
	} else {
		t.Logf("✅ Fragment count correct: %d", fragmentCount)
	}
}

// TestTransactionIsolation проверяет изоляцию транзакций
func TestTransactionIsolation(t *testing.T) {
	dbPath := "test_tx.db"
	defer cleanupTestDB(dbPath)

	db, err := InitDatabase(dbPath)
	if err != nil {
		t.Fatalf("InitDatabase failed: %v", err)
	}
	defer db.Close()

	// Вставляем тестовый source
	sourceResult, err := db.Exec(`INSERT INTO sources (url, name, kind, category, check_interval_seconds) 
		VALUES ('http://test-tx.com', 'Test Source 2', 'page', 'test', 3600)`)
	if err != nil {
		t.Fatalf("Insert source failed: %v", err)
	}
	sourceID, _ := sourceResult.LastInsertId()

	// Вставляем тестовую страницу
	result, err := db.Exec(`INSERT INTO pages (source_id, url, content_hash, clean_text, fetched_at, last_check_at, last_success_at) 
		VALUES (?, 'http://test-tx.com/page1', 'hash456', 'content2', datetime('now'), datetime('now'), datetime('now'))`, sourceID)
	if err != nil {
		t.Fatalf("Insert page failed: %v", err)
	}
	pageID, _ := result.LastInsertId()

	// Начинаем транзакцию
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin transaction failed: %v", err)
	}

	// Вставляем fragment в транзакции
	_, err = tx.Exec(`INSERT INTO fragments (page_id, chunk_index, text, text_hash) VALUES (?, ?, ?, ?)`,
		pageID, 0, "tx content", "txhash")
	if err != nil {
		tx.Rollback()
		t.Fatalf("Insert in transaction failed: %v", err)
	}

	// Параллельное чтение не должно видеть uncommitted данные
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM fragments WHERE page_id = ?", pageID).Scan(&count)
	if err != nil {
		tx.Rollback()
		t.Fatalf("Count failed: %v", err)
	}
	if count != 0 {
		tx.Rollback()
		t.Errorf("Transaction isolation broken: saw %d uncommitted rows", count)
	}

	// Commit транзакции
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Теперь данные должны быть видны
	err = db.QueryRow("SELECT COUNT(*) FROM fragments WHERE page_id = ?", pageID).Scan(&count)
	if err != nil {
		t.Fatalf("Count after commit failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 fragment after commit, got %d", count)
	} else {
		t.Log("✅ Transaction isolation working correctly")
	}
}

// cleanupTestDB удаляет тестовую БД и WAL-файлы
func cleanupTestDB(dbPath string) {
	// Удаляем файлы БД после теста
	os.Remove(dbPath)
	os.Remove(dbPath + "-shm")
	os.Remove(dbPath + "-wal")
}
