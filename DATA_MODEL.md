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
| `estimate_minutes` | INTEGER | NULLABLE, CHECK 5-1440 | Сколько длится работа; без оценки планировщик берёт значение по умолчанию |
| `expected_result` | TEXT | NULLABLE, max 500 | Что означает «готово» для этой задачи |
| `repeat` | TEXT | NULLABLE, JSON max 500 | Правило повторения `{freq, interval, weekdays, until, window_days}` |
| `repeat_parent_id` | INTEGER | FK → tasks(id), NULLABLE | Вхождение, из которого создана эта задача |
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
| `kind` | TEXT | NOT NULL, CHECK IN ('task','idea','note','goal'), DEFAULT 'task' | Что именно записано |
| `revisit_at` | DATE | NULLABLE | Когда вернуть запись пользователю |
| `created_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | UTC |
| `processed_at` | TIMESTAMP | NULLABLE | UTC когда преобразован/удалён |

**Индексы:**
- `idx_inbox_created_at` (created_at DESC)
- `idx_inbox_kind` (kind, created_at DESC)
- `idx_inbox_revisit` (revisit_at)

**Валидация:**
- `text` не пустой, ≤ 2000 символов
- `meta` — валидный JSON если задан

**Жизненный цикл:**
1. `Capture` — создание; сразу задаётся `kind`; идея без даты получает `revisit_at = сегодня + 7 дней`
2. `ConvertToTask` → создаёт Task, ставит `processed_at` и `converted_task_id`, сохраняет InboxItem
3. `Update` — меняет `kind`/`revisit_at` до обработки; идея может стать задачей, но не обязана
4. `Dismiss` — удаляет InboxItem

---

### 2.1 TaskBlock (Зарезервированное время)

| Поле | Тип | Ограничения | Описание |
|------|-----|-------------|----------|
| `id` | INTEGER | PK, AUTOINCREMENT | |
| `task_id` | INTEGER | NOT NULL, FK → tasks(id) ON DELETE CASCADE | Задача, на которую зарезервировано время |
| `starts_at` | TIMESTAMP | NOT NULL | UTC Unix seconds |
| `ends_at` | TIMESTAMP | NOT NULL, CHECK > starts_at | UTC Unix seconds |
| `status` | TEXT | NOT NULL, CHECK IN ('planned','active','done','skipped') | |
| `source` | TEXT | NOT NULL, CHECK IN ('auto','manual','assistant'), DEFAULT 'auto' | Только `auto` может быть заменён планировщиком |
| `created_at`, `updated_at`, `version` | | | Как у остальных агрегатов |

**Индексы:** `idx_task_blocks_range` (starts_at, ends_at), `idx_task_blocks_task` (task_id, starts_at)

**Правило:** блок — это собственное рабочее время пользователя, а не встреча. Существующие встречи и блоки `manual` планировщик не двигает; пересечение с ними при записи плана — ошибка `409`.

---

### 2.2 TaskReminder (Напоминание)

| Поле | Тип | Ограничения | Описание |
|------|-----|-------------|----------|
| `id` | INTEGER | PK, AUTOINCREMENT | |
| `task_id` | INTEGER | NOT NULL, FK → tasks(id) ON DELETE CASCADE | |
| `fire_at` | TIMESTAMP | NOT NULL | UTC момент отправки |
| `kind` | TEXT | NOT NULL, CHECK IN ('lead','due','overdue') | Предупреждение, срок, после срока |
| `status` | TEXT | NOT NULL, CHECK IN ('pending','sent','dismissed'), DEFAULT 'pending' | `pending` — момент наступил и пользователь ещё не подтвердил; `sent` — показано и подтверждено |
| `sent_at` | TIMESTAMP | NULLABLE | Когда пользователь подтвердил показ |
| `notified_at` | TIMESTAMP | NULLABLE | Когда живое событие было опубликовано (ровно один раз) |

**Индекс:** `idx_task_reminders_unique` (task_id, fire_at, kind) — дубликаты невозможны; `idx_task_reminders_pending` (status, fire_at).

**Правило:** напоминания пересчитываются при каждом изменении задачи, подтверждённые сохраняются как история, ожидающие заменяются. Не более трёх на задачу, только будущие моменты, у завершённой задачи — ничего.

**Доставка:** публикация в SSE — вспомогательный канал; факт доставки фиксирует только подтверждение клиента (`sent_at`). Поэтому напоминание переживает перезапуск приложения и закрытую вкладку: клиент получает его из `GET /api/v1/reminders` при следующем подключении. Сбой пересчёта лечится проходом `ReconcileReminders`, который возвращает расписание задачам, оставшимся без напоминаний.

---

### 2.3 CompletionCheck (Проверка выполнения)

| Поле | Тип | Ограничения | Описание |
|------|-----|-------------|----------|
| `id` | INTEGER | PK, AUTOINCREMENT | |
| `task_id` | INTEGER | NOT NULL, FK → tasks(id) ON DELETE CASCADE | |
| `verdict` | TEXT | NOT NULL, CHECK IN ('confirmed','insufficient','contradictory','already_completed') | Заключение проверки |
| `confidence` | REAL | NOT NULL, 0–1 | Уверенность |
| `reason` | TEXT | NOT NULL | Объяснение для пользователя |
| `evidence` | TEXT | NULLABLE, JSON | Найденные основания: источник, ссылка, фрагмент |
| `sources` | TEXT | NULLABLE, JSON | Какие источники проверялись и какие были недоступны |
| `decision` | TEXT | NOT NULL, CHECK IN ('none','accepted','declined'), DEFAULT 'none' | Решение пользователя |
| `decided_at` | TIMESTAMP | NULLABLE | Когда решение принято |
| `created_at` | TIMESTAMP | NOT NULL | UTC |

**Индекс:** `idx_completion_checks_task` (task_id, created_at DESC).

**Правило:** проверка никогда не меняет статус задачи сама. Основание хранится вместе с заключением, чтобы вывод можно было пересмотреть. `declined` запоминается и блокирует автоматическое завершение этой задачи.

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
| `kind` | TEXT | NOT NULL, DEFAULT 'main' | `main` — главный чат; `project` — рабочее пространство |
| `purpose` | TEXT | NULLABLE | Тема рабочего пространства (для kind=project) |
| `source_conversation_id` | INTEGER | NULLABLE | Главный чат, из которого тема вынесена в пространство |
| `created_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | |
| `updated_at` | TIMESTAMP | NOT NULL, DEFAULT (unixepoch()) | |
| `ended_at` | TIMESTAMP | NULLABLE | |

