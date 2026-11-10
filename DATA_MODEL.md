# LifeOS — DATA_MODEL.md

## Обзор

Начальная модель данных для LifeOS v1. Персональная система управления задачами с интеллектуальным ассистентом.

**Принципы:**
- SQLite — единственный источник истины
- `modernc.org/sqlite` (pure Go, без CGO)
- `sqlc` для типизированных запросов
- `goose` для миграций
- WAL mode, foreign keys ON
- Нет ORM — чистый SQL

---

## Сущности

### 1. Task (Задача)

**Назначение:** Основная единица работы. Вся бизнес-логика вращается вокруг задач.

| Поле | Тип | Ограничения | Описание |
|------|-----|-------------|----------|
| `id` | INTEGER | PK, AUTOINCREMENT | Уникальный идентификатор |
| `title` | TEXT | NOT NULL, 1-500 chars | Заголовок задачи |
| `note` | TEXT | NULLABLE, max 5000 | Дополнительная заметка |
| `priority` | TEXT | NOT NULL, CHECK IN ('low','medium','high') | Приоритет |
| `status` | TEXT | NOT NULL, CHECK IN ('open','completed','cancelled'), DEFAULT 'open' | Статус |
| `planned_date` | DATE | NULLABLE | Дата планирования (календарная, без времени) |
| `due_date` | DATE | NULLABLE | Календарный дедлайн (без времени) |
| `due_at` | TIMESTAMP | NULLABLE | Точный момент дедлайна (UTC) |
| `timezone` | TEXT | NOT NULL, DEFAULT 'Europe/Moscow' | IANA TZ для planned/due дат |
| `created_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | UTC момент создания |
| `updated_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | UTC последнего изменения |
| `completed_at` | TIMESTAMP | NULLABLE | UTC момент завершения |
| `cancelled_at` | TIMESTAMP | NULLABLE | UTC момент отмены |
| `parent_task_id` | INTEGER | FK → tasks(id), NULLABLE | Для будущих подзадач (v1: не используется) |

**Индексы:**
- `idx_tasks_status` (status)
- `idx_tasks_priority` (priority)
- `idx_tasks_planned_date` (planned_date)
- `idx_tasks_due_date` (due_date)
- `idx_tasks_due_at` (due_at)
- `idx_tasks_created_at` (created_at)

**Валидация (Domain):**
- `title` не пустой, ≤ 500 символов
- `priority` ∈ {low, medium, high}
- `status` ∈ {open, completed, cancelled}
- Переходы статусов: `open → completed`, `open → cancelled`, `completed → open`, `cancelled → open`
- `due_at` если задан — должен быть ≥ `created_at`
- `planned_date` и `due_date` — календарные даты (DATE), не смещаются при смене TZ
- `timezone` — валидная IANA TZ

**Просроченность (вычисляется, не хранится):**
```go
func (t *Task) IsOverdue(now time.Time) bool {
    if t.Status != "open" { return false }
    if t.DueAt != nil { return now.After(*t.DueAt) }
    if t.DueDate != nil {
        // due_date — конец дня в таймзоне задачи
        loc, _ := time.LoadLocation(t.Timezone)
        endOfDay := time.Date(t.DueDate.Year(), t.DueDate.Month(), t.DueDate.Day(), 23, 59, 59, 0, loc)
        return now.After(endOfDay)
    }
    return false
}
```

---

### 2. InboxItem (Входящий элемент)

**Назначение:** Быстрый захват — мысль, задача, ссылка до структурирования.

| Поле | Тип | Ограничения | Описание |
|------|-----|-------------|----------|
| `id` | INTEGER | PK, AUTOINCREMENT | |
| `text` | TEXT | NOT NULL, 1-2000 chars | Сырой текст ввода |
| `meta` | TEXT | NULLABLE, JSON | Дополнительные метаданные (источник, теги, entities) |
| `created_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | UTC |
| `processed_at` | TIMESTAMP | NULLABLE | UTC когда преобразован/удалён |

**Индексы:**
- `idx_inbox_created_at` (created_at DESC)

**Валидация:**
- `text` не пустой, ≤ 2000 символов
- `meta` — валидный JSON если задан

**Жизненный цикл:**
1. `Capture` — создание
2. `ConvertToTask` → создаёт Task, ставит `processed_at`, удаляет InboxItem (или soft delete)
3. `Dismiss` — ставит `processed_at`, удаляет

---

### 3. TaskEvent (Событие задачи)

**Назначение:** Append-only история изменений. Не Event Sourcing — просто аудит.

| Поле | Тип | Ограничения | Описание |
|------|-----|-------------|----------|
| `id` | INTEGER | PK, AUTOINCREMENT | |
| `task_id` | INTEGER | NOT NULL, FK → tasks(id) | |
| `type` | TEXT | NOT NULL, CHECK IN ('created','updated','completed','cancelled','rescheduled','linked','converted_from_inbox') | Тип события |
| `payload` | TEXT | NOT NULL, JSON | Детали изменения (old/new values) |
| `actor` | TEXT | NOT NULL, DEFAULT 'user' | 'user' \| 'assistant' \| 'system' |
| `created_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | UTC |

