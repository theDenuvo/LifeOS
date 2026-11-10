# LifeOS — API_CONTRACT.md

## Обзор

REST API v1 для LifeOS. Базовый путь: `/api/v1`

- **Content-Type**: `application/json`
- **Ошибки**: `application/problem+json` (RFC 9457)
- **Даты**: ISO 8601 UTC (`2026-11-10T15:00:00Z`) для timestamp, `YYYY-MM-DD` для date
- **Часовой пояс**: По умолчанию `Europe/Moscow`, указывается в задаче
- **Пагинация**: `?limit=50&offset=0` (max limit 100)
- **Сортировка**: `?sort=-created_at` (префикс `-` = DESC)

---

## Общие схемы

### Problem Details (Ошибки)

```json
{
  "type": "https://lifeos.dev/errors/validation-failed",
  "title": "Validation Failed",
  "status": 400,
  "detail": "Request validation failed",
  "instance": "/api/v1/tasks",
  "errors": [
    {"field": "title", "code": "required", "message": "Title is required"},
    {"field": "priority", "code": "invalid", "message": "Priority must be low|medium|high"}
  ]
}
```

**Типы ошибок:**
| Type | HTTP | Когда |
|------|------|-------|
| `validation-failed` | 400 | Неверный запрос |
| `not-found` | 404 | Ресурс не найден |
| `conflict` | 409 | Конкурентное изменение (optimistic lock) |
| `precondition-failed` | 412 | Предусловие не выполнено (например, статус не позволяет) |
| `internal` | 500 | Внутренняя ошибка |

### Task (Схема)

```json
{
  "id": 123,
  "title": "Подготовить презентацию",
  "note": "Слайды 1-15 готовы",
  "priority": "high",
  "status": "open",
  "planned_date": "2026-11-10",
  "due_date": "2026-11-10",
  "due_at": "2026-11-10T15:00:00Z",
  "timezone": "Europe/Moscow",
  "tags": ["презентация", "совет"],
  "created_at": "2026-11-08T10:00:00Z",
  "updated_at": "2026-11-09T12:00:00Z",
  "completed_at": null,
  "cancelled_at": null,
  "overdue": false
}
```

### InboxItem (Схема)

```json
{
  "id": 42,
  "text": "Позвонить маме про воскресенье",
  "meta": {"source": "voice", "entities": ["phone", "date"]},
  "created_at": "2026-11-09T09:12:00Z",
  "processed_at": null
}
```

### TaskEvent (Схема)

```json
{
  "id": 1001,
  "task_id": 123,
  "type": "rescheduled",
  "payload": {"field": "due_at", "old": "2026-11-10T15:00:00Z", "new": "2026-11-12T09:00:00Z"},
  "actor": "assistant",
  "created_at": "2026-11-09T14:30:00Z"
}
```

### Summary (Главный экран)

```json
{
  "today": [
    {"id": 1, "title": "Задача 1", "time": "14:00", "priority": "high"},
    {"id": 2, "title": "Задача 2", "time": "—", "priority": "medium"}
  ],
  "overdue": [
    {"id": 6, "title": "Оплатить счета", "priority": "high", "days_overdue": 1}
  ],
  "high_priority": [
    {"id": 1, "title": "Презентация", "priority": "high"},
    {"id": 5, "title": "Встреча", "priority": "high"}
  ],
  "stats": {
    "active_total": 9,
    "overdue_count": 1,
    "high_priority_count": 3,
    "completed_today": 1,
    "inbox_count": 5
  },
  "focus_task": {"id": 1, "title": "Презентация", "project": "Работа", "time_estimate": "2ч"}
}
```

---

## Endpoints

### 1. Health Check

```
GET /healthz
```

**Response 200:**
```json
{"status": "ok", "version": "1.0.0", "time": "2026-11-10T10:00:00Z"}
```

---

### 2. Задачи

#### Список задач

```
GET /api/v1/tasks
```

