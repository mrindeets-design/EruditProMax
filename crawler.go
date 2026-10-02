package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type CrawlerConfig struct {
	MaxDepth        int
	MaxPages        int
	MaxFileSize     int64
	WorkerCount     int
	RequestDelay    time.Duration
	RequestTimeout  time.Duration
	AllowedHosts    []string
	SkipExtensions  []string
	SkipPaths       []string
	FollowRedirects bool
	MaxRedirects    int
}

func DefaultCrawlerConfig() CrawlerConfig {
	return CrawlerConfig{
		MaxDepth:        3,
		MaxPages:        200,
		MaxFileSize:     10 * 1024 * 1024,
		WorkerCount:     3,
		RequestDelay:    500 * time.Millisecond,
		RequestTimeout:  30 * time.Second,
		AllowedHosts:    []string{},
		SkipExtensions:  []string{".jpg", ".jpeg", ".png", ".gif", ".css", ".js", ".xml", ".zip", ".rar", ".ico"},
		SkipPaths:       []string{"/bitrix/", "/upload/iblock/", "/local/", "/ajax/"},
		FollowRedirects: true,
		MaxRedirects:    5,
	}
}

type Crawler struct {
	db         *sql.DB
	config     CrawlerConfig
	baseURL    string
	visited    map[string]bool
	queue      []crawlItem
	mu         sync.Mutex
	pagesCount int32
	client     *http.Client
	logID      int64
	stopChan   chan struct{}
	wg         sync.WaitGroup
	stats      CrawlStats
}

type crawlItem struct {
	url   string
	depth int
}

type CrawlStats struct {
	PagesFound     int32
	PagesNew       int32
	PagesUpdated   int32
	PagesUnchanged int32
	PagesFailed    int32
	DepthReached   int32
}

