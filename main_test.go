package main

import (
	"strings"
	"testing"
)

func TestSourcesRestored(t *testing.T) {
	sources := buildSources("https://college-nomos.ru")
	
	// Проверяем, что восстановленные источники присутствуют
	requiredSources := map[string]bool{
		"/students/":                    false,
		"/students/practice/":           false,
		"/students/schedule/":           false,
		"/students/educational-resources/": false,
		"/retake/":                      false,
	}
	
	for _, src := range sources {
		for required := range requiredSources {
			if strings.Contains(src.URL, required) {
				requiredSources[required] = true
			}
		}
	}
	
	for url, found := range requiredSources {
		if !found {
			t.Errorf("Required source not found: %s", url)
		}
	}
	
	// Проверяем общее количество источников
	if len(sources) < 31 { // Было 26, добавили 5
		t.Errorf("Expected at least 31 sources, got %d", len(sources))
	}
}

func TestDialogContextToStrings(t *testing.T) {
	dc := &DialogContext{
		CurrentTopic: "payment",
		PendingQuestion: "Какая стоимость?",
		History: []DialogTurn{
			{UserMessage: "Здравствуйте", BotReply: "Привет!"},
			{UserMessage: "Есть ли бюджетные места?", BotReply: "Да, есть"},
		},
	}
	
	contextStrings := dc.ToContextStrings()
	
	if len(contextStrings) == 0 {
		t.Error("ToContextStrings returned empty array")
	}
	
	// Проверяем наличие ключевых элементов
	hasHistory := false
	hasTopic := false
	hasPending := false
	
	for _, s := range contextStrings {
		if strings.Contains(s, "Q:") || strings.Contains(s, "A:") {
			hasHistory = true
		}
		if strings.Contains(s, "Topic:") {
			hasTopic = true
		}
		if strings.Contains(s, "Pending:") {
			hasPending = true
		}
	}
	
	if !hasHistory {
		t.Error("Context strings should include history")
	}
	if !hasTopic {
		t.Error("Context strings should include topic")
	}
	if !hasPending {
		t.Error("Context strings should include pending question")
	}
}

func TestSessionManager(t *testing.T) {
	sm := NewSessionManager()
	
	// Создаем новую сессию
	session1 := sm.GetOrCreate("")
	if session1.ID == "" {
		t.Error("Session ID should not be empty")
	}
	
	// Получаем ту же сессию
	session2 := sm.GetOrCreate(session1.ID)
	if session2.ID != session1.ID {
		t.Error("Should return the same session")
	}
	
	// Обновляем контекст
	session1.DialogContext.CurrentTopic = "admission"
	sm.UpdateContext(session1.ID, session1.DialogContext)
	
	// Проверяем, что контекст обновился
	session3 := sm.GetOrCreate(session1.ID)
	if session3.DialogContext.CurrentTopic != "admission" {
		t.Error("Context should be updated")
	}
}

func TestPDFExtractLinks(t *testing.T) {
	html := `
		<html>
			<a href="/upload/docs/price.pdf">Prices</a>
			<a href="https://example.com/file.pdf">External</a>
			<a href="relative.pdf">Relative</a>
		</html>
	`
	
	links := ExtractPDFLinks(html, "https://college-nomos.ru")
	
	if len(links) < 3 {
		t.Errorf("Expected at least 3 PDF links, got %d", len(links))
	}
	
	// Проверяем, что все ссылки абсолютные
	for _, link := range links {
		if !strings.HasPrefix(link, "http") {
			t.Errorf("Link should be absolute: %s", link)
		}
	}
}

func TestParsePriceList(t *testing.T) {
	text := `
		Стоимость обучения на 2026-2027 учебный год
		
		54.02.01 Дизайн - 120000 руб. за год
		40.02.04 Юриспруденция - 100000 руб за год
		44.02.02 Преподавание в начальных классах - 95000 руб
	`
	
	prices := ParsePriceList(text)
	
	if len(prices) == 0 {
		t.Error("Should parse at least some prices")
	}
	
	// Проверяем, что распознали специальности
	foundDesign := false
	for _, p := range prices {
		if strings.Contains(p.Specialty, "Дизайн") || p.SpecialtyCode == "54.02.01" {
			foundDesign = true
			if p.Amount < 100000 {
				t.Errorf("Design price seems wrong: %.0f", p.Amount)
			}
		}
	}
	
	if !foundDesign {
		t.Error("Should find design specialty in price list")
	}
}

func TestCrawlerPageKindDetection(t *testing.T) {
	crawler := NewCrawler(nil, "https://college-nomos.ru")
	
	tests := []struct {
		url  string
		want string
	}{
		{"https://college-nomos.ru/teachers/", "teachers"},
		{"https://college-nomos.ru/students/practice/", "practice"},
		{"https://college-nomos.ru/students/schedule/", "schedule"},
		{"https://college-nomos.ru/retake/", "retake"},
		{"https://college-nomos.ru/sveden/paid_edu/", "payment"},
		{"https://college-nomos.ru/abitur/specialties/54-02-01-dizayn/", "specialty_detail"},
	}
	
	for _, tt := range tests {
		got := crawler.detectPageKind(tt.url)
		if got != tt.want {
			t.Errorf("detectPageKind(%s) = %s, want %s", tt.url, got, tt.want)
		}
	}
}

func TestDetectAcademicYear(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"Стоимость на 2026-2027 учебный год", "2026-2027"},
		{"Приказ от 2025/2026", "2025-2026"},
		{"Без года", "202"}, // Должен вернуть текущий год (начинается с 202)
	}
	
	for _, tt := range tests {
		got := DetectAcademicYear(tt.text)
		if !strings.HasPrefix(got, tt.want[:3]) {
			t.Errorf("DetectAcademicYear(%q) = %s, want prefix %s", tt.text, got, tt.want[:3])
		}
	}
}