**Индексы:**
- `idx_task_events_task_id` (task_id, created_at)
- `idx_task_events_type` (type)

**Payload примеры:**
```json
// created
{"source": "user", "initial_priority": "high"}

// updated
{"field": "priority", "old": "medium", "new": "high"}

// rescheduled
{"field": "due_at", "old": "2026-11-10T18:00:00Z", "new": "2026-11-12T09:00:00Z"}

// converted_from_inbox
{"inbox_item_id": 42, "inbox_text": "Купить молоко"}
```

---

### 4. Conversation (Диалог)

**Назначение:** Сессия общения с ассистентом.

| Поле | Тип | Ограничения | Описание |
|------|-----|-------------|----------|
| `id` | INTEGER | PK, AUTOINCREMENT | |
| `title` | TEXT | NULLABLE | Авто-генерация из первого сообщения |
| `context_screen` | TEXT | NULLABLE | Экран пользователя на момент старта |
| `created_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | |
| `updated_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | |
| `ended_at` | TIMESTAMP | NULLABLE | |

---

### 5. Message (Сообщение)

**Назначение:** Отдельное сообщение в диалоге.

| Поле | Тип | Ограничения | Описание |
|------|-----|-------------|----------|
| `id` | INTEGER | PK, AUTOINCREMENT | |
| `conversation_id` | INTEGER | NOT NULL, FK → conversations(id) | |
| `role` | TEXT | NOT NULL, CHECK IN ('user','assistant','tool') | |
| `content` | TEXT | NOT NULL | Текст сообщения |
| `tool_calls` | TEXT | NULLABLE, JSON | Для role=assistant: вызванные инструменты |
| `tool_results` | TEXT | NULLABLE, JSON | Для role=tool: результаты выполнения |
| `metadata` | TEXT | NULLABLE, JSON | Доп. данные (tokens, latency, model) |
| `created_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | |

**Индексы:**
- `idx_messages_conversation` (conversation_id, created_at)

---

### 6. PendingAction (Ожидающее подтверждение)

**Назначение:** Операции от ассистента, требующие явного подтверждения пользователя.

| Поле | Тип | Ограничения | Описание |
|------|-----|-------------|----------|
| `id` | INTEGER | PK, AUTOINCREMENT | |
| `conversation_id` | INTEGER | NOT NULL, FK → conversations(id) | |
| `message_id` | INTEGER | NOT NULL, FK → messages(id) | Сообщение-ассистент с предложением |
| `tool_name` | TEXT | NOT NULL | Имя инструмента (create_task, update_task, etc.) |
| `arguments` | TEXT | NOT NULL, JSON | Аргументы для инструмента |
| `preview` | TEXT | NOT NULL, JSON | Человекочитаемое превью результата |
| `status` | TEXT | NOT NULL, CHECK IN ('pending','confirmed','rejected','expired'), DEFAULT 'pending' | |
| `created_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | |
| `resolved_at` | TIMESTAMP | NULLABLE | |
| `resolution` | TEXT | NULLABLE | 'confirmed' \| 'rejected' \| 'expired' |

**Индексы:**
- `idx_pending_actions_conversation` (conversation_id, status)
- `idx_pending_actions_status` (status)

**Жизненный цикл:**
1. Ассистент предлагает действие → создаёт PendingAction (status=pending)
2. Пользователь подтверждает/отклоняет → статус меняется, выполняется/отменяется
3. TTL: 24 часа → expired

---

### 7. AppSettings (Настройки приложения)

**Назначение:** Key-value пользовательские настройки.

| Поле | Тип | Ограничения | Описание |
|------|-----|-------------|----------|
| `key` | TEXT | PK | Настройка (например: 'theme', 'default_priority', 'focus_duration') |
| `value` | TEXT | NOT NULL | JSON-значение |
| `updated_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | |

**Примеры ключей:**
```json
// theme
{"mode": "dark", "accent": "violet"}

