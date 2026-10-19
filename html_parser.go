package main

import (
	"encoding/json"
	"golang.org/x/net/html"
	"io"
	"regexp"
	"strings"
)

// =========================================================
// STRUCTURED HTML PARSER
// =========================================================

// DocumentStructure представляет структурированный документ
type DocumentStructure struct {
	Title        string
	Description  string
	Sections     []Section
	Tables       []TableStructure
	Metadata     map[string]string
	URL          string
	PublishedAt  string
}

// Section представляет раздел документа с иерархией
type Section struct {
	Level        int      // H1=1, H2=2, etc.
	Title        string
	Content      string
	ParentTitles []string // Иерархия заголовков
	Position     int
}

// TableStructure представляет таблицу с сохранением структуры
type TableStructure struct {
	Caption string
	Headers []string
	Rows    [][]string
}

// ParseHTMLStructure извлекает структуру из HTML
func ParseHTMLStructure(htmlContent string, pageURL string) (*DocumentStructure, error) {
	doc := &DocumentStructure{
		Sections:  []Section{},
		Tables:    []TableStructure{},
		Metadata:  make(map[string]string),
		URL:       pageURL,
	}

	// Парсим HTML
	tokenizer := html.NewTokenizer(strings.NewReader(htmlContent))
	
	var currentSection *Section
	var contentBuffer strings.Builder
	var headingStack []string // Стек заголовков для иерархии
	sectionPosition := 0

	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			err := tokenizer.Err()
			if err == io.EOF {
				break
			}
			return nil, err
		}

		token := tokenizer.Token()

		switch tokenType {
		case html.StartTagToken, html.SelfClosingTagToken:
			switch token.Data {
			case "title":
				if tokenizer.Next() == html.TextToken {
					doc.Title = strings.TrimSpace(tokenizer.Token().Data)
				}

			case "meta":
				name, content := extractMetaTag(token)
				if name != "" && content != "" {
					doc.Metadata[name] = content
					if name == "description" {
						doc.Description = content
					}
				}

			case "h1", "h2", "h3", "h4", "h5", "h6":
				// Сохраняем предыдущую секцию
				if currentSection != nil && contentBuffer.Len() > 0 {
					currentSection.Content = strings.TrimSpace(contentBuffer.String())
					doc.Sections = append(doc.Sections, *currentSection)
					contentBuffer.Reset()
				}

				// Извлекаем уровень заголовка
				level := int(token.Data[1] - '0') // h1 -> 1, h2 -> 2, etc.
				
				// Читаем текст заголовка
				headingText := extractTextUntilClosingTag(tokenizer, token.Data)
				headingText = strings.TrimSpace(headingText)

				// Обновляем стек заголовков
				headingStack = updateHeadingStack(headingStack, level, headingText)

				// Создаём новую секцию
				currentSection = &Section{
					Level:        level,
					Title:        headingText,
					ParentTitles: make([]string, len(headingStack)),
					Position:     sectionPosition,
				}
				copy(currentSection.ParentTitles, headingStack)
				sectionPosition++

			case "table":
				// Извлекаем таблицу
				tableHTML := extractElementHTML(tokenizer, "table")
				table := parseTableStructure(tableHTML)
				if table != nil && len(table.Rows) > 0 {
					doc.Tables = append(doc.Tables, *table)
				}

			case "script", "style", "nav", "footer", "header":
				// Пропускаем содержимое этих тегов
				skipUntilClosingTag(tokenizer, token.Data)
			}

		case html.TextToken:
			text := strings.TrimSpace(token.Data)
			if text != "" && currentSection != nil {
				if contentBuffer.Len() > 0 {
					contentBuffer.WriteString(" ")
				}
				contentBuffer.WriteString(text)
			}
		}
	}

	// Сохраняем последнюю секцию
	if currentSection != nil && contentBuffer.Len() > 0 {
		currentSection.Content = strings.TrimSpace(contentBuffer.String())
		doc.Sections = append(doc.Sections, *currentSection)
	}

	// Извлекаем дату публикации
	doc.PublishedAt = extractPublishedDate(doc.Metadata, pageURL)

	return doc, nil
}