func NewCrawler(db *sql.DB, baseURL string, config CrawlerConfig) *Crawler {
	if len(config.AllowedHosts) == 0 {
		u, _ := url.Parse(baseURL)
		if u != nil {
			config.AllowedHosts = []string{u.Host}
		}
	}

	return &Crawler{
		db:       db,
		config:   config,
		baseURL:  strings.TrimRight(baseURL, "/"),
		visited:  make(map[string]bool),
		queue:    []crawlItem{},
		stopChan: make(chan struct{}),
		client: &http.Client{
			Timeout: config.RequestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if !config.FollowRedirects {
					return http.ErrUseLastResponse
				}
				if len(via) >= config.MaxRedirects {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
}

func (c *Crawler) Start() error {
	log.Printf("🔄 Starting crawler: %s", c.baseURL)
	log.Printf("   Max depth: %d, Max pages: %d, Workers: %d", c.config.MaxDepth, c.config.MaxPages, c.config.WorkerCount)

	result, err := c.db.Exec(`INSERT INTO crawl_log (started_at, status, pages_found) VALUES (?, 'running', 0)`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("failed to create crawl log: %w", err)
	}

	c.logID, err = result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get log ID: %w", err)
	}

	c.queue = append(c.queue, crawlItem{url: c.baseURL + "/", depth: 0})

	for i := 0; i < c.config.WorkerCount; i++ {
		c.wg.Add(1)
		go c.worker(i)
	}

	c.wg.Wait()
	return c.finishCrawl()
}

func (c *Crawler) Stop() {
	close(c.stopChan)
}

func (c *Crawler) worker(id int) {
	defer c.wg.Done()

	for {
		select {
		case <-c.stopChan:
			return
		default:
		}

		c.mu.Lock()
		if len(c.queue) == 0 || atomic.LoadInt32(&c.pagesCount) >= int32(c.config.MaxPages) {
			c.mu.Unlock()
			return
		}

		item := c.queue[0]
		c.queue = c.queue[1:]
		c.mu.Unlock()

		if c.visited[item.url] {
			continue
		}

		c.visited[item.url] = true
		atomic.AddInt32(&c.pagesCount, 1)

		log.Printf("Worker %d [%d/%d, depth=%d]: %s", id, atomic.LoadInt32(&c.pagesCount), c.config.MaxPages, item.depth, item.url)

		if err := c.processURL(item.url, item.depth); err != nil {
			log.Printf("  ❌ Error: %v", err)
			atomic.AddInt32(&c.stats.PagesFailed, 1)
		}

		time.Sleep(c.config.RequestDelay)
	}
}

func (c *Crawler) processURL(pageURL string, depth int) error {
	if depth > int(atomic.LoadInt32(&c.stats.DepthReached)) {
		atomic.StoreInt32(&c.stats.DepthReached, int32(depth))
	}

	if c.shouldSkipURL(pageURL) {
		return nil
	}

	lower := strings.ToLower(pageURL)
	if strings.HasSuffix(lower, ".pdf") {
		return c.processDocument(pageURL)
	}

	return c.processPage(pageURL, depth)
}



func (c *Crawler) processPage(pageURL string, depth int) error {
	atomic.AddInt32(&c.stats.PagesFound, 1)

	resp, err := c.client.Get(pageURL)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}

	finalURL := resp.Request.URL.String()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.config.MaxFileSize))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	htmlContent := decodeHTML(body, resp.Header.Get("Content-Type"))
	cleanText := extractTextFromHTML(htmlContent)
	contentHash := computeHash(cleanText)

	title := extractTitle(htmlContent, pageURL)
	specialtyCode, specialtyName := extractSpecialty(htmlContent, pageURL)
	academicYear := DetectAcademicYear(cleanText)
	kind := c.detectPageKind(pageURL)

	sourceID, err := c.getOrCreateSource(pageURL, title, kind)
	if err != nil {
		return fmt.Errorf("get source: %w", err)
	}

	var existingID int64
	var existingHash string
	err = c.db.QueryRow(`SELECT id, content_hash FROM pages WHERE url = ?`, pageURL).Scan(&existingID, &existingHash)

	now := time.Now().UTC().Format(time.RFC3339)

	if err == sql.ErrNoRows {
		result, err := c.db.Exec(`
			INSERT INTO pages (
				source_id, url, final_url, content_hash, clean_text, title,
				specialty_code, specialty_name, academic_year,
				version_number, fetched_at, last_check_at, last_success_at,
				http_etag, http_last_modified
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)
		`, sourceID, pageURL, finalURL, contentHash, cleanText, title,
			specialtyCode, specialtyName, academicYear,
			now, now, now,
			resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"))

		if err != nil {
			return fmt.Errorf("insert page: %w", err)
		}

		pageID, _ := result.LastInsertId()
		log.Printf("  ✅ New page saved (id=%d)", pageID)

		if err := c.createFragments(pageID, 0, cleanText); err != nil {
			log.Printf("  ⚠️  Failed to create fragments: %v", err)
		}

		atomic.AddInt32(&c.stats.PagesNew, 1)

	} else if err != nil {
		return fmt.Errorf("check existing page: %w", err)
	} else {
		if existingHash == contentHash {
			_, err = c.db.Exec(`UPDATE pages SET last_check_at = ?, last_success_at = ? WHERE id = ?`, now, now, existingID)
			if err != nil {
				return fmt.Errorf("update check time: %w", err)
			}

			log.Printf("  ✓ Unchanged (id=%d)", existingID)
			atomic.AddInt32(&c.stats.PagesUnchanged, 1)

		} else {
			var currentVersion int
			c.db.QueryRow(`SELECT version_number FROM pages WHERE id = ?`, existingID).Scan(&currentVersion)
			newVersion := currentVersion + 1

			_, err = c.db.Exec(`
				INSERT INTO page_versions (page_id, version_number, content_hash, clean_text, changed_at)
				VALUES (?, ?, ?, (SELECT clean_text FROM pages WHERE id = ?), ?)
			`, existingID, currentVersion, existingHash, existingID, now)

			_, err = c.db.Exec(`
				UPDATE pages SET
					final_url = ?, content_hash = ?, clean_text = ?, title = ?,
					specialty_code = ?, specialty_name = ?, academic_year = ?,
					version_number = ?, fetched_at = ?, last_check_at = ?, last_success_at = ?,
					http_etag = ?, http_last_modified = ?
				WHERE id = ?
			`, finalURL, contentHash, cleanText, title,
				specialtyCode, specialtyName, academicYear,
				newVersion, now, now, now,
				resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"),
				existingID)

			if err != nil {
				return fmt.Errorf("update page: %w", err)
			}

			c.db.Exec(`DELETE FROM fragments WHERE page_id = ?`, existingID)
			if err := c.createFragments(existingID, 0, cleanText); err != nil {
				log.Printf("  ⚠️  Failed to update fragments: %v", err)
			}

			log.Printf("  🔄 Updated (id=%d, v%d→v%d)", existingID, currentVersion, newVersion)
			atomic.AddInt32(&c.stats.PagesUpdated, 1)
		}
	}

	if depth < c.config.MaxDepth {
		links := c.extractLinks(htmlContent, pageURL)
		c.mu.Lock()
		for _, link := range links {
			if !c.visited[link] && !c.isInQueue(link) {
				c.queue = append(c.queue, crawlItem{url: link, depth: depth + 1})
			}
		}
		c.mu.Unlock()
	}

	pdfLinks := ExtractPDFLinks(htmlContent, c.baseURL)
	for _, pdfURL := range pdfLinks {
		c.mu.Lock()
		if !c.visited[pdfURL] && !c.isInQueue(pdfURL) {
			c.queue = append(c.queue, crawlItem{url: pdfURL, depth: depth + 1})
		}
		c.mu.Unlock()
	}

	return nil
}