**Индексы:**
- `idx_conversations_kind` (kind, updated_at DESC, id DESC)

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

### 8. CalendarEvent (Событие календаря)

| Поле | Тип | Правила |
|---|---|---|
| `id` | int64 | PK |
| `title` | string | 1–200 символов |
| `description` | string? | ≤ 2000 |
| `location` | string? | ≤ 200 |
| `calendar` | string | `work`, `meetings`, `personal` |
| `starts_at` | timestamp | UTC |
| `ends_at` | timestamp | UTC, строго позже `starts_at`, ≤ 31 дня |
| `all_day` | bool | событие на весь день |
| `timezone` | string | IANA, для отображения |
| `created_at`, `updated_at` | timestamp | UTC |
| `version` | int | optimistic locking |

Таблица `calendar_events`. Задачи в календарь не копируются: read-model объединяет события и задачи.

### Вложения сообщений

`messages.attachments` — JSON-массив `{kind, data}` (`task`, `task_list`, `event`, `event_list`). Заполняется из read-инструментов ассистента. Старые сообщения имеют `NULL`.

### Профиль

Хранится в `app_settings` под ключами `profile.name` и `profile.role`; инициалы выводятся из имени. Отдельной таблицы нет — приложение однопользовательское.

### 8.1 MemoryEntry (Запись памяти)

**Назначение:** устойчивое сведение о пользователе или материал знаний. Отдельно
от истории диалогов и задач.

