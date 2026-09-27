package main

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Crawler автоматический индексатор сайта
type Crawler struct {
	db          *sql.DB
	baseURL     string
	visited     map[string]bool
	queue       []string
	mu          sync.Mutex
	maxDepth    int
	maxPages    int
	pagesCount  int
	client      *http.Client
}

// NewCrawler создает новый краулер
func NewCrawler(db *sql.DB, baseURL string) *Crawler {
	return &Crawler{
		db:       db,
		baseURL:  strings.TrimRight(baseURL, "/"),
		visited:  make(map[string]bool),
		queue:    []string{},
		maxDepth: 3,
		maxPages: 100,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// CrawlSite запускает полное сканирование сайта
func (c *Crawler) CrawlSite() error {
	log.Println("Starting site crawl...")
	c.queue = append(c.queue, c.baseURL+"/")
	
	for len(c.queue) > 0 && c.pagesCount < c.maxPages {
		c.mu.Lock()
		if len(c.queue) == 0 {
			c.mu.Unlock()
			break
		}
		
		currentURL := c.queue[0]
		c.queue = c.queue[1:]
		c.mu.Unlock()
		
		if c.visited[currentURL] {
			continue
		}
		
		c.visited[currentURL] = true
		c.pagesCount++
		
		log.Printf("Crawling [%d/%d]: %s", c.pagesCount, c.maxPages, currentURL)
		
		links, err := c.crawlPage(currentURL)
		if err != nil {
			log.Printf("Error crawling %s: %v", currentURL, err)
			continue
		}
		
		c.mu.Lock()
		for _, link := range links {
			if !c.visited[link] && !c.isInQueue(link) {
				c.queue = append(c.queue, link)
			}
		}
		c.mu.Unlock()
		
		time.Sleep(500 * time.Millisecond)
	}
	
	log.Printf("Crawl completed: %d pages indexed", c.pagesCount)
	return nil
}

// crawlPage обрабатывает одну страницу
func (c *Crawler) crawlPage(pageURL string) ([]string, error) {
	resp, err := c.client.Get(pageURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	
	kind := c.detectPageKind(pageURL)
	
	_, err = c.db.Exec(`
		INSERT OR IGNORE INTO sources (url, name, kind, priority, enabled)
		VALUES (?, ?, ?, ?, 1)
	`, pageURL, extractPageTitle(pageURL), kind, 5)
	
	if err != nil {
		log.Printf("Warning: failed to save source %s: %v", pageURL, err)
	}
	
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}
	
	// Используем decodeHTML из main.go для правильной обработки кодировки
	htmlContent := decodeHTML(body, resp.Header.Get("Content-Type"))
	
	links := c.extractLinks(htmlContent, pageURL)
	
	pdfLinks := ExtractPDFLinks(htmlContent, c.baseURL)
	for _, pdfURL := range pdfLinks {
		go c.processPDF(pdfURL)
	}
	
	return links, nil
}


// detectPageKind определяет тип страницы по URL
func (c *Crawler) detectPageKind(pageURL string) string {
	lower := strings.ToLower(pageURL)
	
	if strings.Contains(lower, "/teachers/") {
		return "teachers"
	} else if strings.Contains(lower, "/abitur/specialties/") && len(strings.Split(lower, "/")) > 5 {
		return "specialty_detail"
	} else if strings.Contains(lower, "/specialties/") {
		return "specialties"
	} else if strings.Contains(lower, "/abitur/") {
		return "admission"
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
	} else if strings.Contains(lower, "/students/practice/") {
		return "practice"
	} else if strings.Contains(lower, "/students/schedule/") {
		return "schedule"
	} else if strings.Contains(lower, "/students/") {
		return "students"
	} else if strings.Contains(lower, "/retake/") {
		return "retake"
	} else if strings.Contains(lower, "/contacts/") {
		return "contacts"
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
		if strings.HasPrefix(link, "#") || strings.HasPrefix(link, "javascript:") {
			continue
		}
		absoluteURL := c.normalizeURL(link, baseURL)
		if strings.HasPrefix(absoluteURL, c.baseURL) && !c.shouldSkipURL(absoluteURL) {
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

func (c *Crawler) shouldSkipURL(urlStr string) bool {
	lower := strings.ToLower(urlStr)
	skipExtensions := []string{".jpg", ".jpeg", ".png", ".gif", ".css", ".js", ".xml", ".zip", ".rar"}
	for _, ext := range skipExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	skipPaths := []string{"/bitrix/", "/upload/", "/local/", "/ajax/"}
	for _, path := range skipPaths {
		if strings.Contains(lower, path) {
			return true
		}
	}
	return false
}

func (c *Crawler) isInQueue(urlStr string) bool {
	for _, u := range c.queue {
		if u == urlStr {
			return true
		}
	}
	return false
}

func (c *Crawler) processPDF(pdfURL string) {
	log.Printf("Processing PDF: %s", pdfURL)
	data, err := DownloadPDF(pdfURL)
	if err != nil {
		log.Printf("Failed to download PDF %s: %v", pdfURL, err)
		return
	}
	text, err := ExtractTextFromPDF(data)
	if err != nil {
		log.Printf("Failed to extract text from PDF %s: %v", pdfURL, err)
		return
	}
	_, err = c.db.Exec(`
		INSERT OR REPLACE INTO documents (url, content, document_type, extracted_at, academic_year)
		VALUES (?, ?, ?, ?, ?)
	`, pdfURL, text, "pdf", time.Now().UTC(), DetectAcademicYear(text))
	
	if err != nil {
		log.Printf("Failed to save PDF content: %v", err)
	} else {
		log.Printf("PDF saved: %s (%d chars)", pdfURL, len(text))
	}
}

func extractPageTitle(urlStr string) string {
	parts := strings.Split(strings.TrimRight(urlStr, "/"), "/")
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if last != "" {
			title := strings.ReplaceAll(last, "-", " ")
			return strings.Title(title)
		}
	}
	return urlStr
}
