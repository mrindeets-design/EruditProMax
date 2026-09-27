# Erudit UTF-8 Fix — Summary of Changes

## Problem
Chatbot was returning incorrect answers about teachers due to:
1. UTF-8 encoding issues (mojibake)
2. Poor text chunking (multiple teachers in one chunk)
3. Equal relevance scores for all teacher fragments
4. Too much context noise for Ollama (15 fragments)

## Solution

### 1. UTF-8 Handling (main.go)
- Added `decodeHTML()` with cascading charset detection
- Added `strings.ToValidUTF8()` for cleaning invalid sequences
- Result: Page loads correctly (31,026 chars)

### 2. Text Normalization (main.go)
- Added punctuation removal in `normalize()`
- Fixed "олеговна?" → "олеговна" issue

### 3. Teacher-Specific Chunking (search.go)
- New function: `splitTeacherChunks()`
- Detects teacher pages: contains("преподава") && contains("@college-nomos.ru")
- Recognizes full names (FIO): 3 words, capitalized, Cyrillic
- Each teacher in separate chunk starting with their name

### 4. Relevance Boost (search.go)
- **Key improvement**: +0.5 bonus for matching ALL keywords
- Example: Query "Богитова Юлия Олеговна" matches all 3 words → +0.5 bonus
- Result: Bogitova relevance 0.0 → 0.860, position #30+ → #1

### 5. Context Optimization (answer.go)
- Reduced fragments sent to Ollama: 15 → 5
- Less noise, more focused context

## Results

✅ "Какие предметы ведёт Богитова Юлия Олеговна?"
   → История, История родного края, Основы этики, История России

✅ "Какие предметы ведёт Юдина Валерия Николаевна?"
   → Педагогическая психология, Информатика и ИКТ, ...

✅ "Какие предметы ведёт Князева Юлия Николаевна?"
   → Живопись, Рисунок, Живопись с основами цветоведения, ...

System correctly distinguishes teachers even with same first names (Юлия).

## Tests: 10/10 passing

## Time: ~2h 45min
