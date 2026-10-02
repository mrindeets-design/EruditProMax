package main

import (
	"log"
	"regexp"
	"strings"
)

// =========================================================
// INTENT UNDERSTANDING & CONTEXT RESOLUTION
// =========================================================

// Intent представляет понятое намерение пользователя
type Intent struct {
	Type        string            // greeting, question, clarification, topic_change
	Topic       string            // admission, payment, practice, contacts, etc.
	Entities    map[string]string // specialty, group, year, date, etc.
	Conditions  []string          // важные уточнения
	Anaphora    []string          // "там", "это", "такой"
	Question    string            // нормализованный вопрос
	IsAmbiguous bool              // требуется уточнение
	Confidence  float64           // уверенность в понимании
}

// DialogContext хранит контекст диалога
type DialogContext struct {
	History           []DialogTurn
	CurrentTopic      string
	LastEntities      map[string]string
	LastSources       []Source
	TopicChanged      bool
	PendingQuestion   string            // Незавершенный вопрос, ожидающий уточнения
	ExpectedParameter string            // Ожидаемый параметр (specialty, group, date)
	PartialInfo       map[string]string // Уже собранная информация
}

// ToContextStrings преобразует DialogContext в []string для кэша
func (dc *DialogContext) ToContextStrings() []string {
	if dc == nil {
		return nil
	}
	
	var context []string
	
	// Добавляем последние 3 реплики из истории
	start := len(dc.History) - 3
	if start < 0 {
		start = 0
	}
	for i := start; i < len(dc.History); i++ {
		turn := dc.History[i]
		context = append(context, "Q: "+turn.UserMessage)
		if turn.BotReply != "" {
			context = append(context, "A: "+turn.BotReply)
		}
	}
	
	// Добавляем текущую тему
	if dc.CurrentTopic != "" {
		context = append(context, "Topic: "+dc.CurrentTopic)
	}
	
	// Добавляем незавершенный вопрос
	if dc.PendingQuestion != "" {
		context = append(context, "Pending: "+dc.PendingQuestion)
	}
	
	return context
}

// DialogTurn представляет один шаг диалога
type DialogTurn struct {
	UserMessage string
	BotReply    string
	Intent      Intent
	Sources     []Source
	Timestamp   string
}


// UnderstandIntent анализирует вопрос пользователя с учётом контекста
func UnderstandIntent(question string, context *DialogContext) Intent {
	q := normalize(question)
	
	intent := Intent{
		Question:   question,
		Entities:   make(map[string]string),
		Conditions: []string{},
		Anaphora:   []string{},
		Confidence: 1.0,
	}
	
	// 1. Определяем тип сообщения
	if isGreeting(q) {
		intent.Type = "greeting"
		intent.Topic = "general"
		return intent
	}
	
	if isTopicChange(q, context) {
		intent.Type = "topic_change"
		if context != nil {
			context.TopicChanged = true
			context.LastEntities = make(map[string]string)
			// Очищаем незавершенное ожидание уточнения при явной смене темы
			context.PendingQuestion = ""
			context.ExpectedParameter = ""
			context.PartialInfo = nil
			log.Printf("TOPIC CHANGE: cleared pending clarification")
		}
	} else {
		intent.Type = "question"
		if context != nil {
			context.TopicChanged = false
		}
	}
	
	// 2. Извлекаем анафоры
	intent.Anaphora = extractAnaphora(q)
	
	// 3. Разрешаем ссылки на предыдущий контекст
	// ВАЖНО: делаем это ДО detectTopic, чтобы восстановленная тема не перезаписывалась
	topicRestoredFromClarification := false
	if context != nil {
		resolveAnaphora(&intent, context)
		// Если тема была восстановлена из уточнения, пометим это
		if intent.Type == "clarification" && intent.Topic != "" {
			topicRestoredFromClarification = true
		}
	} else {
		log.Printf("SKIPPING resolveAnaphora: context is nil")
	}
	
	// 4. Определяем тему (но не перезаписываем восстановленную из уточнения)
	if !topicRestoredFromClarification {
		intent.Topic = detectTopic(q, context)
	} else {
		log.Printf("TOPIC PROTECTION: keeping restored topic='%s', not calling detectTopic", intent.Topic)
	}
	
	// 5. Извлекаем сущности (специальность, группа, дата, и т.д.)
	extractEntities(q, &intent, context)
	
	// 6. Извлекаем условия и уточнения
	extractConditions(q, &intent)
	
	// 7. Проверяем неоднозначность
	checkAmbiguity(&intent, q, context)
	
	return intent
}