| Поле | Тип | Правила |
|---|---|---|
| `id` | INTEGER | PK |
| `area` | TEXT | `personal`, `knowledge`; выводится из категории |
| `category` | TEXT | `preference`, `habit`, `circumstance`, `fact`, `agreement`, `context`, `rule` (личное); `instruction`, `summary`, `reference`, `research`, `idea`, `documentation`, `note` (знания) |
| `title` | TEXT | 1–200 символов |
| `normalized_title` | TEXT | тема без регистра и пунктуации, для поиска той же темы |
| `content` | TEXT | 1–100000 символов |
| `dedupe_key` | TEXT | SHA-256 от области, категории, темы и содержания |
| `document_sha` | TEXT? | хеш исходного документа, если запись пришла из файла |
| `certainty` | TEXT | `fact`, `assumption`, `inference` — факты, предположения и выводы не смешиваются |
| `confidence` | REAL | 0–1 |
| `status` | TEXT | `active`, `disputed`, `superseded` |
| `superseded_by` | INTEGER? | FK → memory_entries; замена в цепочке версий |
| `entities` | TEXT | JSON-массив имён, проектов, мест |
| `created_at`, `updated_at` | TIMESTAMP | UTC |
| `version` | INTEGER | optimistic locking, как у задач |

**Индексы:** список (`area,status,updated_at`), тема (`area,category,normalized_title,status`),
дубли (`dedupe_key`), замена (`superseded_by`).

**Правила:**

- точный дубль не создаёт запись, а добавляет `memory_sources`;
- исправление той же темы допускается автоматически только при `certainty = fact`
  и высокой уверенности; иначе запись становится `disputed` и создаётся
  `memory_conflicts`;
- `superseded` не попадает в активный список и в контекст ассистента, но
  остаётся читаемым как история;
- удаление — физическое, вместе с источниками, связями и противоречиями.

### 8.2 MemorySource (Происхождение)

| Поле | Тип | Правила |
|---|---|---|
| `id` | INTEGER | PK |
| `entry_id` | INTEGER | FK → memory_entries, cascade |
| `kind` | TEXT | `user`, `assistant`, `conversation`, `document`, `task`, `event`, `system`, `web` |
| `source_id` | INTEGER | ID объекта-источника, 0 если его нет |
| `label` | TEXT | человекочитаемое описание источника |
| `excerpt` | TEXT | фрагмент, на котором основано сведение (≤ 2000) |
| `observed_at` | TIMESTAMP | когда источник наблюдался |

Запись без источника невозможна: `Entry.Validate` отклоняет её.

### 8.3 MemoryLink (Связь)

| Поле | Тип | Правила |
|---|---|---|
| `id` | INTEGER | PK |
| `entry_id` | INTEGER | FK → memory_entries, cascade |
| `target_type` | TEXT | `memory`, `task`, `event`, `conversation`, `document`, `inbox`, `goal` |
| `target_id` | INTEGER | ID объекта |
| `relation` | TEXT | смысл связи (≤ 50), например «относится к», «источник» |
| `created_at` | TIMESTAMP | UTC |

Уникальность `(entry_id, target_type, target_id, relation)`. Существование цели
проверяется в той же транзакции, что и вставка.

### 8.4 MemoryConflict (Противоречие)

| Поле | Тип | Правила |
|---|---|---|
| `id` | INTEGER | PK |
| `existing_id`, `candidate_id` | INTEGER | FK → memory_entries |
| `reason` | TEXT | почему нужно решение |
| `status` | TEXT | `pending`, `resolved` |
| `resolution` | TEXT? | `existing`, `candidate`, `both`, `merge` |
| `created_at`, `resolved_at` | TIMESTAMP | UTC |

Один незавершённый конфликт на пару записей (`UNIQUE ... WHERE status = 'pending'`).
Решение применяется атомарно: выбранная версия остаётся активной, проигравшая
становится `superseded` со ссылкой на замену.

### 8.5 Memory FTS

`memory_fts` — виртуальная таблица `FTS5` (`content='memory_entries'`,
`unicode61 remove_diacritics 2`) с триггерами на вставку, обновление и удаление.
Индекс покрывает тему, содержание и сущности; поиск использует префиксное
совпадение по последнему слову и экранирует пользовательский ввод.

### 9. Document (Документ)