func (c *Crawler) processDocument(docURL string) error {
	atomic.AddInt32(&c.stats.PagesFound, 1)
	log.Printf("  📄 Processing document: %s", docURL)

	resp, err := c.client.Get(docURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}

	fileData, err := io.ReadAll(io.LimitReader(resp.Body, c.config.MaxFileSize))
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	fileSize := int64(len(fileData))
	fileHash := computeHash(string(fileData))

	extractedText, err := ExtractTextFromPDF(fileData)
	if err != nil || len(extractedText) < 50 {
		log.Printf("  ⚠️  Text extraction failed or insufficient")
		extractedText = ""
	}

	contentHash := computeHash(extractedText)
	academicYear := DetectAcademicYear(extractedText)
	title := extractPageTitle(docURL)

	sourceID, err := c.getOrCreateSource(docURL, title, "document")
	if err != nil {
		return fmt.Errorf("get source: %w", err)
	}

	var existingID int64
	var existingFileHash string
	err = c.db.QueryRow(`SELECT id, file_hash FROM documents WHERE url = ?`, docURL).Scan(&existingID, &existingFileHash)

	now := time.Now().UTC().Format(time.RFC3339)

	if err == sql.ErrNoRows {
		result, err := c.db.Exec(`
			INSERT INTO documents (
				source_id, url, file_hash, content_hash, file_size,
				extracted_text, ocr_attempted, ocr_successful,
				title, academic_year, version_number,
				fetched_at, last_check_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		`, sourceID, docURL, fileHash, contentHash, fileSize,
			extractedText, 0, len(extractedText) > 50,
			title, academicYear, now, now)

		if err != nil {
			return fmt.Errorf("insert document: %w", err)
		}

		docID, _ := result.LastInsertId()
		log.Printf("  ✅ New document saved (id=%d, %d bytes)", docID, fileSize)

		if len(extractedText) > 50 {
			if err := c.createFragments(0, docID, extractedText); err != nil {
				log.Printf("  ⚠️  Failed to create fragments: %v", err)
			}
		}

		atomic.AddInt32(&c.stats.PagesNew, 1)

	} else if err != nil {
		return fmt.Errorf("check existing document: %w", err)
	} else {
		if existingFileHash == fileHash {
			_, err = c.db.Exec(`UPDATE documents SET last_check_at = ? WHERE id = ?`, now, existingID)
			if err != nil {
				return fmt.Errorf("update check time: %w", err)
			}

			log.Printf("  ✓ Unchanged (id=%d)", existingID)
			atomic.AddInt32(&c.stats.PagesUnchanged, 1)

		} else {
			var currentVersion int
			c.db.QueryRow(`SELECT version_number FROM documents WHERE id = ?`, existingID).Scan(&currentVersion)
			newVersion := currentVersion + 1

			_, err = c.db.Exec(`
				UPDATE documents SET
					file_hash = ?, content_hash = ?, file_size = ?,
					extracted_text = ?, ocr_successful = ?,
					title = ?, academic_year = ?,
					version_number = ?, fetched_at = ?, last_check_at = ?
				WHERE id = ?
			`, fileHash, contentHash, fileSize,
				extractedText, len(extractedText) > 50,
				title, academicYear,
				newVersion, now, now, existingID)

			if err != nil {
				return fmt.Errorf("update document: %w", err)
			}

			c.db.Exec(`DELETE FROM fragments WHERE document_id = ?`, existingID)
			if len(extractedText) > 50 {
				if err := c.createFragments(0, existingID, extractedText); err != nil {
					log.Printf("  ⚠️  Failed to update fragments: %v", err)
				}
			}

			log.Printf("  🔄 Updated (id=%d, v%d→v%d)", existingID, currentVersion, newVersion)
			atomic.AddInt32(&c.stats.PagesUpdated, 1)
		}
	}

	return nil
}