// detectTopic определяет тему вопроса
func detectTopic(q string, context *DialogContext) string {
	topics := map[string][]string{
		"admission": {"поступ", "абитур", "документ", "прием", "приём", "зачисл", "экзамен", "подать", "вступительн"},
		"payment":   {"стоимость", "стоит", "сколько стоит", "цена", "оплат", "скидк", "платн", "бюджет", "контракт", "деньги", "рубл"},
		"practice":  {"практик", "стажировк", "производственн", "учебная практика"},
		"schedule":  {"расписан", "когда занятия", "урок", "пар", "время", "график"},
		"contacts":  {"контакт", "телефон", "адрес", "email", "почта", "где находится", "как добраться"},
		"staff":     {"преподава", "учитель", "учителя", "директор", "руководитель", "завуч", "кто ведет", "кто преподает", "педагог", "препод", "предмет", "дисциплин", "богитова", "юлия"},
		"specialty": {"специальность", "направлен", "профес", "квалификац", "факультет", "кафедра", "отделение"},
		"news":      {"новост", "событи", "мероприят", "когда прошло", "что нового"},
		"documents": {"справк", "аттестат", "диплом", "свидетельств", "документ"},
		"life":      {"общежит", "столов", "кружок", "секци", "студенческ"},
	}
	
	bestTopic := "general"
	maxMatches := 0
	
	log.Printf("DETECT TOPIC: normalized query = '%s'", q)
	
	for topic, keywords := range topics {
		matches := 0
		for _, keyword := range keywords {
			if strings.Contains(q, keyword) {
				matches++
				log.Printf("DETECT TOPIC: matched keyword '%s' for topic '%s'", keyword, topic)
			}
		}
		if matches > maxMatches {
			maxMatches = matches
			bestTopic = topic
		}
	}
	
	log.Printf("DETECT TOPIC: result = '%s' (matches=%d)", bestTopic, maxMatches)
	
	// Если не нашли явную тему, используем контекст
	if bestTopic == "general" && context != nil && !context.TopicChanged {
		bestTopic = context.CurrentTopic
		log.Printf("DETECT TOPIC: using context topic = '%s'", bestTopic)
	}
	
	return bestTopic
}

// isGreeting проверяет приветствия
func isGreeting(q string) bool {
	// Проверяем, содержит ли вопрос приветственные слова
	greetingWords := []string{
		"привет", "здравствуй", "добрый день", "добрый вечер",
		"доброе утро", "hi", "hello", "hey",
	}
	
	hasGreeting := false
	for _, greeting := range greetingWords {
		if strings.HasPrefix(q, greeting) {
			hasGreeting = true
			break
		}
	}
	
	if !hasGreeting {
		return false
	}
	
	// Если есть приветствие, проверяем наличие вопросительных слов или знака вопроса
	questionWords := []string{
		"что", "как", "где", "когда", "какой", "какие", "какая", "сколько", 
		"почему", "можно", "подскажи", "расскажи", "скажи", "нужн", "требуется",
	}
	
	for _, word := range questionWords {
		if strings.Contains(q, word) {
			return false // Есть вопрос после приветствия
		}
	}
	
	if strings.Contains(q, "?") {
		return false // Есть знак вопроса
	}
	
	// Чистое приветствие без вопроса
	return true
}

// isTopicChange определяет явную смену темы
func isTopicChange(q string, context *DialogContext) bool {
	changeMarkers := []string{
		"другой вопрос", "теперь", "а теперь", "кстати",
		"еще вопрос", "ещё вопрос", "смени тему", "хватит про",
		"давай о другом", "другая тема",
	}
	
	for _, marker := range changeMarkers {
		if strings.Contains(q, marker) {
			return true
		}
	}
	
	return false
}

// extractAnaphora находит анафорические ссылки
func extractAnaphora(q string) []string {
	anaphora := []string{}
	
	anaphoricWords := []string{
		"там", "это", "этот", "эта", "эти", "такой", "такая", "такие",
		"он", "она", "они", "его", "её", "их",
		"а сколько", "а какие", "а когда", "а где",
	}
	
	for _, word := range anaphoricWords {
		if strings.Contains(q, word) {
			anaphora = append(anaphora, word)
		}
	}
	
	return anaphora
}