| Поле | Тип | Правила |
|---|---|---|
| `id` | int64 | PK |
| `filename` | string | исходное имя |
| `mime` | string | MIME |
| `size` | int64 | размер, ≤ 25 МиБ |
| `sha256` | string | хеш содержимого |
| `path` | string | файл в `LIFEOS_DOCUMENTS_DIR` |
| `status` | string | `pending`, `processing`, `processed`, `failed` |
| `error` | string? | причина ошибки |
| `extracted_text` | text? | извлечённый текст |
| `created_at`, `updated_at` | timestamp | UTC |

### 10. DocumentProposedTask (Предложенная задача)

| Поле | Тип | Правила |
|---|---|---|
| `id` | int64 | PK |
| `document_id` | int64 | FK → documents, cascade |
| `title`, `note` | string | как у задачи |
| `due_date` | date? | опционально |
| `priority` | string | low/medium/high |
| `confidence` | float | 0–1 |
| `source` | string? | цитата из документа |
| `status` | string | `proposed`, `accepted`, `dismissed` |
| `task_id` | int64? | FK → tasks после принятия |

Таблицы `documents`, `document_tasks` (миграция 014). Исходные файлы хранятся вне БД; удаление документа удаляет файл и предложения, но не созданные задачи.

### 11. Goal (Долгосрочная цель)

| Поле | Тип | Правила |
|---|---|---|
| `id` | int64 | PK |
| `title` | string | 1–200 символов |
| `description` | string | ≤ 5000 символов |
| `status` | string | `proposed`, `active`, `paused`, `achieved`, `abandoned` |
| `target_date` | date? | желаемый срок |
| `approved_at` | timestamp? | момент перехода `proposed` → `active` |
| `created_at`, `updated_at`, `version` | | как у задач, optimistic locking |

Таблица `goals` (миграция 020).

### 11.1 GoalMilestone (Этап плана)

| Поле | Тип | Правила |
|---|---|---|
| `id` | int64 | PK |
| `goal_id` | int64 | FK → goals, cascade |
| `position` | int | порядок в плане |
| `title` | string | 1–200 символов |
| `criteria` | string | чем измеряется выполнение |
| `status` | string | `pending`, `in_progress`, `done`, `skipped` |
| `due_date` | date? | срок этапа |
| `completed_at` | timestamp? | фиксируется при `done`/`skipped` |

### 11.2 GoalLink (Связь цели)

`goal_links`: `goal_id` + `target_type` (`conversation`, `task`, `event`, `document`, `memory`, `inbox`) + `target_id` + `relation`; уникальность четверки; цель проверяется в той же транзакции.

### 11.3 GoalProgress (Журнал прогресса)

`goal_progress`: append-only записи `kind` (`goal_created`, `approved`, `milestone`, `task_done`, `note`, `status`, `proposal`) с кратким summary, ссылкой на источник и временем; по ним считаются прогресс и сигнал «цель стоит» (нет записей > 14 дней).

### 11.4 GoalProposal (Предложение по плану)

`goal_proposals`: `summary` (объяснение от ассистента), `payload` (JSON: title, description, target_date, add/complete/skip/reopen/remove milestones), `status` (`pending`, `applied`, `rejected`), `created_at`, `decided_at`. Применение выполняется атомарно одной транзакцией; одновременно может быть только одно `pending`-предложение на цель.

## Автоматизации

### 12. Automation (Автоматизация)

| Поле | Тип | Правила |
|---|---|---|
| `id` | int64 | PK |
| `title` | string | 1–200 символов |
| `description` | string | ≤ 5000 символов |
| `status` | string | `proposed`, `active`, `paused`, `archived`; `archived` терминален |
| `trigger_kind` | string | `schedule`, `event`, `condition` |
| `trigger_json` | JSON | тело триггера того же вида, что и в API (см. `API_CONTRACT.md` 8.3) |
| `steps_json` | JSON | сценарий: шаги `name` + `arguments` (JSON) + `description`; 1–20 шагов |
| `expected_result` | string | что должен производить запуск |
| `consequences` | string | эффекты и риски, названные при проектировании |
| `approved_at` | timestamp? | момент включения выполнения |
| `last_run_at` | timestamp? | последний фактический запуск |
| `next_run_at` | timestamp? | durable-закладка расписания; `NULL` для event/condition и как маркер «пересчитать» |
| `created_at`, `updated_at`, `version` | | как у задач, optimistic locking |