// extractMetaTag извлекает name и content из meta-тега
func extractMetaTag(token html.Token) (string, string) {
	var name, content string
	for _, attr := range token.Attr {
		switch attr.Key {
		case "name", "property":
			name = attr.Val
		case "content":
			content = attr.Val
		}
	}
	return name, content
}

// extractTextUntilClosingTag извлекает текст до закрывающего тега
func extractTextUntilClosingTag(tokenizer *html.Tokenizer, tagName string) string {
	var text strings.Builder
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}
		token := tokenizer.Token()
		if tokenType == html.EndTagToken && token.Data == tagName {
			break
		}
		if tokenType == html.TextToken {
			text.WriteString(token.Data)
		}
	}
	return text.String()
}

// extractElementHTML извлекает полный HTML элемента
func extractElementHTML(tokenizer *html.Tokenizer, tagName string) string {
	var htmlBuf strings.Builder
	depth := 1
	
	for depth > 0 {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}
		token := tokenizer.Token()
		htmlBuf.WriteString(token.String())
		
		if tokenType == html.StartTagToken && token.Data == tagName {
			depth++
		} else if tokenType == html.EndTagToken && token.Data == tagName {
			depth--
		}
	}
	return htmlBuf.String()
}

// skipUntilClosingTag пропускает содержимое до закрывающего тега
func skipUntilClosingTag(tokenizer *html.Tokenizer, tagName string) {
	depth := 1
	for depth > 0 {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}
		token := tokenizer.Token()
		if tokenType == html.StartTagToken && token.Data == tagName {
			depth++
		} else if tokenType == html.EndTagToken && token.Data == tagName {
			depth--
		}
	}
}

// updateHeadingStack обновляет стек заголовков при встрече нового заголовка
func updateHeadingStack(stack []string, level int, title string) []string {
	// Обрезаем стек до уровня (level-1)
	if level-1 < len(stack) {
		stack = stack[:level-1]
	}
	// Добавляем текущий заголовок
	stack = append(stack, title)
	return stack
}

// parseTableStructure парсит HTML таблицы
func parseTableStructure(tableHTML string) *TableStructure {
	table := &TableStructure{
		Headers: []string{},
		Rows:    [][]string{},
	}

	tokenizer := html.NewTokenizer(strings.NewReader(tableHTML))
	inThead := false
	var currentRow []string

	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}
		token := tokenizer.Token()

		switch tokenType {
		case html.StartTagToken:
			switch token.Data {
			case "thead":
				inThead = true
			case "tr":
				currentRow = []string{}
			case "th", "td":
				cellText := extractTextUntilClosingTag(tokenizer, token.Data)
				cellText = strings.TrimSpace(cellText)
				if cellText != "" {
					currentRow = append(currentRow, cellText)
				}
			case "caption":
				table.Caption = extractTextUntilClosingTag(tokenizer, "caption")
			}

		case html.EndTagToken:
			switch token.Data {
			case "thead":
				inThead = false
			case "tr":
				if len(currentRow) > 0 {
					if inThead || (len(table.Headers) == 0 && len(table.Rows) == 0) {
						table.Headers = currentRow
					} else {
						table.Rows = append(table.Rows, currentRow)
					}
				}
			}
		}
	}

	return table
}

// extractPublishedDate извлекает дату публикации
func extractPublishedDate(metadata map[string]string, pageURL string) string {
	// Проверяем meta-теги
	if date, ok := metadata["article:published_time"]; ok {
		return date
	}
	if date, ok := metadata["datePublished"]; ok {
		return date
	}

	// Ищем дату в URL (например, /news/2025/10/13/)
	datePattern := regexp.MustCompile(`/(\d{4})/(\d{1,2})/(\d{1,2})/`)
	if matches := datePattern.FindStringSubmatch(pageURL); len(matches) == 4 {
		return matches[1] + "-" + matches[2] + "-" + matches[3]
	}

	return ""
}

// SerializeParentTitles сериализует массив в JSON для БД
func SerializeParentTitles(titles []string) string {
	if len(titles) == 0 {
		return "[]"
	}
	data, _ := json.Marshal(titles)
	return string(data)
}

// DeserializeParentTitles десериализует JSON в массив
func DeserializeParentTitles(jsonStr string) []string {
	var titles []string
	if jsonStr == "" || jsonStr == "[]" {
		return titles
	}
	json.Unmarshal([]byte(jsonStr), &titles)
	return titles
}

