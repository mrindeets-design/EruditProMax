package main

// Migration SQL для версии 1 (разбито на части из-за больших размеров)

const migrationV1Part1 = `
-- =========================================================
-- SOURCES (конфигурация обхода)
-- =========================================================
CREATE TABLE sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    url TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    category TEXT,
    check_interval_seconds INTEGER NOT NULL DEFAULT 86400,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_sources_enabled ON sources(enabled);
CREATE INDEX idx_sources_category ON sources(category);

-- =========================================================
-- PAGES (загруженные страницы)
-- =========================================================
CREATE TABLE pages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL,
    url TEXT NOT NULL UNIQUE,
    final_url TEXT,
    content_hash TEXT NOT NULL,
    raw_html TEXT,
    clean_text TEXT NOT NULL,
    title TEXT,
    published_date TEXT,
    specialty_code TEXT,
    specialty_name TEXT,
    group_name TEXT,
    academic_year TEXT,
    valid_from TEXT,
    valid_until TEXT,
    version_number INTEGER NOT NULL DEFAULT 1,
    fetched_at TEXT NOT NULL,
    last_check_at TEXT NOT NULL,
    last_success_at TEXT NOT NULL,
    last_error TEXT,
    http_etag TEXT,
    http_last_modified TEXT,
    FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE
);

CREATE INDEX idx_pages_source ON pages(source_id);
CREATE INDEX idx_pages_hash ON pages(content_hash);
CREATE INDEX idx_pages_category ON pages(specialty_code, academic_year);
CREATE INDEX idx_pages_check ON pages(last_check_at);

-- =========================================================
-- PAGE_VERSIONS (история изменений)
-- =========================================================
CREATE TABLE page_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    page_id INTEGER NOT NULL,
    version_number INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    clean_text TEXT NOT NULL,
    changed_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
);

CREATE INDEX idx_page_versions_page ON page_versions(page_id, version_number);

-- =========================================================
-- DOCUMENTS (PDF, DOC files)
-- =========================================================
CREATE TABLE documents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL,
    url TEXT NOT NULL UNIQUE,
    file_hash TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    file_path TEXT,
    file_size INTEGER NOT NULL,
    extracted_text TEXT,
    ocr_attempted INTEGER NOT NULL DEFAULT 0,
    ocr_successful INTEGER NOT NULL DEFAULT 0,
    title TEXT,
    published_date TEXT,
    specialty_code TEXT,
    academic_year TEXT,
    version_number INTEGER NOT NULL DEFAULT 1,
    fetched_at TEXT NOT NULL,
    last_check_at TEXT NOT NULL,
    FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE
);

CREATE INDEX idx_documents_source ON documents(source_id);
CREATE INDEX idx_documents_file_hash ON documents(file_hash);
CREATE INDEX idx_documents_content_hash ON documents(content_hash);
`