Таблица `automations` (миграция 026). Сценарий выполняется только в статусе
`active`; правка триггера/шагов включённой автоматизации запрещена на уровне
Application (сначала пауза).

### 12.1 AutomationRun (Запуск автоматизации)

| Поле | Тип | Правила |
|---|---|---|
| `id` | int64 | PK |
| `automation_id` | int64 | FK → automations, cascade |
| `trigger` | string | источник запуска: `schedule`, `event`, `condition`, `manual` |
| `status` | string | `running` → `succeeded` / `failed` / `skipped` |
| `steps_json` | JSON | пошаговый журнал: статус шага, вывод (обрезан до 4000 символов), ошибка, длительность |
| `summary` | string | краткий итог по-человечески |
| `error` | string | причина остановки сценария с номером шага |
| `started_at` | timestamp | момент создания запуска |
| `finished_at` | timestamp? | только у завершённого запуска |

Таблица `automation_runs` (миграция 026). Один незавершённый запуск на
автоматизацию: вставка running-строки атомарно отказывается, если такой уже
есть, поэтому два прохода раннера не выполняют сценарий параллельно.

## Исследования и внешние источники

### 13. ResearchReport (Исследование)

| Поле | Тип | Правила |
|---|---|---|
| `id` | INTEGER | PK |
| `question` | TEXT | вопрос, 1–500 символов |
| `status` | TEXT | `running`, `completed`, `failed` |
| `queries_json` | JSON | поисковые запросы, не больше трёх |
| `summary` | TEXT | итог в Markdown: подтверждённое, одиночные источники, расхождения, как проверялось |
| `findings_json` | JSON | выводы: `statement`, `certainty`, `confidence`, `corroboration`, `domains`, `citations[]` (`url`, `title`, `site`, `excerpt`, `trust`, `read`, `read_unknown`), `conflict`. `read_unknown` — цитата старого формата: статус чтения неизвестен и не приравнивается к «не прочитана» |
| `sources_count` | INTEGER | число разных страниц, на которые опирается хотя бы один вывод |
| `knowledge_entry_id` | INTEGER? | FK → memory_entries (set null при удалении записи) |
| `error` | TEXT | причина сбоя или предупреждение (например, запись в память не удалась) |
| `started_at`, `finished_at` | TIMESTAMP | UTC |
| `version` | INTEGER | optimistic locking |

Таблица `research_reports` (миграция 027). Доказательства хранятся одним JSON-
столбцом: отчёт читается целиком, по выводам не ищут построчно.

**Уровни достоверности.** `fact` — утверждение подтверждают минимум два разных
регистрируемых сайта, и хотя бы одна страница была прочитана (`read`); два
поддомена одного издателя — это один сайт. `assumption` — один источник, только
поисковые выдержки или расхождение с другим источником; `inference` — вывод
самой LifeOS. Расхождение указывается в `conflict` и цитирует вторую сторону;
такой вывод никогда не становится фактом, а утверждения с разными значениями
(люди, организации, годы, суммы) не объединяются вовсе.

### 14. ResearchConnection (Внешний источник)

| Поле | Тип | Правила |
|---|---|---|
| `id` | INTEGER | PK |
| `name` | TEXT | 1–200 символов |
| `kind` | TEXT | `web_search`, `http_api`, `mcp`, `script` |
| `mode` | TEXT | `read`, `write`, `sync`; у `script` допускается только `read` |
| `trust` | TEXT | `high`, `normal`, `low` — вес источника при сопоставлении |
| `format` | TEXT | для `web_search`: `searxng` или `duckduckgo` |
| `endpoint` | TEXT | шаблон поиска с `{query}`, базовый URL API, адрес MCP или команда |
| `notes` | TEXT | описание источника |
| `enabled` | INTEGER | выключенный источник нельзя использовать |
| `allowed_tools` | JSON | инструменты MCP, которые пользователь разрешил без изменения данных; список ведёт только пользователь, ассистент его не меняет |
| `created_by_assistant` | INTEGER | источник, подключённый ассистентом: только `read` и выключен |
| `last_used_at` | TIMESTAMP? | последнее успешное обращение |
| `created_at`, `updated_at`, `version` | | optimistic locking |