func (c *Crawler) createFragments(pageID, docID int64, text string) error {
	// Простое разбиение на части по 800 символов с перекрытием
	const chunkSize = 800
	const overlap = 100

	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO fragments (page_id, document_id, chunk_index, text, text_hash, version_number)
		VALUES (?, ?, ?, ?, ?, 1)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	var pid, did interface{}
	if pageID > 0 {
		pid = pageID
	}
	if docID > 0 {
		did = docID
	}

	chunkIndex := 0
	for i := 0; i < len(text); i += chunkSize - overlap {
		end := i + chunkSize
		if end > len(text) {
			end = len(text)
		}

		chunk := text[i:end]
		if len(strings.TrimSpace(chunk)) < 50 {
			continue
		}

		chunkHash := computeHash(chunk)
		if _, err := stmt.Exec(pid, did, chunkIndex, chunk, chunkHash); err != nil {
			return err
		}
		chunkIndex++

		if end == len(text) {
			break
		}
	}

	return tx.Commit()
}

func (c *Crawler) getOrCreateSource(pageURL, title, kind string) (int64, error) {
	var sourceID int64
	err := c.db.QueryRow(`SELECT id FROM sources WHERE url = ?`, pageURL).Scan(&sourceID)
	if err == nil {
		return sourceID, nil
	}

	category := c.detectCategory(pageURL)
	result, err := c.db.Exec(`
		INSERT INTO sources (url, name, kind, category, check_interval_seconds, enabled)
		VALUES (?, ?, ?, ?, 86400, 1)
	`, pageURL, title, kind, category)

	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

func (c *Crawler) detectPageKind(pageURL string) string {
	lower := strings.ToLower(pageURL)

	if strings.Contains(lower, "/teachers/") {
		return "teachers"
	} else if strings.Contains(lower, "/abitur/specialties/") {
		return "specialty_detail"
	} else if strings.Contains(lower, "/specialties/") {
		return "specialties"
	} else if strings.Contains(lower, "/schedule/") {
		return "schedule"
	} else if strings.Contains(lower, "/news/") {
		return "news"
	} else if strings.Contains(lower, "/sveden/paid_edu/") {
		return "payment"
	} else if strings.Contains(lower, "/sveden/education/") {
		return "education"
	} else if strings.Contains(lower, "/sveden/document/") {
		return "documents"
	} else if strings.Contains(lower, "/sveden/") {
		return "official"
	} else if strings.Contains(lower, "/students/") {
		return "students"
	} else if strings.Contains(lower, "/contacts/") {
		return "contacts"
	}
	return "general"
}

func (c *Crawler) detectCategory(pageURL string) string {
	lower := strings.ToLower(pageURL)

	if strings.Contains(lower, "/schedule/") {
		return "schedule"
	} else if strings.Contains(lower, "/news/") {
		return "news"
	} else if strings.Contains(lower, "/abitur/") || strings.Contains(lower, "/paid_edu/") {
		return "admission"
	} else if strings.Contains(lower, "/sveden/education/") || strings.Contains(lower, "/students/") {
		return "education"
	} else if strings.Contains(lower, "/teachers/") {
		return "teachers"
	} else if strings.Contains(lower, "/document/") {
		return "documents"
	}
	return "general"
}

func (c *Crawler) extractLinks(html, baseURL string) []string {
	var links []string
	linkPattern := regexp.MustCompile(`<a[^>]+href=["']([^"']+)["']`)
	matches := linkPattern.FindAllStringSubmatch(html, -1)

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		link := match[1]
		if strings.HasPrefix(link, "#") || strings.HasPrefix(link, "javascript:") || strings.HasPrefix(link, "mailto:") {
			continue
		}

		absoluteURL := c.normalizeURL(link, baseURL)
		if c.isAllowedURL(absoluteURL) && !c.shouldSkipURL(absoluteURL) {
			links = append(links, absoluteURL)
		}
	}

	return links
}