// resolveAnaphora разрешает ссылки на предыдущий контекст
func resolveAnaphora(intent *Intent, context *DialogContext) {
	q := normalize(intent.Question)
	
	// Обработка коротких ответов (продолжений)
	isShortAnswer := len(strings.Fields(q)) <= 3
	
	// ПРИОРИТЕТ 1: Обработка незавершенного вопроса (работает независимо от History)
	if context.PendingQuestion != "" && context.ExpectedParameter != "" {
		// Проверяем, является ли это ответом на уточнение или новым вопросом
		// Новый вопрос имеет больше 5 слов или содержит явные вопросительные слова
		wordCount := len(strings.Fields(q))
		hasQuestionMark := strings.Contains(q, "?")
		hasExplicitQuestion := containsAny(q, "расскажи", "подскажи", "скажи", "хочу узнать", 
			"интересует", "где находится", "как добраться", "когда начинается", "что такое")
		
		isNewQuestion := (wordCount > 5 && hasQuestionMark) || hasExplicitQuestion
		
		if isShortAnswer && !isNewQuestion {
			// Это уточнение к предыдущему вопросу
			intent.Type = "clarification"
			
			// Восстанавливаем исходный вопрос и добавляем уточнение
			if context.ExpectedParameter == "specialty" {
				// Пользователь ответил названием специальности
				// Формируем полный вопрос: исходный + уточнение
				intent.Question = context.PendingQuestion + " по специальности " + q
				
				// Извлекаем специальность из ответа пользователя
				extractEntities(q, intent, context)
				
				// Восстанавливаем тему из исходного вопроса
				if context.PartialInfo != nil {
					if topic, ok := context.PartialInfo["topic"]; ok {
						intent.Topic = topic
						log.Printf("CLARIFICATION: restored topic='%s' from PartialInfo", topic)
					}
					// Копируем уже известную информацию (кроме topic)
					for k, v := range context.PartialInfo {
						if k != "topic" {
							intent.Entities[k] = v
						}
					}
				}
			} else if context.ExpectedParameter == "group" {
				intent.Question = context.PendingQuestion + " для группы " + q
			} else {
				intent.Question = context.PendingQuestion + " " + q
			}
			
			// Очищаем ожидание после успешного разрешения
			context.PendingQuestion = ""
			context.ExpectedParameter = ""
			context.PartialInfo = nil
			
			log.Printf("CLARIFICATION RESOLVED: question='%s' topic='%s' entities=%v", intent.Question, intent.Topic, intent.Entities)
			return // Завершаем обработку, не продолжаем дальше
		} else {
			// Новый вопрос во время ожидания уточнения - отменяем ожидание
			log.Printf("CLARIFICATION CANCELLED: new question detected while pending")
			context.PendingQuestion = ""
			context.ExpectedParameter = ""
			context.PartialInfo = nil
		}
	}
	
	// ПРИОРИТЕТ 2: Обработка анафор из предыдущих реплик (требует History)
	if len(context.History) == 0 {
		return
	}
	
	lastTurn := context.History[len(context.History)-1]
	
	// Копируем сущности из предыдущего контекста
	log.Printf("RESOLVE_ANAPHORA: before copy - intent.Entities=%v context.LastEntities=%v", intent.Entities, context.LastEntities)
	for k, v := range context.LastEntities {
		if _, exists := intent.Entities[k]; !exists {
			intent.Entities[k] = v
		}
	}
	log.Printf("RESOLVE_ANAPHORA: after copy - intent.Entities=%v", intent.Entities)
	
	// Наследуем тему, если текущая не определена чётко
	if intent.Topic == "general" && context.CurrentTopic != "" && !context.TopicChanged {
		intent.Topic = context.CurrentTopic
	}
	
	// Обрабатываем короткие продолжения без явных маркеров
	if isShortAnswer && !strings.Contains(q, "?") && context.CurrentTopic != "" {
		// Короткий ответ в контексте диалога - скорее всего продолжение
		intent.Type = "clarification"
		
		// Пытаемся понять, это название специальности или другое уточнение
		if detectSpecialtyInText(q) != "" {
			intent.Entities["specialty"] = detectSpecialtyInText(q)
			intent.Topic = context.CurrentTopic // Сохраняем тему
		}
	}
	
	// Обрабатываем "а сколько", "а какие" и т.д.
	if strings.HasPrefix(q, "а ") {
		// Это продолжение предыдущего вопроса
		intent.Type = "clarification"
		
		// Извлекаем новый аспект вопроса
		if strings.Contains(q, "сколько") {
			// "А сколько стоит" - меняем тему на payment
			if lastTurn.Intent.Topic == "admission" || lastTurn.Intent.Topic == "specialty" {
				intent.Topic = "payment"
				// Наследуем специальность
				if spec, ok := context.LastEntities["specialty"]; ok {
					intent.Entities["specialty"] = spec
				}
			}
		} else if strings.Contains(q, "бюджет") {
			intent.Topic = "admission"
			// Наследуем специальность
			if spec, ok := context.LastEntities["specialty"]; ok {
				intent.Entities["specialty"] = spec
			}
		}
	}
	
	// Обрабатываем "там"
	if strings.Contains(q, "там") && len(context.LastSources) > 0 {
		// Пользователь ссылается на источники из предыдущего ответа
		intent.Conditions = append(intent.Conditions, "в упомянутых источниках")
	}
	
	// Обрабатываем местоимения
	if containsAny(q, "это", "этот", "эта") && context.CurrentTopic != "" {
		// "Это платно?" после обсуждения специальности
		intent.Topic = context.CurrentTopic
		for k, v := range context.LastEntities {
			intent.Entities[k] = v
		}
	}
}