**Параметры:**
| Параметр | Тип | Описание |
|----------|-----|----------|
| `status` | string | `open|completed|cancelled` (multiple: `status=open&status=completed`) |
| `priority` | string | `low|medium|high` |
| `planned_date` | date | Фильтр по дате планирования |
| `due_date` | date | Фильтр по календарному дедлайну |
| `overdue` | boolean | `true` — только просроченные |
| `search` | string | Полнотекстовый поиск по title/note |
| `tags` | string | Теги через запятую (AND) |
| `sort` | string | Поле: `created_at`, `updated_at`, `due_at`, `priority`, `title` (префикс `-` = DESC) |
| `limit` | int | 1-100, default 50 |
| `offset` | int | Default 0 |

**Response 200:**
```json
{
  "data": [Task, ...],
  "meta": {"limit": 50, "offset": 0, "total": 42}
}
```

#### Получить задачу

```
GET /api/v1/tasks/{id}
```

**Response 200:** Task
**Response 404:** ProblemDetails

#### Создать задачу

```
POST /api/v1/tasks
```

**Request:**
```json
{
  "title": "Новая задача",
  "note": "Описание",
  "priority": "medium",
  "planned_date": "2026-11-10",
  "due_date": "2026-11-12",
  "due_at": "2026-11-12T18:00:00Z",
  "timezone": "Europe/Moscow",
  "tags": ["тег1", "тег2"]
}
```

**Валидация:**
- `title` required, 1-500 chars
- `priority` ∈ {low,medium,high}, default medium
- `planned_date`, `due_date` — валидные DATE
- `due_at` — валидный RFC3339 UTC, ≥ now
- `timezone` — валидная IANA TZ, default Europe/Moscow
- `tags` — массив строк, каждая ≤ 50 chars

**Response 201:** Task (с заполненными `id`, `created_at`, `updated_at`, `status: "open"`)

**Response 400:** ValidationFailed

#### Изменить задачу (частичное обновление)

```
PATCH /api/v1/tasks/{id}
```

**Request:** Любые поля Task (кроме `id`, `created_at`, `completed_at`, `cancelled_at`, `overdue`)

**Валидация:**
- `status` переходит только по разрешённым переходам
- `due_at` ≥ now если меняется
- Optimistic locking через `If-Match: <updated_at>` или `ETag`

**Response 200:** Task (обновлённая)
**Response 404:** NotFound
**Response 409:** Conflict (версия изменилась)
**Response 412:** PreconditionFailed (недопустимый переход статуса)

#### Завершить задачу

```
POST /api/v1/tasks/{id}/complete
```

**Response 200:** Task (status=completed, completed_at=now)
**Response 404:** NotFound
**Response 412:** PreconditionFailed (status не open)

#### Отменить задачу

```
POST /api/v1/tasks/{id}/cancel
```

**Response 200:** Task (status=cancelled, cancelled_at=now)
**Response 404:** NotFound
**Response 412:** PreconditionFailed (status не open)

#### Восстановить задачу

```
POST /api/v1/tasks/{id}/restore
```

**Response 200:** Task (status=open, completed_at/cancelled_at=NULL)
**Response 404:** NotFound
**Response 412:** PreconditionFailed (status не completed/cancelled)

#### Перепланировать задачу

```
POST /api/v1/tasks/{id}/reschedule
```

**Request:**
```json
{
  "planned_date": "2026-11-15",
  "due_date": "2026-11-15",
  "due_at": "2026-11-15T10:00:00Z",
  "timezone": "Europe/Moscow"
}
```

**Response 200:** Task
**Response 400:** ValidationFailed
**Response 404:** NotFound
**Response 412:** PreconditionFailed (status не open)

---

### 3. Входящие

#### Список входящих

```
GET /api/v1/inbox
```

**Параметры:** `limit`, `offset`, `unprocessed_only` (boolean, default true)

**Response 200:**
```json
{"data": [InboxItem, ...], "meta": {"limit": 50, "offset": 0, "total": 12}}
```

#### Быстрый захват (Capture)

```
POST /api/v1/inbox
```

**Request:**
```json
{"text": "Мысль или задача", "meta": {"source": "widget"}}
```

**Response 201:** InboxItem

#### Преобразовать в задачу

```
POST /api/v1/inbox/{id}/convert
```