func (c *Crawler) normalizeURL(link, baseURL string) string {
	if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
		return link
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return link
	}

	ref, err := url.Parse(link)
	if err != nil {
		return link
	}

	return base.ResolveReference(ref).String()
}

func (c *Crawler) isAllowedURL(urlStr string) bool {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false
	}

	for _, allowedHost := range c.config.AllowedHosts {
		if u.Host == allowedHost || strings.HasSuffix(u.Host, "."+allowedHost) {
			return true
		}
	}

	return false
}

func (c *Crawler) shouldSkipURL(urlStr string) bool {
	lower := strings.ToLower(urlStr)

	for _, ext := range c.config.SkipExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}

	for _, path := range c.config.SkipPaths {
		if strings.Contains(lower, path) {
			return true
		}
	}

	return false
}

func (c *Crawler) isInQueue(urlStr string) bool {
	for _, item := range c.queue {
		if item.url == urlStr {
			return true
		}
	}
	return false
}

func (c *Crawler) finishCrawl() error {
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := c.db.Exec(`
		UPDATE crawl_log SET
			finished_at = ?,
			status = 'completed',
			pages_found = ?,
			pages_new = ?,
			pages_updated = ?,
			pages_unchanged = ?,
			pages_failed = ?,
			depth_reached = ?
		WHERE id = ?
	`, now,
		atomic.LoadInt32(&c.stats.PagesFound),
		atomic.LoadInt32(&c.stats.PagesNew),
		atomic.LoadInt32(&c.stats.PagesUpdated),
		atomic.LoadInt32(&c.stats.PagesUnchanged),
		atomic.LoadInt32(&c.stats.PagesFailed),
		atomic.LoadInt32(&c.stats.DepthReached),
		c.logID)

	log.Printf("✅ Crawl completed:")
	log.Printf("   Found: %d, New: %d, Updated: %d, Unchanged: %d, Failed: %d",
		atomic.LoadInt32(&c.stats.PagesFound),
		atomic.LoadInt32(&c.stats.PagesNew),
		atomic.LoadInt32(&c.stats.PagesUpdated),
		atomic.LoadInt32(&c.stats.PagesUnchanged),
		atomic.LoadInt32(&c.stats.PagesFailed))

	return err
}

func extractTitle(html, fallbackURL string) string {
	titlePattern := regexp.MustCompile(`<title[^>]*>([^<]+)</title>`)
	if match := titlePattern.FindStringSubmatch(html); len(match) > 1 {
		title := strings.TrimSpace(match[1])
		if title != "" {
			return title
		}
	}

	h1Pattern := regexp.MustCompile(`<h1[^>]*>([^<]+)</h1>`)
	if match := h1Pattern.FindStringSubmatch(html); len(match) > 1 {
		title := strings.TrimSpace(match[1])
		if title != "" {
			return title
		}
	}

	return extractPageTitle(fallbackURL)
}

func extractPageTitle(urlStr string) string {
	parts := strings.Split(strings.TrimRight(urlStr, "/"), "/")
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if last != "" && !strings.Contains(last, ".") {
			title := strings.ReplaceAll(last, "-", " ")
			title = strings.ReplaceAll(title, "_", " ")
			return strings.Title(title)
		}
	}
	return urlStr
}

func extractSpecialty(html, pageURL string) (string, string) {
	codePattern := regexp.MustCompile(`(\d{2}\.\d{2}\.\d{2})`)
	if match := codePattern.FindStringSubmatch(html); len(match) > 1 {
		code := match[1]

		namePattern := regexp.MustCompile(code + `\s*[«"]?([А-Яа-яёЁ\s-]+)[»"]?`)
		if nameMatch := namePattern.FindStringSubmatch(html); len(nameMatch) > 1 {
			return code, strings.TrimSpace(nameMatch[1])
		}

		return code, ""
	}

	return "", ""
}

func computeHash(text string) string {
	hash := sha256.Sum256([]byte(text))
	return hex.EncodeToString(hash[:])
}