Таблица `research_connections` (миграция 029 добавляет `allowed_tools`). Режим
проверяется при каждом обращении: `read` отказывает в изменяющем запросе, журнал
фиксирует отказ. Инструмент MCP без заявленного намерения записи выполняется
только при двух условиях: он в `allowed_tools` **и** сервер пометил его
`readOnlyHint`; пометка сервера — его заявление, а не гарантия.

### 15. ResearchWatch (Наблюдение)

| Поле | Тип | Правила |
|---|---|---|
| `id` | INTEGER | PK |
| `topic` | TEXT | тема по-человечески, 1–200 символов |
| `query`, `queries_json` | TEXT/JSON | поисковые запросы наблюдения (до пяти) |
| `cadence` | TEXT | `daily`, `weekly`, `manual` |
| `enabled` | INTEGER | выключенное наблюдение не проверяется |
| `next_check_at` | TIMESTAMP? | закладка следующей проверки; `NULL` у ручного |
| `last_checked_at`, `last_change_at` | TIMESTAMP? | когда проверяли и когда изменилось |
| `last_change_summary` | TEXT | «новых источников: N, обновлено: M» |
| `seen_json` | JSON | база отпечатков: `url`, `title`, `hash`, `first_seen`; не больше 200 записей |
| `created_at`, `updated_at`, `version` | | optimistic locking |

Таблица `research_watches`. Отпечаток — первые 4000 символов текста страницы,
свёрнутые и захешированные: так изменение текста отличается от перестановки
разметки.

### 16. ResearchCall (Журнал обращений)

`research_calls`: `connection_id`, `name`, `kind`, `mode`, `method`,
`operation`, `outcome`, `detail`, `duration_ms`, `created_at`.

`outcome`:

| Значение | Смысл |
|---|---|
| `started` | вызов записан до отправки, результат ещё неизвестен |
| `ok` | вызов выполнен |
| `refused` | отказ по разрешениям источника, списку разрешённых инструментов или по аннотации MCP-инструмента |
| `failed` | вызов не удался (сеть, ответ сервиса) |
| `unknown` | LifeOS перезапустили во время вызова: сервис мог принять запрос |

`method` хранится отдельно от `operation`, потому что у MCP `operation` — имя
инструмента, а не HTTP-метод; по `method` видно, был вызов чтением или
удалением. Журнал пишется на каждое обращение **до его отправки**, включая отказ
по режиму доступа
и сбой сети, а для изменяющих вызовов — до отправки. `connection_id` не
является внешним ключом: журнал переживает удаление источника и продолжает
объяснять, что с ним делали.

### 17. Skill (Навык и самоулучшение)

`skills`: `id`, `name`, `name_lower` (UNIQUE, регистронезависимая уникальность),
`purpose`, `when_to_use`, `instructions`, `limitations`, `examples`, `tags`
(JSON-массив), `status` (`draft`, `active`, `disabled`, `archived` — CHECK),
`current_version`, `version` (optimistic locking, пишется Application),
`created_by_assistant` (INTEGER 0/1), `use_count`, `good_count`, `bad_count`
(сводные счётчики, их поддерживает репозиторий), `last_used_at`,
`created_at`, `updated_at`. Индекс по `status`.

`skill_versions`: `id`, `skill_id` (FK → `skills`, CASCADE), `version_no`,
`name`, `purpose`, `when_to_use`, `instructions`, `limitations`, `examples`,
`tags`, `change_note`, `created_by` (`user`/`assistant`) , `created_at`.
Уникальность `(skill_id, version_no)`. Создание навыка и первая версия —
одна транзакция; правка и откат добавляют запись.

