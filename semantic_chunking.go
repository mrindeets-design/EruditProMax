package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log"
	"regexp"
	"strings"
)

// =========================================================
// SEMANTIC CHUNKING WITH CONTEXT PRESERVATION
// =========================================================

// EnrichedFragment представляет фрагмент с метаданными
type EnrichedFragment struct {
	PageID        int64
	DocumentID    int64
	ChunkIndex    int
	Text          string
	TextHash      string
	SectionTitle  string
	ParentTitles  string // JSON
	ContentType   string
	SpecialtyCode string
	AcademicYear  string
	URLStable     string
	VersionNumber int
}

// CreateEnrichedFragments создаёт фрагменты с сохранением контекста
func CreateEnrichedFragments(db *sql.DB, pageID int64, doc *DocumentStructure) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Удаляем старые фрагменты
	_, err = tx.Exec(`DELETE FROM fragments WHERE page_id = ?`, pageID)
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO fragments (
			page_id, chunk_index, text, text_hash, version_number,
			section_title, parent_titles, content_type, specialty_code, academic_year, url_stable
		) VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	chunkIndex := 0
	urlStable := computeStableURLID(doc.URL)

	// Обрабатываем каждую секцию
	for _, section := range doc.Sections {
		specialtyCode := extractSpecialtyCodeFromTitles(section.ParentTitles)
		academicYear := extractAcademicYear(section.Content)

		chunks := splitSemanticSection(section.Content, 600, 150)

		for _, chunk := range chunks {
			if len(strings.TrimSpace(chunk)) < 50 {
				continue
			}

			fragment := EnrichedFragment{
				PageID:        pageID,
				ChunkIndex:    chunkIndex,
				Text:          chunk,
				TextHash:      computeHash(chunk),
				SectionTitle:  section.Title,
				ParentTitles:  SerializeParentTitles(section.ParentTitles),
				ContentType:   "text",
				SpecialtyCode: specialtyCode,
				AcademicYear:  academicYear,
				URLStable:     urlStable,
			}

			_, err = stmt.Exec(
				fragment.PageID, fragment.ChunkIndex, fragment.Text, fragment.TextHash,
				fragment.SectionTitle, fragment.ParentTitles, fragment.ContentType,
				fragment.SpecialtyCode, fragment.AcademicYear, fragment.URLStable,
			)
			if err != nil {
				return err
			}

			chunkIndex++
		}
	}

	// Обрабатываем таблицы отдельно
	for _, table := range doc.Tables {
		tableText := formatTableAsText(table)
		if len(strings.TrimSpace(tableText)) < 50 {
			continue
		}

		contentType := detectTableType(table)
		
		fragment := EnrichedFragment{
			PageID:       pageID,
			ChunkIndex:   chunkIndex,
			Text:         tableText,
			TextHash:     computeHash(tableText),
			SectionTitle: table.Caption,
			ContentType:  contentType,
			URLStable:    urlStable,
		}

		_, err = stmt.Exec(
			fragment.PageID, fragment.ChunkIndex, fragment.Text, fragment.TextHash,
			fragment.SectionTitle, "[]", fragment.ContentType, "", "", fragment.URLStable,
		)
		if err != nil {
			return err
		}

		chunkIndex++
	}

	log.Printf("Created %d enriched fragments for page %d", chunkIndex, pageID)
	return tx.Commit()
}

// splitSemanticSection разбивает секцию на смысловые фрагменты
func splitSemanticSection(content string, targetSize int, overlap int) []string {
	if len(content) <= targetSize {
		return []string{content}
	}

	var chunks []string
	paragraphs := strings.Split(content, "\n\n")
	var currentChunk strings.Builder
	
	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		if currentChunk.Len()+len(para) > targetSize && currentChunk.Len() > 0 {
			chunks = append(chunks, currentChunk.String())
			
			prevText := currentChunk.String()
			if len(prevText) > overlap {
				currentChunk.Reset()
				currentChunk.WriteString(prevText[len(prevText)-overlap:])
				currentChunk.WriteString(" ")
			} else {
				currentChunk.Reset()
			}
		}

		if currentChunk.Len() > 0 {
			currentChunk.WriteString("\n\n")
		}
		currentChunk.WriteString(para)
	}

	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}

	return chunks
}

// extractSpecialtyCodeFromTitles извлекает код специальности из заголовков
func extractSpecialtyCodeFromTitles(titles []string) string {
	pattern := regexp.MustCompile(`(\d{2}\.\d{2}\.\d{2})`)
	
	for _, title := range titles {
		if match := pattern.FindString(title); match != "" {
			return match
		}
	}
	
	return ""
}

// extractAcademicYear извлекает учебный год из текста
func extractAcademicYear(text string) string {
	pattern := regexp.MustCompile(`20\d{2}[-/]20\d{2}`)
	if match := pattern.FindString(text); match != "" {
		return strings.ReplaceAll(match, "/", "-")
	}
	return ""
}

// computeStableURLID создаёт стабильный идентификатор на основе URL
func computeStableURLID(url string) string {
	hash := sha256.Sum256([]byte(url))
	return hex.EncodeToString(hash[:])[:16]
}

// formatTableAsText форматирует таблицу в текст
func formatTableAsText(table TableStructure) string {
	var text strings.Builder
	
	if table.Caption != "" {
		text.WriteString(table.Caption)
		text.WriteString("\n\n")
	}
	
	if len(table.Headers) > 0 {
		text.WriteString(strings.Join(table.Headers, " | "))
		text.WriteString("\n")
	}
	
	for _, row := range table.Rows {
		text.WriteString(strings.Join(row, " | "))
		text.WriteString("\n")
	}
	
	return text.String()
}

// detectTableType определяет тип таблицы по заголовкам
func detectTableType(table TableStructure) string {
	if len(table.Headers) == 0 {
		return "table"
	}
	
	headersLower := strings.ToLower(strings.Join(table.Headers, " "))
	
	if strings.Contains(headersLower, "стоимость") || strings.Contains(headersLower, "цена") {
		return "price_table"
	}
	if strings.Contains(headersLower, "расписание") || strings.Contains(headersLower, "время") {
		return "schedule_table"
	}
	if strings.Contains(headersLower, "специальность") {
		return "specialty_table"
	}
	
	return "table"
}