// default_priority
{"priority": "medium"}

// focus_duration
{"minutes": 25}

// assistant
{"provider": "openrouter", "model": "openai/gpt-4o-mini", "temperature": 0.3}
```

---

## Работа с датами и часовыми поясами

### Правила (критично)

1. **Точные моменты времени** (`created_at`, `updated_at`, `completed_at`, `due_at`) — всегда **UTC** в БД.
2. **Календарные даты** (`planned_date`, `due_date`) — хранятся как `DATE` (без времени), **не смещаются** при смене TZ.
3. **timezone** на задаче — IANA TZ (по умолчанию `Europe/Moscow`). Используется для:
   - Интерпретации `planned_date` / `due_date` как "весь день в этой TZ"
   - Вычисления просроченности календарных дат
   - Отображения пользователю
4. **Никогда** не конвертировать `DATE` в timestamp с учётом TZ при сохранении/чтении.
5. Фронтенд получает `due_at` (UTC ISO8601) и `due_date` (YYYY-MM-DD) раздельно.

### Примеры

```sql
-- Задача на 10 ноября 2026 (весь день в Москве)
INSERT INTO tasks (title, due_date, timezone) VALUES ('Дедлайн', '2026-11-10', 'Europe/Moscow');

-- Задача с точным дедлайном 10 ноября 18:00 по Москве
INSERT INTO tasks (title, due_at, timezone) VALUES ('Встреча', '2026-11-10T15:00:00Z', 'Europe/Moscow');
-- due_at хранится в UTC (15:00Z = 18:00 MSK)
```

### Go типы

```go
type Task struct {
    ID            int64
    Title         string
    Note          sql.NullString
    Priority      Priority     // low | medium | high
    Status        TaskStatus   // open | completed | cancelled
    PlannedDate   *time.Time   // DATE (local midnight in task TZ)
    DueDate       *time.Time   // DATE
    DueAt         *time.Time   // UTC timestamp
    Timezone      string       // IANA TZ
    CreatedAt     time.Time    // UTC
    UpdatedAt     time.Time    // UTC
    CompletedAt   *time.Time   // UTC
    CancelledAt   *time.Time   // UTC
}
```

---

## Статусы задач

| Статус | Описание | Переходы |
|--------|----------|----------|
| `open` | Активная, требует внимания | → `completed`, → `cancelled` |
| `completed` | Выполнена пользователем | → `open` (восстановление) |
| `cancelled` | Отменена, неактуальна | → `open` (восстановление) |

**Просроченность** — вычисляемое свойство, не статус. Используется для фильтров и UI.

---

## Связи

```
Task 1 ──< TaskEvent
Task 1 ──< Task (parent_task_id, для будущих подзадач)
InboxItem 1 ──> Task (при конвертации, через TaskEvent.payload)
Conversation 1 ──< Message
Conversation 1 ──< PendingAction
Message 1 ──< PendingAction
```

---

## Миграции (план)

| Версия | Описание |
|--------|----------|
| 001 | Создание таблиц: tasks, inbox_items, task_events, conversations, messages, pending_actions, app_settings |
| 002 | Индексы |
| 003 | Триггер updated_at для tasks, messages |
| 004 | Проверочные ограничения (CHECK) |

> Детальные SQL миграции создаются на этапе реализации Task Engine.

---

## Открытые вопросы (требуют решения)

1. **Подзадачи** — включать `parent_task_id` в v1 или отложить до v2?
   - *Вариант А:* Включить колонку, не использовать в UI v1
   - *Вариант Б:* Не включать, добавить миграцией позже

2. **Теги** — отдельная таблица `task_tags` (many-to-many) или JSON в задаче?
   - *Вариант А:* JSONB/TEXT в tasks (проще, достаточно для v1)
   - *Вариант Б:* Нормализованная таблица (гибче для поиска)

3. **Повторяющиеся задачи** — схема для recurrence rule (RRULE)?
   - *Решение:* Не в v1. Заложить колонку `recurrence_rule TEXT NULL` в tasks.

4. **Прикрепления** — таблица `attachments` или внешние ссылки?
   - *Решение:* v1 — только внешние ссылки в `note` или отдельное поле `external_urls JSON`.

5. **Мягкое удаление** — `deleted_at` везде или hard delete + event?
   - *Вариант А:* `deleted_at` + фильтр во всех запросах (безопаснее)
   - *Вариант Б:* Hard delete, история в `task_events` (проще)

> **Правило:** Не принимай необратимые решения. Зафиксируй варианты здесь, предоставь выбор.