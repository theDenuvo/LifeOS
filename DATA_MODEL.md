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
| `created_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | UTC момент создания (Unix seconds) |
| `updated_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | UTC последнего изменения (Unix seconds) |
| `completed_at` | TIMESTAMP | NULLABLE | UTC момент завершения (Unix seconds) |
| `cancelled_at` | TIMESTAMP | NULLABLE | UTC момент отмены (Unix seconds) |
| `parent_task_id` | INTEGER | FK → tasks(id), NULLABLE | Для будущих подзадач (v1: не используется) |
| `version` | INTEGER | NOT NULL, DEFAULT 1, CHECK ≥ 1 | Метаданные optimistic locking; не поле домена |

**Индексы:**
- `idx_tasks_status` (status)
- `idx_tasks_priority` (priority)
- `idx_tasks_planned_date` (planned_date)
- `idx_tasks_due_date` (due_date)
- `idx_tasks_due_at` (due_at)
- `idx_tasks_created_at` (created_at)

**Валидация (Domain):**
- `title` не пустой, ≤ 500 символов
- `note` ≤ 5000 символов
- `priority` ∈ {low, medium, high}
- `status` ∈ {open, completed, cancelled}
- Переходы статусов: `open → completed`, `open → cancelled`, `completed → open`, `cancelled → open`
- `planned_date` и `due_date` — реальные календарные даты (год 1–9999, месяц 1–12, день в пределах месяца с учётом високосных лет); zero value означает «дата не задана»
- `timezone` — валидная IANA TZ
- **Прошедший дедлайн не является ошибкой.** `due_at` и `due_date` могут быть в прошлом как при создании, так и при обновлении; просроченность вычисляется через `IsOverdue`
- Ограничения длины строк измеряются в **Unicode code points** (`utf8.RuneCountInString`), не в байтах

**Просроченность (вычисляется, не хранится):**

`OverdueAt()` возвращает момент, начиная с которого задача считается просроченной:
- если задан `due_at` — это ровно `due_at`;
- если задан только `due_date` — это **начало следующего календарного дня** (локальная полночь) в таймзоне задачи, вычисленное календарной арифметикой (учитывает DST: локальный день может длиться 23 или 25 часов);
- если дедлайна нет — `nil`.

`IsOverdue(now)` использует **разные сравнения для разных типов дедлайна**:

| Тип дедлайна | Сравнение | Ровно в момент `OverdueAt()` |
|---|---|---|
| `due_at` (точный) | строгое: `now.After(due_at)` | ещё **не** просрочена |
| `due_date` (календарный) | включительное: `now >= OverdueAt()` | уже **просрочена** |

Пояснение: точный дедлайн — это момент, после которого задача считается просроченной, поэтому равенство ещё укладывается в срок. Календарный дедлайн разрешает весь локальный день целиком, поэтому его верхняя граница (следующая локальная полночь) уже находится за пределами допустимого дня.

Завершённые и отменённые задачи никогда не считаются просроченными. Приоритет `due_at` над `due_date` сохраняется: если задан `due_at`, `due_date` не участвует в расчёте.

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
2. `ConvertToTask` → создаёт Task, ставит `processed_at` и `converted_task_id`, сохраняет InboxItem
3. `Dismiss` — удаляет InboxItem

---

### 3. TaskEvent (Событие задачи)

**Назначение:** Append-only история изменений. Не Event Sourcing — просто аудит.