**Request (опционально):**
```json
{
  "priority": "high",
  "planned_date": "2026-11-10",
  "due_date": "2026-11-10"
}
```

**Response 201:** Task (созданная)
**Response 404:** NotFound (inbox item не найден или уже processed)

#### Удалить входящий (Dismiss)

```
DELETE /api/v1/inbox/{id}
```

**Response 204:** No Content
**Response 404:** NotFound

---

### 4. Сводка (Главный экран)

```
GET /api/v1/summary/today
```

**Response 200:** Summary (см. схему выше)

---

### 5. Ассистент

#### Отправить сообщение

```
POST /api/v1/assistant/messages
```

**Request:**
```json
{
  "content": "Создай задачу купить молоко на завтра",
  "conversation_id": 1
}
```

**Response 202 (Accepted):**
```json
{
  "message_id": 101,
  "conversation_id": 1,
  "status": "processing"
}
```

> Обработка асинхронна. Результат приходит через SSE или polling.

#### Получить историю диалога

```
GET /api/v1/assistant/conversations/{id}
```

**Response 200:**
```json
{
  "id": 1,
  "title": "Утренний планировщик",
  "messages": [Message, ...],
  "created_at": "...",
  "updated_at": "..."
}
```

#### Список диалогов

```
GET /api/v1/assistant/conversations
```

**Response 200:** `{ "data": [Conversation, ...] }`

#### Подтвердить/Отклонить ожидающее действие

```
POST /api/v1/assistant/pending-actions/{id}/confirm
POST /api/v1/assistant/pending-actions/{id}/reject
```

**Response 200:** PendingAction (resolved)

---

### 6. События (SSE)

```
GET /api/v1/events
```

**Headers:** `Accept: text/event-stream`, `Cache-Control: no-cache`

**Формат события:**
```
event: task.created
data: {"id": 123, "title": "Новая задача", ...}

event: task.updated
data: {"id": 123, "title": "Обновлённая", "priority": "high"}

event: task.completed
data: {"id": 123}

event: inbox.captured
data: {"id": 42, "text": "Новая мысль"}

event: inbox.converted
data: {"inbox_id": 42, "task_id": 123}

event: assistant.message
data: {"conversation_id": 1, "message_id": 101, "role": "assistant", "content": "Готово!"}

event: pending_action.created
data: {"id": 5, "tool": "create_task", "preview": {...}}
```

**Типы событий:**
- `task.created`, `task.updated`, `task.completed`, `task.cancelled`, `task.restored`, `task.rescheduled`
- `inbox.captured`, `inbox.converted`, `inbox.dismissed`
- `assistant.message` (user/assistant/tool)
- `pending_action.created`, `pending_action.resolved`

**Переподключение:** Клиент должен переподключаться с `Last-Event-ID` для восстановления пропущенных событий.

---

### 7. Настройки

#### Получить настройку

```
GET /api/v1/settings/{key}
```

#### Установить настройку

```
PUT /api/v1/settings/{key}
```

**Request:** `{"value": <any JSON>}`

---

## Правила повторных запросов (Idempotency)

- `POST /api/v1/tasks` — идемпотентен по `Idempotency-Key` header (сохраняется 24ч)
- `POST /api/v1/inbox` — идемпотентен по `Idempotency-Key`
- `POST /api/v1/assistant/messages` — **не** идемпотентен (каждое сообщение новое)
- `PATCH /api/v1/tasks/{id}` — оптимистическая блокировка через `If-Match`

---

## Версионирование

- `/api/v1/` — стабильный контракт
- Breaking changes → `/api/v2/`
- Добавление новых полей в response — не breaking
- Добавление новых optional параметров — не breaking
- Удаление/переименование полей — breaking

---

## OpenAPI

Полная спецификация в `api/openapi.yaml` (OpenAPI 3.1).

Генерация:
- Go: `oapi-codegen -generate types,server,spec -package api api/openapi.yaml`
- TypeScript: `openapi-typescript api/openapi.yaml -o web/src/lib/api/types.ts`
- Client: `openapi-fetch` с сгенерированными типами