const migrationV1Part2 = `
-- =========================================================
-- FRAGMENTS (chunks для RAG)
-- =========================================================
CREATE TABLE fragments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    page_id INTEGER,
    document_id INTEGER,
    chunk_index INTEGER NOT NULL,
    text TEXT NOT NULL,
    text_hash TEXT NOT NULL,
    version_number INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE,
    FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE,
    CHECK ((page_id IS NOT NULL AND document_id IS NULL) OR (page_id IS NULL AND document_id IS NOT NULL))
);

CREATE INDEX idx_fragments_page ON fragments(page_id);
CREATE INDEX idx_fragments_document ON fragments(document_id);
CREATE INDEX idx_fragments_hash ON fragments(text_hash);

-- =========================================================
-- FRAGMENTS_FTS (полнотекстовый поиск)
-- =========================================================
CREATE VIRTUAL TABLE IF NOT EXISTS fragments_fts USING fts5(
    text,
    content='fragments',
    content_rowid='id',
    tokenize='unicode61 remove_diacritics 2'
);

-- Триггеры для автоматической синхронизации FTS5
CREATE TRIGGER fragments_ai AFTER INSERT ON fragments BEGIN
    INSERT INTO fragments_fts(rowid, text) VALUES (new.id, new.text);
END;

CREATE TRIGGER fragments_ad AFTER DELETE ON fragments BEGIN
    INSERT INTO fragments_fts(fragments_fts, rowid, text) VALUES ('delete', old.id, old.text);
END;

CREATE TRIGGER fragments_au AFTER UPDATE ON fragments BEGIN
    INSERT INTO fragments_fts(fragments_fts, rowid, text) VALUES ('delete', old.id, old.text);
    INSERT INTO fragments_fts(rowid, text) VALUES (new.id, new.text);
END;

-- =========================================================
-- EMBEDDINGS (векторные представления фрагментов)
-- =========================================================
CREATE TABLE embeddings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    fragment_id INTEGER NOT NULL,
    model_name TEXT NOT NULL,
    model_dimension INTEGER NOT NULL,
    embedding_blob BLOB NOT NULL,
    content_version INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (fragment_id) REFERENCES fragments(id) ON DELETE CASCADE,
    UNIQUE (fragment_id, model_name)
);

CREATE INDEX idx_embeddings_fragment ON embeddings(fragment_id);
CREATE INDEX idx_embeddings_model ON embeddings(model_name, content_version);

-- =========================================================
-- FRAGMENTS_FTS (полнотекстовый поиск)
-- =========================================================
CREATE VIRTUAL TABLE IF NOT EXISTS fragments_fts USING fts5(
    text,
    content='fragments',
    content_rowid='id',
    tokenize='unicode61 remove_diacritics 2'
);

-- Триггеры для автоматической синхронизации FTS5
CREATE TRIGGER fragments_ai AFTER INSERT ON fragments BEGIN
    INSERT INTO fragments_fts(rowid, text) VALUES (new.id, new.text);
END;

CREATE TRIGGER fragments_ad AFTER DELETE ON fragments BEGIN
    INSERT INTO fragments_fts(fragments_fts, rowid, text) VALUES ('delete', old.id, old.text);
END;

CREATE TRIGGER fragments_au AFTER UPDATE ON fragments BEGIN
    INSERT INTO fragments_fts(fragments_fts, rowid, text) VALUES ('delete', old.id, old.text);
    INSERT INTO fragments_fts(rowid, text) VALUES (new.id, new.text);
END;

-- =========================================================
-- EMBEDDINGS (векторные представления фрагментов)
-- =========================================================
CREATE TABLE embeddings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    fragment_id INTEGER NOT NULL,
    model_name TEXT NOT NULL,
    model_dimension INTEGER NOT NULL,
    embedding_blob BLOB NOT NULL,
    content_version INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (fragment_id) REFERENCES fragments(id) ON DELETE CASCADE,
    UNIQUE (fragment_id, model_name)
);

CREATE INDEX idx_embeddings_fragment ON embeddings(fragment_id);
CREATE INDEX idx_embeddings_model ON embeddings(model_name, content_version);

-- =========================================================
-- CACHE_KEYS (нормализованные вопросы с контекстом)
-- =========================================================
CREATE TABLE cache_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key_hash TEXT NOT NULL UNIQUE,
    question TEXT NOT NULL,
    context_dialog TEXT,
    specialty TEXT,
    group_name TEXT,
    academic_year TEXT,
    resolved_date TEXT,
    model TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_cache_keys_hash ON cache_keys(key_hash);

-- =========================================================
-- CACHE_ENTRIES (сохранённые ответы)
-- =========================================================
CREATE TABLE cache_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key_id INTEGER NOT NULL,
    answer TEXT NOT NULL,
    sources_json TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    invalidated_at TEXT,
    hit_count INTEGER NOT NULL DEFAULT 0,
    last_hit_at TEXT,
    FOREIGN KEY (key_id) REFERENCES cache_keys(id) ON DELETE CASCADE
);

CREATE INDEX idx_cache_entries_key ON cache_entries(key_id);
CREATE INDEX idx_cache_entries_valid ON cache_entries(invalidated_at) WHERE invalidated_at IS NULL;

-- =========================================================
-- CACHE_DEPENDENCIES (связь ответа с фрагментами)
-- =========================================================
CREATE TABLE cache_dependencies (
    cache_entry_id INTEGER NOT NULL,
    fragment_id INTEGER NOT NULL,
    recorded_version INTEGER NOT NULL,
    PRIMARY KEY (cache_entry_id, fragment_id),
    FOREIGN KEY (cache_entry_id) REFERENCES cache_entries(id) ON DELETE CASCADE,
    FOREIGN KEY (fragment_id) REFERENCES fragments(id) ON DELETE CASCADE
);

CREATE INDEX idx_cache_deps_fragment ON cache_dependencies(fragment_id);
`

