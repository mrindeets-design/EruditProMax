package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// PDFDocument представляет извлеченные данные из PDF
type PDFDocument struct {
	URL           string
	Text          string
	Tables        []PDFTable
	ExtractedAt   time.Time
	AcademicYear  string // например "2026-2027"
	DocumentType  string // "price_list", "schedule", "regulations"
}

// PDFTable представляет таблицу из PDF
type PDFTable struct {
	Headers []string
	Rows    [][]string
}

// PriceInfo структурированная информация о стоимости
type PriceInfo struct {
	Specialty    string
	SpecialtyCode string
	Course       int
	Year         string
	Period       string // "за год", "за семестр", "за месяц"
	Amount       float64
}

// ExtractPDFLinks находит все PDF-ссылки на странице
func ExtractPDFLinks(htmlContent, baseURL string) []string {
	var links []string
	
	// Паттерны для поиска PDF-ссылок
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`href=["']([^"']+\.pdf)["']`),
		regexp.MustCompile(`href=["'](https?://[^"']+\.pdf)["']`),
		regexp.MustCompile(`<a[^>]+href=["']([^"']*upload[^"']*\.pdf)["']`),
	}
	
	seen := make(map[string]bool)
	
	for _, pattern := range patterns {
		matches := pattern.FindAllStringSubmatch(htmlContent, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			
			link := match[1]
			
			// Нормализация относительных ссылок
			if !strings.HasPrefix(link, "http") {
				if strings.HasPrefix(link, "/") {
					link = strings.TrimRight(baseURL, "/") + link
				} else {
					link = strings.TrimRight(baseURL, "/") + "/" + link
				}
			}
			
			if !seen[link] {
				seen[link] = true
				links = append(links, link)
			}
		}
	}
	
	return links
}

// DownloadPDF загружает PDF-файл
func DownloadPDF(url string) ([]byte, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}
	
	return data, nil
}


// ExtractTextFromPDF извлекает текст из PDF (базовая реализация)
func ExtractTextFromPDF(pdfData []byte) (string, error) {
	text := string(pdfData)
	var extracted strings.Builder
	
	// Паттерн для извлечения текста между BT (Begin Text) и ET (End Text)
	btPattern := regexp.MustCompile(`BT\s+(.*?)\s+ET`)
	matches := btPattern.FindAllStringSubmatch(text, -1)
	
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		
		textBlock := match[1]
		stringPattern := regexp.MustCompile(`\(((?:[^()\\]|\\.)*)\)\s*Tj`)
		stringMatches := stringPattern.FindAllStringSubmatch(textBlock, -1)
		
		for _, sm := range stringMatches {
			if len(sm) < 2 {
				continue
			}
			cleaned := cleanPDFString(sm[1])
			if cleaned != "" {
				extracted.WriteString(cleaned)
				extracted.WriteString(" ")
			}
		}
	}
	
	result := extracted.String()
	if len(result) < 50 {
		result = extractReadableText(pdfData)
	}
	
	return strings.TrimSpace(result), nil
}

func cleanPDFString(s string) string {
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\r`, "\r")
	s = strings.ReplaceAll(s, `\t`, "\t")
	s = strings.ReplaceAll(s, `\\`, `\`)
	s = strings.ReplaceAll(s, `\(`, `(`)
	s = strings.ReplaceAll(s, `\)`, `)`)
	return strings.TrimSpace(s)
}

func extractReadableText(data []byte) string {
	var result strings.Builder
	inWord := false
	var word bytes.Buffer
	
	for _, b := range data {
		if b >= 32 && b <= 126 || b >= 128 {
			word.WriteByte(b)
			inWord = true
		} else if inWord && (b == ' ' || b == '\n' || b == '\r' || b == '\t') {
			if word.Len() > 2 {
				result.WriteString(word.String())
				result.WriteByte(' ')
			}
			word.Reset()
			inWord = false
		} else {
			if word.Len() > 2 {
				result.WriteString(word.String())
				result.WriteByte(' ')
			}
			word.Reset()
			inWord = false
		}
	}
	
	return result.String()
}

// ParsePriceList извлекает информацию о ценах из текста
func ParsePriceList(text string) []PriceInfo {
	var prices []PriceInfo
	lines := strings.Split(text, "\n")
	
	specialtyPattern := regexp.MustCompile(`(?i)(40\.02\.0[14]|44\.02\.02|54\.02\.01)`)
	pricePattern := regexp.MustCompile(`(\d+[\s,]?\d*)\s*(?:руб|₽)`)
	yearPattern := regexp.MustCompile(`20\d{2}[-/]20\d{2}`)
	
	var currentYear string
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		if yearMatch := yearPattern.FindString(line); yearMatch != "" {
			currentYear = yearMatch
		}
		
		if specMatch := specialtyPattern.FindString(line); specMatch != "" {
			specName := detectSpecialtyName(line)
			
			if priceMatch := pricePattern.FindStringSubmatch(line); len(priceMatch) > 1 {
				priceStr := strings.ReplaceAll(priceMatch[1], " ", "")
				priceStr = strings.ReplaceAll(priceStr, ",", "")
				
				var amount float64
				fmt.Sscanf(priceStr, "%f", &amount)
				
				period := "за год"
				if strings.Contains(strings.ToLower(line), "семестр") {
					period = "за семестр"
				} else if strings.Contains(strings.ToLower(line), "месяц") {
					period = "за месяц"
				}
				
				prices = append(prices, PriceInfo{
					Specialty:     specName,
					SpecialtyCode: specMatch,
					Year:          currentYear,
					Period:        period,
					Amount:        amount,
				})
			}
		}
	}
	
	return prices
}

func detectSpecialtyName(line string) string {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "дизайн") {
		return "Дизайн"
	} else if strings.Contains(lower, "юрисп") {
		return "Юриспруденция"
	} else if strings.Contains(lower, "препод") {
		return "Преподавание в начальных классах"
	} else if strings.Contains(lower, "право") {
		return "Право и организация социального обеспечения"
	}
	return ""
}

func FormatPriceInfo(prices []PriceInfo) string {
	if len(prices) == 0 {
		return ""
	}
	
	var result strings.Builder
	result.WriteString("\n=== ИНФОРМАЦИЯ О СТОИМОСТИ ОБУЧЕНИЯ ===\n\n")
	
	bySpecialty := make(map[string][]PriceInfo)
	for _, p := range prices {
		key := p.Specialty
		if key == "" {
			key = p.SpecialtyCode
		}
		bySpecialty[key] = append(bySpecialty[key], p)
	}
	
	for spec, items := range bySpecialty {
		result.WriteString(fmt.Sprintf("Специальность: %s\n", spec))
		for _, item := range items {
			if item.Year != "" {
				result.WriteString(fmt.Sprintf("  Учебный год: %s\n", item.Year))
			}
			result.WriteString(fmt.Sprintf("  Стоимость: %.0f руб. %s\n", item.Amount, item.Period))
		}
		result.WriteString("\n")
	}
	
	return result.String()
}

func DetectAcademicYear(text string) string {
	yearPattern := regexp.MustCompile(`20\d{2}[-/]20\d{2}`)
	if match := yearPattern.FindString(text); match != "" {
		return strings.ReplaceAll(match, "/", "-")
	}
	
	now := time.Now()
	year := now.Year()
	month := now.Month()
	
	if month >= 9 {
		return fmt.Sprintf("%d-%d", year, year+1)
	}
	return fmt.Sprintf("%d-%d", year-1, year)
}