// detectSpecialtyInText пытается определить специальность в коротком тексте
func detectSpecialtyInText(text string) string {
	text = normalize(text)
	
	// Возвращаем полное название специальности, а не код
	specialties := map[string]string{
		"дизайн":             "дизайн",
		"графический дизайн": "дизайн",
		"юрист":              "юриспруденция",
		"юриспруденц":        "юриспруденция",
		"юридическ":          "юриспруденция",
		"право":              "право и социальное обеспечение",
		"социальн":           "право и социальное обеспечение",
		"преподав":           "преподавание в начальных классах",
		"начальн":            "преподавание в начальных классах",
		"учитель":            "преподавание в начальных классах",
	}
	
	for pattern, name := range specialties {
		if strings.Contains(text, pattern) {
			return name
		}
	}
	
	return ""
}



// extractEntities извлекает сущности из вопроса
func extractEntities(q string, intent *Intent, context *DialogContext) {
	// Специальности - сопоставление паттернов с полными названиями
	specialtyPatterns := map[string]struct{
		name string
		code string
	}{
		"дизайн":             {"дизайн", "54.02.01"},
		"графический дизайн": {"дизайн", "54.02.01"},
		"юрист":              {"юриспруденция", "40.02.04"},
		"право":              {"юриспруденция", "40.02.04"},
		"юриспруденц":        {"юриспруденция", "40.02.04"},
		"программир":         {"программирование", "09.02.07"},
		"информационн":       {"информационные системы", "09.02.07"},
		"сети":               {"компьютерные сети", "09.02.06"},
		"системн":            {"информационные системы", "09.02.07"},
		"туризм":             {"туризм", "43.02.16"},
		"туристическ":        {"туризм", "43.02.16"},
		"экономик":           {"экономика", "38.02.01"},
		"бухгалтер":          {"экономика", "38.02.01"},
		"преподав":           {"преподавание в начальных классах", "44.02.02"},
		"начальн":            {"преподавание в начальных классах", "44.02.02"},
	}
	
	for pattern, spec := range specialtyPatterns {
		if strings.Contains(q, pattern) {
			intent.Entities["specialty"] = spec.name
			intent.Entities["specialty_code"] = spec.code
			break
		}
	}
	
	// Группы
	groupPattern := regexp.MustCompile(`\b([А-Я]{1,3}-?\d{2,3})\b`)
	if match := groupPattern.FindString(q); match != "" {
		intent.Entities["group"] = match
	}
	
	// Года
	yearPattern := regexp.MustCompile(`\b(202[3-9]|20[3-9]\d)\b`)
	if match := yearPattern.FindString(q); match != "" {
		intent.Entities["year"] = match
	}
	
	// Курс
	coursePattern := regexp.MustCompile(`\b([1-4])\s*курс`)
	if match := coursePattern.FindStringSubmatch(q); len(match) > 1 {
		intent.Entities["course"] = match[1]
	}
	
	// Форма обучения
	if strings.Contains(q, "очн") && !strings.Contains(q, "заочн") {
		intent.Entities["form"] = "очная"
	} else if strings.Contains(q, "заочн") {
		intent.Entities["form"] = "заочная"
	}
	
	// Бюджет/контракт
	if strings.Contains(q, "бюджет") {
		intent.Entities["payment_type"] = "бюджет"
	} else if strings.Contains(q, "контракт") || strings.Contains(q, "платн") {
		intent.Entities["payment_type"] = "контракт"
	}
}

// extractConditions извлекает важные условия
func extractConditions(q string, intent *Intent) {
	conditions := []struct {
		pattern   string
		condition string
	}{
		{"после 9 класс", "после 9 класса"},
		{"после 11 класс", "после 11 класса"},
		{"с красным диплом", "с отличием"},
		{"льгот", "для льготников"},
		{"инвалид", "для инвалидов"},
		{"целевое", "целевое направление"},
		{"дистанционн", "дистанционно"},
	}
	
	for _, cond := range conditions {
		if strings.Contains(q, cond.pattern) {
			intent.Conditions = append(intent.Conditions, cond.condition)
		}
	}
}

