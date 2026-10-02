-- =========================================================
-- Исправление информации о колледже "Номос"
-- =========================================================
-- Дата: 02.10.2026
-- Что исправляем:
-- 1. Общежития НЕТ
-- 2. Рабочее время: ПН-ПТ 14:00-19:00, СБ-ВС выходной

-- =========================================================
-- 1. Удаляем упоминания об общежитии
-- =========================================================

DELETE FROM fragments 
WHERE content LIKE '%общежит%' 
  AND (content LIKE '%есть%' OR content LIKE '%имеется%' OR content LIKE '%предоставляе%');

-- Добавляем правильную информацию об отсутствии общежития
INSERT INTO sources (url, name, kind, category, enabled) 
VALUES ('internal://college-info/dormitory', 'Информация об общежитии', 'internal', 'info', 1)
ON CONFLICT(url) DO UPDATE SET name=excluded.name;

INSERT INTO pages (source_id, url, content_hash, clean_text, title, fetched_at, last_check_at, last_success_at)
SELECT 
    s.id,
    'internal://college-info/dormitory',
    'hash_dormitory_2026_10_02',
    'У Воронежского колледжа «Номос» нет собственного общежития. Студентам при необходимости можно помочь с поиском жилья в Воронеже.',
    'Информация об общежитии',
    datetime('now'),
    datetime('now'),
    datetime('now')
FROM sources s
WHERE s.url = 'internal://college-info/dormitory'
ON CONFLICT(url) DO UPDATE SET 
    clean_text=excluded.clean_text,
    content_hash=excluded.content_hash,
    last_check_at=datetime('now');

INSERT INTO fragments (page_id, fragment_index, content, tokens)
SELECT 
    p.id,
    1,
    'Общежитие: У Воронежского колледжа «Номос» нет собственного общежития. Иногородним студентам при необходимости можем помочь с поиском жилья в Воронеже.',
    50
FROM pages p
WHERE p.url = 'internal://college-info/dormitory'
ON CONFLICT(page_id, fragment_index) DO UPDATE SET content=excluded.content;

-- =========================================================
-- 2. Исправляем рабочее время колледжа
-- =========================================================

DELETE FROM fragments 
WHERE content LIKE '%10:00%16:00%' 
   OR (content LIKE '%часы работы%' AND content LIKE '%понедельник%пятниц%');

INSERT INTO sources (url, name, kind, category, enabled) 
VALUES ('internal://college-info/working-hours', 'Режим работы колледжа', 'internal', 'contacts', 1)
ON CONFLICT(url) DO UPDATE SET name=excluded.name;

INSERT INTO pages (source_id, url, content_hash, clean_text, title, fetched_at, last_check_at, last_success_at)
SELECT 
    s.id,
    'internal://college-info/working-hours',
    'hash_working_hours_2026_10_02',
    'Режим работы колледжа «Номос»: с понедельника по пятницу с 14:00 до 19:00. Суббота и воскресенье — выходные дни. Телефон: +7 (473) 271-35-36. Адрес: 394036, г. Воронеж, ул. Пятницкого, 67.',
    'Режим работы',
    datetime('now'),
    datetime('now'),
    datetime('now')
FROM sources s
WHERE s.url = 'internal://college-info/working-hours'
ON CONFLICT(url) DO UPDATE SET 
    clean_text=excluded.clean_text,
    content_hash=excluded.content_hash,
    last_check_at=datetime('now');

INSERT INTO fragments (page_id, fragment_index, content, tokens)
SELECT 
    p.id,
    1,
    'Режим работы: Воронежский колледж «Номос» работает с понедельника по пятницу с 14:00 до 19:00. Суббота и воскресенье — выходные дни. Контакты: телефон +7 (473) 271-35-36, адрес: г. Воронеж, ул. Пятницкого, 67.',
    60
FROM pages p
WHERE p.url = 'internal://college-info/working-hours'
ON CONFLICT(page_id, fragment_index) DO UPDATE SET content=excluded.content;