| Поле | Тип | Ограничения | Описание |
|------|-----|-------------|----------|
| `id` | INTEGER | PK, AUTOINCREMENT | |
| `task_id` | INTEGER | NOT NULL, FK → tasks(id) | |
| `type` | TEXT | NOT NULL, CHECK IN ('created','updated','completed','cancelled','rescheduled','restored','linked','converted_from_inbox') | Тип события |
| `payload` | TEXT | NOT NULL, JSON | Детали изменения (old/new values) |
| `actor` | TEXT | NOT NULL, DEFAULT 'user' | 'user' \| 'assistant' \| 'system' |
| `created_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | UTC |

**Индексы:**
- `idx_task_events_task_id` (task_id, created_at)
- `idx_task_events_type` (type)

**Payload — `updated` / `rescheduled` (audit diff):**

Каждая успешная мутация задач пишет фактические значения до и после. Формат
зависит только от числа изменённых полей.

Одно изменённое поле — документированная однополевая форма:

```json
{"field": "priority", "old": "medium", "new": "high"}
```

Несколько изменённых полей — массив `changes`:

```json
{
  "changes": [
    {"field": "priority", "old": "medium", "new": "high"},
    {"field": "due_date", "old": "2026-11-12", "new": null}
  ]
}
```

Правила:

- в payload попадают только реально изменённые поля;
- у каждого поля есть `old` и `new`;
- явная очистка поля представляется как `new: null`;
- `old`/`new` берутся из независимого снимка задачи до мутации и из итогового
  состояния, а не из входных параметров запроса.

Формат значений:

| Тип поля | Представление |
|---|---|
| `CalendarDate` (`planned_date`, `due_date`) | `YYYY-MM-DD`, иначе `null` |
| `time.Time` (`due_at`) | RFC3339Nano в UTC (сохраняет дробные секунды), иначе `null` |
| `Priority`, `Status` | строка (`low`/`medium`/`high`, `open`/...) |
| `Tags` | массив строк (пустой массив, если тегов нет) |
| `Note`, `Title`, `Timezone` | строка или `null` для отсутствующего значения |

`updated` и `rescheduled` используют одинаковую структуру; `rescheduled`
заполняет только `planned_date`, `due_date`, `due_at`.

**Payload примеры:**
```json
// created
{"source": "user", "initial_priority": "high"}

// updated
{"field": "priority", "old": "medium", "new": "high"}

// updated (несколько полей за одну операцию — partial update)
{"changes": [{"field": "priority", "old": "medium", "new": "high"}, {"field": "due_date", "old": "2026-11-12", "new": null}]}

// rescheduled
{"field": "due_at", "old": "2026-11-10T18:00:00Z", "new": "2026-11-12T09:00:00Z"}

// rescheduled (несколько полей)
{"changes": [{"field": "planned_date", "old": "2026-11-12", "new": "2026-11-13"}, {"field": "due_at", "old": "2026-11-12T18:00:00Z", "new": null}]}

// completed / cancelled / restored
{"status": "completed"}

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
-- due_at хранится в SQLite как Unix seconds (UTC)
INSERT INTO tasks (title, due_at, timezone) VALUES ('Встреча', 1762867200, 'Europe/Moscow');
-- 1762867200 = 2026-11-10T15:00:00Z = 2026-11-10 18:00 MSK
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

## Версионирование и временные метки

- `tasks.version` — concurrency metadata на границе Application/Repository. Чтение версию не меняет; каждая успешная мутация увеличивает её ровно на 1; запись условна по `expectedVersion` (`WHERE id = ? AND version = ?`). Устаревшая версия → `ErrVersionConflict`.
- `updated_at` пишет Application тем же значением часов, что и событие аудита. Триггер `tasks_updated_at` из миграции 008 удалён в 010: он перезаписывал `updated_at` часами БД и расходился с `task_events.created_at`.
- Временные метки хранятся как целые Unix-секунды. Production-часы (`SystemClock`) усекают время до секунды, чтобы записанное и прочитанное значение совпадали.

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
| 002 | Индексы для inbox_items |
| 003 | Таблица task_events |
| 004 | Таблица conversations |
| 005 | Таблица messages |
| 006 | Таблица pending_actions |
| 007 | Таблица app_settings |
| 008 | Триггеры updated_at для tasks, conversations |
| 009 | Защита append-only task_events, защита от удаления tasks с историей, converted_task_id для inbox_items |
| 010 | `tasks.version` для optimistic locking; удаление триггера `tasks_updated_at` (времена пишет Application) |

> Детальные SQL миграции в `internal/infrastructure/sqlite/migrations/`.

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