const migrationV1Part3 = `
-- =========================================================
-- CRAWL_LOG (статистика обходов)
-- =========================================================
CREATE TABLE crawl_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    status TEXT NOT NULL,
    pages_found INTEGER NOT NULL DEFAULT 0,
    pages_new INTEGER NOT NULL DEFAULT 0,
    pages_updated INTEGER NOT NULL DEFAULT 0,
    pages_unchanged INTEGER NOT NULL DEFAULT 0,
    pages_failed INTEGER NOT NULL DEFAULT 0,
    depth_reached INTEGER NOT NULL DEFAULT 0,
    limit_reached TEXT,
    error_message TEXT
);

CREATE INDEX idx_crawl_log_started ON crawl_log(started_at DESC);

-- =========================================================
-- UPDATE_SCHEDULE (расписание обновлений по категориям)
-- =========================================================
CREATE TABLE update_schedule (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    category TEXT NOT NULL UNIQUE,
    interval_seconds INTEGER NOT NULL,
    last_run_at TEXT,
    next_run_at TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX idx_update_schedule_next ON update_schedule(next_run_at) WHERE enabled=1;

-- =========================================================
-- STATS (метрики системы)
-- =========================================================
CREATE TABLE stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT NOT NULL DEFAULT (datetime('now')),
    metric_name TEXT NOT NULL,
    metric_value REAL NOT NULL
);

CREATE INDEX idx_stats_time ON stats(timestamp DESC);
CREATE INDEX idx_stats_name ON stats(metric_name, timestamp DESC);
`


const migrationV2 = `
-- =========================================================
-- Migration V2: FTS5 и embeddings для гибридного поиска
-- =========================================================

-- Полнотекстовый поиск с FTS5
CREATE VIRTUAL TABLE IF NOT EXISTS fragments_fts USING fts5(
    text,
    content='fragments',
    content_rowid='id',
    tokenize='unicode61 remove_diacritics 2'
);

-- Заполняем FTS5 существующими данными
INSERT INTO fragments_fts(rowid, text) 
SELECT id, text FROM fragments;

-- Триггеры для автоматической синхронизации
CREATE TRIGGER fragments_ai AFTER INSERT ON fragments BEGIN
    INSERT INTO fragments_fts(rowid, text) VALUES (new.id, new.text);
END;

CREATE TRIGGER fragments_ad AFTER DELETE ON fragments BEGIN
    INSERT INTO fragments_fts(fragments_fts, rowid, text) VALUES ('delete', old.id, old.text);
END;

CREATE TRIGGER fragments_au AFTER UPDATE ON fragments BEGIN
    INSERT INTO fragments_fts(fragments_fts, rowid, text) VALUES ('delete', old.id, old.text);
    INSERT INTO fragments_fts(rowid, text) VALUES (new.id, new.text);
END;

-- Таблица эмбеддингов
CREATE TABLE embeddings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    fragment_id INTEGER NOT NULL,
    model_name TEXT NOT NULL,
    model_dimension INTEGER NOT NULL,
    embedding_blob BLOB NOT NULL,
    content_version INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (fragment_id) REFERENCES fragments(id) ON DELETE CASCADE,
    UNIQUE (fragment_id, model_name)
);

CREATE INDEX idx_embeddings_fragment ON embeddings(fragment_id);
CREATE INDEX idx_embeddings_model ON embeddings(model_name, content_version);

-- Таблица конфигурации поиска
CREATE TABLE search_config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Начальные настройки
INSERT INTO search_config (key, value) VALUES 
    ('embedding_model', ''),
    ('embedding_dimension', '0'),
    ('lexical_weight', '0.4'),
    ('semantic_weight', '0.6'),
    ('min_score_threshold', '0.3');
`