// checkAmbiguity проверяет неоднозначность вопроса
func checkAmbiguity(intent *Intent, q string, context *DialogContext) {
	// Вопрос требует уточнения, если:
	
	// 1. Упоминается несколько специальностей без явного сравнения
	specialtyCount := 0
	specialties := []string{"дизайн", "юрист", "программир", "туризм", "экономик", "преподав"}
	for _, spec := range specialties {
		if strings.Contains(q, spec) {
			specialtyCount++
		}
	}
	
	if specialtyCount > 1 && !containsAny(q, "сравни", "разница", "отличие", "лучше") {
		intent.IsAmbiguous = true
		intent.Confidence = 0.5
		return
	}
	
	// 2. Вопрос о стоимости или бюджете без указания специальности
	if containsAny(q, "стоимость", "цена", "оплат", "бюджет", "платн", "контракт") {
		hasSpecialty := false
		for _, spec := range specialties {
			if strings.Contains(q, spec) {
				hasSpecialty = true
				break
			}
		}
		
		// Проверяем, есть ли специальность в entities (уже извлеченная)
		if !hasSpecialty && intent.Entities["specialty"] != "" {
			hasSpecialty = true
		}
		
		// Проверяем контекст - если в предыдущем ответе была специальность, используем её
		if !hasSpecialty && context != nil && context.LastEntities["specialty"] != "" {
			// Наследуем специальность из контекста
			intent.Entities["specialty"] = context.LastEntities["specialty"]
			hasSpecialty = true
			log.Printf("checkAmbiguity: inherited specialty='%s' from context.LastEntities", intent.Entities["specialty"])
		} else if !hasSpecialty && context != nil {
			log.Printf("checkAmbiguity: NO specialty in context! context.LastEntities=%v", context.LastEntities)
		}
		
		// Не требуем уточнения для общих вопросов типа "Есть ли бюджетные места?"
		if !hasSpecialty && !containsAny(q, "вообще", "в принципе", "есть ли", "бывают ли") {
			intent.IsAmbiguous = true
			intent.Confidence = 0.6
			return
		}
	}
	
	// 3. Вопрос слишком общий без контекста (только одиночные слова-вопросы)
	vague := []string{
		"^расскажи$", "^что$", "^какие$", "^как$", "^где$",
	}
	
	for _, pattern := range vague {
		matched, _ := regexp.MatchString(pattern, q)
		if matched {
			intent.IsAmbiguous = true
			intent.Confidence = 0.3
			return
		}
	}
}

// BuildClarificationQuestion формирует уточняющий вопрос
func BuildClarificationQuestion(intent Intent, dialogContext *DialogContext) string {
	if !intent.IsAmbiguous {
		return ""
	}
	
	q := normalize(intent.Question)
	
	log.Printf("BuildClarificationQuestion: q='%s' topic='%s' entities=%v", q, intent.Topic, intent.Entities)
	
	// Неоднозначная специальность для вопросов о стоимости/бюджете
	if containsAny(q, "стоимость", "цена", "оплат", "бюджет", "платн", "контракт") {
		if _, hasSpec := intent.Entities["specialty"]; !hasSpec {
			// Проверяем, есть ли специальность в контексте диалога
			if dialogContext != nil && dialogContext.LastEntities != nil {
				if _, hasSpecInContext := dialogContext.LastEntities["specialty"]; hasSpecInContext {
					log.Printf("BuildClarificationQuestion: specialty found in LastEntities, no clarification needed")
					return "" // Специальность уже известна из контекста
				}
			}
			
			clarification := "Уточните, пожалуйста, о какой специальности вы спрашиваете: дизайн, юриспруденция, преподавание в начальных классах или право и социальное обеспечение?"
			log.Printf("BuildClarificationQuestion: returning payment clarification")
			return clarification
		}
	}
	
	// Слишком общий вопрос
	if intent.Confidence < 0.5 {
		switch intent.Topic {
		case "admission":
			return "Что именно вас интересует о поступлении: документы, сроки, экзамены или условия зачисления?"
		case "payment":
			return "Уточните, пожалуйста, о какой специальности идёт речь: дизайн, юриспруденция, преподавание или право?"
		case "general":
			return "Уточните, пожалуйста, ваш вопрос. О чём именно вы хотите узнать?"
		}
	}
	
	log.Printf("BuildClarificationQuestion: returning empty (no match)")
	return ""
}