`skill_proposals`: `id`, `skill_id` (FK, NULL для рабочих инструкций —
частичный индекс `skill_id IS NULL`), `kind` (`skill`/`working_instructions`),
`title`, `problem`, `evidence` (JSON-массив), `proposed_instructions`,
`diff` (JSON-массив строк), `expected_effect`, `test_result`, `base_version`,
`status` (`proposed`/`applied`/`rejected` — CHECK), `created_at`,
`resolved_at`, `resolved_by`.

`skill_feedback`: `id`, `skill_id` (FK, CASCADE), `rating` (`good`/`bad` —
CHECK), `comment`, `conversation_id`, `created_at`.

`skill_usages`: `id`, `skill_id` (FK, CASCADE), `conversation_id`,
`query_excerpt` (≤300 символов), `source` (`query`/`tool` — CHECK),
`applied_at`. Журнал переживает архивирование навыка.

Миграция 030 создаёт все пять таблиц и индексы; переход `archived → draft`
проверяется доменом, удаление — только черновика/архива.

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
MemoryEntry 1 ──< MemorySource
MemoryEntry 1 ──< MemoryLink >── Task / Event / Conversation / Document / Memory / Goal
MemoryEntry 1 ──< MemoryConflict
Task 1 ──< TaskEvent
Task 1 ──< Task (parent_task_id, для будущих подзадач)
InboxItem 1 ──> Task (при конвертации, через TaskEvent.payload)
Conversation 1 ──< Message
Conversation 1 ──< PendingAction
Message 1 ──< PendingAction
Goal 1 ──< GoalMilestone
Goal 1 ──< GoalLink >── Conversation / Task / Event / Document / Memory / InboxItem
Goal 1 ──< GoalProgress
Goal 1 ──< GoalProposal
Automation 1 ──< AutomationRun
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
| 018 | Память и база знаний: `memory_entries`, `memory_sources`, `memory_links`, `memory_conflicts`, `memory_fts` с триггерами |
| 019 | Рабочие пространства: `conversations.kind`, `purpose`, `source_conversation_id`, индекс `idx_conversations_kind` |
| 020 | Долгосрочные цели: `goals`, `goal_milestones`, `goal_links`, `goal_progress`, `goal_proposals` с CHECK-ограничениями статусов и каскадным удалением |
| 021 | `event_sequence`: долговечный счётчик ID событий SSE, резервируемый блоками |
| 022 | Колонка `event_sequence.epoch`: эпоха диапазона ID |
| 023 | Инициатива: `outreach` с исходами реакции и окном тишины |
| 024–025 | Маршрутизация источников рабочих пространств и её восстановление |
| 026 | Автоматизации: `automations`, `automation_runs` с CHECK-ограничениями статусов, частичным индексом running-запусков и каскадным удалением |
| 027 | Исследования: `research_reports`, `research_connections`, `research_watches`, `research_calls`; перестройка `memory_sources` ради вида источника `web` |
| 028 | Журнал внешних обращений: колонка `method` и исходы `started`/`unknown` (перестройка таблицы ради нового CHECK), индекс по `outcome` |
| 029 | `research_connections.allowed_tools`: инструменты MCP, разрешённые пользователем для чтения |
| 030 | Навыки: `skills` (CHECK статусов, UNIQUE `name_lower`), `skill_versions`, `skill_proposals`, `skill_feedback`, `skill_usages` с каскадным удалением |

### Идентификаторы событий

`event_sequence` — одна строка (`id = 1`) с текущим потолком выданных ID и текущей
эпохой. Процесс при старте резервирует блок (`4096`) и раздаёт ID из него в памяти;
брокер продлевает блок до его исчерпания, поэтому выдача никогда не выходит за
пределы зарезервированного. Резервирование переживает перезапуск и падение, поэтому
ID не повторяются. Это требование SSE: браузер присылает `Last-Event-ID`, и счётчик,
начинающийся заново с единицы, сделал бы все новые события «старее» позиции клиента.

Эпоха генерируется заново при каждом резервировании. SSE-идентификатор имеет вид
`epoch.seq`; клиент с ID из другой эпохи — например, после восстановления базы из
старой копии — получает `resync`, а не частичный replay, который выглядел бы
непрерывным, но пропускал потерянное окно. Ошибка резервирования блокирует запуск
приложения: без долговечного диапазона поток не может гарантировать уникальность ID.

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