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
  "overdue": false,
  "version": 3
}
```

**Правило `version`:** `version` — токен optimistic locking. Он возвращается в теле и в заголовке `ETag` (`"3"`). Любая мутация требует `If-Match` с этим токеном; без него сервер отвечает `428 Precondition Required`, с устаревшим — `409 Conflict`.

**Правило `overdue`** (вычисляется на сервере, не хранится):

| Заданный дедлайн | Сравнение | Ровно в момент границы |
|---|---|---|
| `due_at` | строгое `now > due_at` | `overdue: false` |
| только `due_date` | включительное `now >= следующая локальная полночь` | `overdue: true` |

Для `due_date` допустим весь локальный календарный день; границей служит начало следующего локального дня в `timezone` задачи (учитывает DST). Если задан `due_at`, он имеет приоритет и `due_date` не участвует в расчёте. Задачи со статусом `completed`/`cancelled` никогда не считаются просроченными.

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
  "focus_task": {"id": 1, "title": "Презентация", "time_estimate": "2ч"}
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
- `note` ≤ 5000 chars
- `priority` ∈ {low,medium,high}, default medium
- `planned_date`, `due_date` — валидные календарные DATE (реальная дата, без «тихой» нормализации)
- `due_at` — валидный RFC3339 UTC
- `timezone` — валидная IANA TZ, default Europe/Moscow
- `tags` — массив строк, каждая ≤ 50 chars
- `due_at` / `due_date` в прошлом допустимы — это не ошибка валидации (см. `overdue`)
- Ограничения длины строк измеряются в Unicode code points

**Response 201:** Task (с заполненными `id`, `created_at`, `updated_at`, `status: "open"`)

**Response 400:** ValidationFailed

#### Изменить задачу (частичное обновление)

```
PATCH /api/v1/tasks/{id}
```

**Request:** Любые поля Task (кроме `id`, `created_at`, `completed_at`, `cancelled_at`, `overdue`)

**Валидация:**
- `status` переходит только по разрешённым переходам
- Optimistic locking через `If-Match: <version>` (значение `ETag` из последнего чтения). Заголовок обязателен: без него — `428`, при устаревшей версии — `409`
- `status` через `PATCH` не меняется: для переходов используйте `/complete`, `/cancel`, `/restore`

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
  "due_at": "2026-11-15T10:00:00Z"
}
```

`timezone` не принимается этим эндпоинтом — измените часовой пояс через `PATCH`. `null` очищает соответствующее поле.

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

**Параметры:** `limit`, `offset`, `unprocessed_only` (boolean, default true), `kind` (`task|idea|note|goal`), `revisit=due` (только записи, к которым пора вернуться)

**Response 200:**
```json
{"data": [InboxItem, ...], "meta": {"limit": 50, "offset": 0, "total": 12}}
```

InboxItem: `id`, `text`, `meta`, `kind`, `revisit_at`, `created_at`, `processed_at`, `converted_task_id`.

#### Быстрый захват (Capture)

```
POST /api/v1/inbox
```

**Request:**
```json
{"text": "Мысль или задача", "meta": {"source": "widget"}, "kind": "idea", "revisit_at": "2026-11-21"}
```

`kind` по умолчанию `task`. Для `idea` и `note` без `revisit_at` дата возврата ставится через неделю. Идея не создаёт задачу: она живёт во входящих и возвращается в назначенный день.

**Response 201:** InboxItem

#### Изменить вид записи

```
PATCH /api/v1/inbox/{id}
```

**Request:** `{"kind": "task", "revisit_at": "2026-11-20"}` (`revisit_at: ""` очищает дату).

**Response 200:** InboxItem
**Response 404:** NotFound
**Response 409:** Conflict (запись уже преобразована)

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
**Response 404:** NotFound (inbox item не найден)
**Response 409:** Conflict (элемент уже преобразован или удалён)

Конвертация атомарна: задача, событие `converted_from_inbox` и `processed_at` записываются в одной транзакции. Если текст длиннее 500 символов, заголовок обрезается до 500, а полный текст сохраняется в `note`.

#### Удалить входящий (Dismiss)

```
DELETE /api/v1/inbox/{id}
```

**Response 204:** No Content
**Response 404:** NotFound
**Response 409:** Conflict (элемент уже обработан)

---

#### План дня и напоминания

```
GET    /api/v1/plan/day?date=YYYY-MM-DD
GET    /api/v1/plan/replan?days=7
POST   /api/v1/plan/apply
DELETE /api/v1/plan/blocks/{id}
```

`GET /plan/day` возвращает день целиком и ничего не меняет: `working` (рабочий ли день по настройкам `planning.workday_*`), `past` (день уже прошёл), `workday_start`/`workday_end`, `free_minutes`, `planned_minutes`, `blocks` (уже занятое под задачи), `busy` (встречи и блоки пользователя), `proposed` (предложение планировщика) и `unplaced` (что не поместилось и почему).

Свободное время считается от текущего момента: в сегодняшнем дне прошедшие часы не предлагаются, а для прошедшего дня `past: true` и `free_minutes: 0`. Каждый предложенный блок содержит `estimate_minutes` и `estimate_source`:

| `estimate_source` | Значение |
|---|---|
| `explicit` | Оценку задал пользователь — она всегда в приоритете |
| `history` | Медиана похожих завершённых задач (минимум две) |
| `default` | `planning.default_estimate_minutes`, если истории нет |

`GET /plan/replan` — предложение переносов на горизонт `days` (1–30): `moves` (задача, `from`/`to`, причина), `placements` (конкретные интервалы), `already_placed` (работа, у которой уже есть время: причина и интервал) и `days` (загрузка по дням). Ничего не записывается. День выбирается только при наличии одного непрерывного окна нужной длины; точный `due_at` ограничивает конец блока, а `due_date` — календарный день.

`POST /plan/apply` записывает принятый план:

```json
{
  "replace_dates": ["2026-12-07"],
  "items": [{"task_id": 8, "start": "2026-12-07T15:10:00Z", "end": "2026-12-07T15:40:00Z"}]
}
```

Набор проверяется целиком до записи: пересечение с встречей или с блоком, который поставил пользователь → `409 plan-conflict`; время вне рабочего дня, нерабочий день, начало в прошлом или блок, заканчивающийся позже дедлайна → `400`. Запрос не стирает день: заменяются только блоки той же задачи на этом дне и автоматические блоки, пересекающиеся с новым интервалом; остальные блоки остаются. Повторная отправка того же интервала идемпотентна. При ошибке не меняется ничего. `task.planned_date` выставляется по дню блока.

`DELETE /plan/blocks/{id}` снимает зарезервированное время; сама задача остаётся открытой.

#### Напоминания

```
GET  /api/v1/reminders
POST /api/v1/reminders/{id}/ack
POST /api/v1/reminders/resync
```

Доставка разделена на два шага, и это различие важно:

1. **Момент наступил** — напоминание становится доступным (`GET /reminders`), статус `pending`.
2. **Пользователь увидел** — клиент подтверждает `POST /reminders/{id}/ack`, статус становится `sent`, `sent_at` заполняется.

`GET /reminders` возвращает всё, что наступило и ещё не подтверждено, — включая напоминания, отправленные при закрытой вкладке или до перезапуска приложения. Повторный `ack` идемпотентен. Событие `reminder` в SSE — вспомогательный канал «здесь и сейчас»; оно не считается доставкой.

`POST /reminders/resync` пересобирает расписание напоминаний у открытых задач, которые остались без него после сбоя.

#### Проверка выполнения

```
POST /api/v1/tasks/{id}/verify
POST /api/v1/tasks/{id}/verify/decision   {"decision": "accepted" | "declined"}
GET  /api/v1/tasks/{id}/verification
```

Проверка сопоставляет `expected_result` задачи с источниками, которые у LifeOS действительно есть: загруженные документы, файлы на машине и календарь. `github` и `messages` возвращаются как `available: false` с причиной — интеграций нет, и проверка этого не скрывает.

**Response 200:** `task_id`, `title`, `expected_result`, `verdict`, `confidence`, `reason`, `evidence[]` (источник, ссылка, фрагмент, `against`), `sources[]` (доступность и причина), `decision`, `checked_at`, `completed`.

| `verdict` | Значение |
|---|---|
| `confirmed` | Ожидаемый результат найден в доступном источнике |
| `insufficient` | Найдено только косвенное подтверждение или ничего |
| `contradictory` | Источники противоречат друг другу |
| `already_completed` | Задача уже завершена, проверка ничего не меняет |

Вердикт сам по себе статус не меняет. `decision: accepted` завершает задачу, `declined` оставляет открытой и запоминается, чтобы следующая проверка не настаивала. Автоматическое завершение возможно только при `planning.auto_complete_verified: true` и вердикте `confirmed`.

#### Повторяющиеся задачи

```
GET  /api/v1/recurrence/pending
POST /api/v1/recurrence/reconcile
```

Завершённая повторяющаяся задача без следующего вхождения — это и есть незавершённая операция: `GET /recurrence/pending` её показывает, `POST /recurrence/reconcile` создаёт пропущенное. Операция идемпотентна (существующее вхождение не дублируется) и выполняется в фоне после перезапуска приложения.

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
  "conversation_id": 1,
  "document_ids": []
}
```

**Response 200:**
```json
{
  "conversation_id": 1,
  "message": {"id": 101, "conversation_id": 1, "role": "assistant", "content": "Создание задачи ждёт подтверждения.", "attachments": [], "created_at": "..."},
  "pending_actions": [
    {"id": 5, "tool_name": "create_task", "arguments": {"title": "Купить молоко"}, "preview": {"summary": "Создать задачу «Купить молоко»"}, "status": "pending"}
  ]
}
```

Обработка синхронная в рамках запроса. Изменения данных не выполняются сразу: инструменты-мутации создают `pending_action`, который пользователь подтверждает или отклоняет. События `assistant.message` и `pending_action.*` также приходят через SSE.

`message.attachments` — типизированные результаты read-инструментов (`task`, `task_list`, `event`, `event_list`), которые интерфейс показывает карточками. Для старых сообщений массив пуст.

**Response 503:** провайдер LLM не настроен (`LIFEOS_LLM_API_KEY` пуст и провайдер не `ollama`).

#### Потоковый ответ (SSE)

```
POST /api/v1/assistant/messages/stream
```

Тело: `content`, `conversation_id`, `model`, `auto_confirm`, `interaction`. Ответ `text/event-stream`:
- `start` — `conversation_id`, `user_message_id`;
- `reasoning` — `delta`;
- `content` — `delta`;
- `tool` — `name`, `status` (`running|done`), `auto_confirmed`;
- `retry` — `model`, `attempt`, `fallback`, `reason`: провайдер не ответил, запрос повторяется или переключается на другую модель (сообщение не прерывается);
- `pending_action` — карточка предложенного действия;
- `done` — `conversation_id`, `message`, `pending_actions`;
- `error` — `message`, `retryable`, `reason`.

`model` — id бесплатной модели из `/assistant/config`. `auto_confirm=true` выполняет мутации сразу; `false` оставляет `pending_action`.

**Устойчивость провайдера.** Сетевые сбои, `429` и `5xx` не завершают сообщение ошибкой: модель повторяется с экспоненциальной задержкой (`LIFEOS_LLM_RETRY_ATTEMPTS`), затем ход переходит к следующей модели из `LIFEOS_LLM_FALLBACK_MODELS`, `LIFEOS_LLM_MODEL` и каталога. Стрим, который уже отдал текст, не перезапускается — клиент сохраняет видимый ответ. Повторная отправка того же текста после сбоя не создаёт второе сообщение пользователя.

`message.reasoning` содержит reasoning trace, если провайдер его отдаёт.

#### Настройки ассистента

```
GET /api/v1/assistant/config
PUT /api/v1/assistant/config   {"model":"...", "auto_confirm":false}
```

Возвращает список бесплатных tool-capable моделей, выбранную модель и режим автоподтверждения.

#### Получить историю диалога

```
GET /api/v1/assistant/conversations/{id}
```

**Response 200:**
```json
{
  "id": 1,
  "title": "Утренний планировщик",
  "kind": "main",
  "purpose": null,
  "source_conversation_id": null,
  "messages": [Message, ...],
  "created_at": "...",
  "updated_at": "..."
}
```

`kind` — `main` (главный чат) или `project` (рабочее пространство). У рабочего пространства `purpose` содержит тему; `source_conversation_id` указывает главный чат, из которого тема вынесена.

#### Список диалогов

```
GET /api/v1/assistant/conversations?kind=main|project
```

**Response 200:** `{ "data": [Conversation, ...] }`. Без `kind` возвращаются все диалоги.

#### Создать рабочее пространство

```
POST /api/v1/assistant/workspaces   {"title": "Диссертация", "purpose": "план глав"}
```

**Response 201:** Conversation с `kind="project"`. Пространство — проектный чат со своей историей; общая память LifeOS общая для всех диалогов.

#### Передать результат из пространства в главный чат

Ассистент вызывает инструмент `report_result` внутри рабочего пространства; результат появляется в главном чате как сообщение с `metadata.handoff`. Отдельного эндпоинта нет.

#### Подтвердить/Отклонить ожидающее действие

```
POST /api/v1/assistant/pending-actions/{id}/confirm
POST /api/v1/assistant/pending-actions/{id}/reject
```

**Response 200:** `{"pending_action": PendingAction, "result": <результат инструмента>}`

Подтверждение выполняет инструмент и записывает результат как сообщение `role=tool`. Повторное подтверждение → `409`.

---

### 5.1 Календарь

```
GET /api/v1/calendar?from=YYYY-MM-DD&to=YYYY-MM-DD&calendar=work&calendar=meetings
```

Возвращает `events`, `tasks` и `conflicts` за диапазон. Конфликты: `event_event` и `event_task`.

```
GET    /api/v1/calendar/events
POST   /api/v1/calendar/events
GET    /api/v1/calendar/events/{id}
PATCH  /api/v1/calendar/events/{id}     If-Match: version
DELETE /api/v1/calendar/events/{id}     If-Match: version
```

Событие: `id`, `title`, `description`, `location`, `calendar` (`work|meetings|personal`), `starts_at`, `ends_at`, `all_day`, `timezone`, `version`. `ends_at` при создании можно не передавать: по умолчанию +1 час (+1 день для all-day).

`DELETE` удаляет событие безвозвратно: строка `calendar_events` удаляется, восстановления нет, событие исчезает из выдачи `/api/v1/calendar` и из инструментов ассистента. `PATCH` — частичное обновление: передаются только изменяемые поля; пустые `description`/`location` очищают поле.

### 5.2 Профиль

```
GET /api/v1/profile
PUT /api/v1/profile   {"name":"...", "role":"..."}
```

Ответ: `{"name","role","initials"}`. Дефолт: «Пользователь / Личный профиль».

### 5.3 Статус

```
GET /api/v1/status
```

`ok`, `version`, `database.ok`, `assistant.configured`, `events.revision`, `pending_actions`, `synced_at`. Используется индикатором синхронизации.

### 5.4 Документы

```
POST   /api/v1/documents              multipart/form-data, поле file
GET    /api/v1/documents
GET    /api/v1/documents/{id}
DELETE /api/v1/documents/{id}
POST   /api/v1/documents/{id}/reprocess
```

Загрузка возвращает `202` и документ в статусе `pending`. Backend **не извлекает задачи** при загрузке: он конвертирует документ в Markdown (DOCX/XLSX/XLS/PPTX/PDF/текст локально; изображения — через vision-модель) и сохраняет результат. UI опрашивает `GET /documents/{id}` до `processed` или `failed`.

Прикрепить документ к сообщению: передать `document_ids` в `POST /assistant/messages` или `/stream`. В пользовательском сообщении сохраняются вложения типа `document`; в системный промпт ассистента добавляется список документов. Ассистент читает Markdown через инструмент `read_document(id, offset, limit)` и уже в диалоге решает, какие задачи создать. Автоподтверждение применяется к созданным задачам как к обычным мутациям.

Поддерживаются `.docx`, `.xlsx`, `.xls`, `.pptx`, `.pdf`, текстовые файлы и изображения (`png/jpeg/webp/gif`). Для PDF нужен `pdftotext`. Устаревшие `.doc/.ppt` отклоняются. Максимальный размер — 25 МиБ. Повторная обработка доступна для неудачных документов.

### 6. Наблюдения (проактивность)

```
GET /api/v1/insights
```

Возвращает вычисленные наблюдения: просрочки, дедлайны сегодня, перегрузку дня, накопившиеся входящие, задачи без срока, совпадающие точные дедлайны. Наблюдения не хранятся: они исчезают, когда исчезает причина.

```json
{"data": [{"key": "overdue", "kind": "overdue", "severity": "critical", "title": "Просроченные задачи", "detail": "2 задач(и) просрочены", "count": 2, "task_ids": [1, 2], "proposal": "Перенести просроченное на реальные даты или закрыть"}]}
```

```
POST /api/v1/insights/{key}/dismiss
POST /api/v1/insights/{key}/snooze   {"until": "2026-11-13T12:00:00Z"}
```

`dismiss` скрывает наблюдение до изменения ситуации; `snooze` — до указанного момента (по умолчанию +24 часа). Оба возвращают `204`.

### 6.1. Инициатива LifeOS (outreach)

Когда наблюдение оправдывает обращение, LifeOS сам начинает диалог в главном чате: сообщение ассистента с причиной и предлагаемым действием. Каждое обращение хранится с реакцией пользователя.

```
GET /api/v1/outreach?limit=30&offset=0
GET /api/v1/outreach/{id}
POST /api/v1/outreach/{id}/ack
POST /api/v1/outreach/{id}/dismiss
POST /api/v1/outreach/{id}/snooze   {"until": "2026-11-13T12:00:00Z"}
```

```json
{"data": [{"id": 1, "conversation_id": 4, "message_id": 21, "insight_key": "overdue", "kind": "overdue", "severity": "critical", "title": "Просроченные задачи", "reason": "2 задач(и) просрочены", "proposal": "Перенести или закрыть", "outcome": "pending", "created_at": "2026-11-12T09:00:00Z", "decided_at": null, "snoozed_until": null}]}
```

`outcome` — `pending`, `acknowledged`, `dismissed` или `snoozed`. Первый ответ пользователя в том же диалоге классифицируется автоматически при следующем проходе движка: согласие («да», «давай») — `acknowledged`, «не сейчас» / «позже» — `snoozed` (+24 часа), отказ («не надо», «нет» и т.п.) — `dismissed`. Ответ, который не распознан ни как согласие, ни как отказ или перенос («что ты имеешь в виду?»), остаётся `pending` и ждёт явной реакции через интерфейс. Классификация читает последние сообщения диалога, поэтому ранний ответ не теряется в длинной переписке; кнопки реакции в интерфейсе имеют приоритет над эвристикой. Повторная реакция по уже решённому обращению возвращает `500` с текстом ошибки, неизвестный id — `404`.

Обращения инициируются только для наблюдений уровня `critical` и `warning` и сдерживаются вратами: тихие часы (`LIFEOS_OUTREACH_QUIET_FROM/TO`), минимальный интервал (`LIFEOS_OUTREACH_MIN_INTERVAL_MINUTES`), откат на вид (`LIFEOS_OUTREACH_KIND_COOLDOWN_HOURS`), вид, отклонённый пользователем трижды за неделю, замолкает на неделю, идущая встреча. Snoozed обращение молчит до сохранённого времени независимо от обычных откатов, в том числе когда snooze назначен на срок дольше окна статистики. Глобальный выключатель — переменная `LIFEOS_OUTREACH_ENABLED` или настройка `proactive.outreach` (`{"enabled": false}`).

### 7. События (SSE)

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

**Переподключение:** Клиент переподключается с `Last-Event-ID`; сервер хранит буфер последних 256 событий и досылает пропущенное. Каждые 15 секунд отправляется комментарий-keepalive (`: keepalive`).

**Payload:** события задач содержат `{"id", "version", "status"}` — этого достаточно, чтобы клиент перечитал задачу по REST и не зависел от формы домена. События входящих: `{"id"}` для `inbox.captured`/`inbox.dismissed`, `{"inbox_id", "task_id", "version"}` для `inbox.converted`.

**Доставка:** in-process broker, доставка неблокирующая. Медленный клиент может пропустить событие и восстановить его при переподключении по `Last-Event-ID`.

---

### 8. Поиск

```
GET /api/v1/search?q=молоко&limit=20
```

Ищет по названию/заметке задач и по тексту входящих. Память и база знаний
ищутся отдельно через `GET /api/v1/memory?q=…`. Ответ:

```json
{"tasks": [Task], "inbox": [InboxItem], "meta": {"total": 2, "limit": 20}}
```

Пустой `q` → `400`. Поиск по входящим нечувствителен к регистру, включая кириллицу.

### 8.1 Память и база знаний

Записи памяти — устойчивые сведения о пользователе (`area: personal`) и материалы
знаний (`area: knowledge`). Каждая запись имеет источник, уверенность, статус и
версию.

**Схема `MemoryEntry`:**

```json
{
  "id": 12,
  "area": "personal",
  "category": "preference",
  "title": "Формат отчётов",
  "content": "Короткие отчёты с таблицами",
  "certainty": "fact",
  "confidence": 1,
  "status": "active",
  "superseded_by": null,
  "entities": ["отчёты"],
  "sources": [
    {"kind": "user", "label": "Слова пользователя", "excerpt": "…", "observed_at": "2026-11-15T09:00:00Z"}
  ],
  "created_at": "2026-11-15T09:00:00Z",
  "updated_at": "2026-11-15T09:00:00Z",
  "version": 1
}
```

`certainty` — `fact`, `assumption` или `inference`; `status` — `active`,
`disputed`, `superseded`. Запись `superseded` в активном списке не появляется, но
остаётся читаемой по id.

#### Список и поиск

```
GET /api/v1/memory?q=отчёты&area=personal&category=preference&status=active&limit=30&offset=0
```

Ответ: `{"data": [MemoryEntry], "meta": {"limit", "offset", "total"}}`. `q`
использует `FTS5` с префиксным совпадением и не ломается на символах `FTS`.
Некорректные `area`/`category`/`status` → `400`.

#### Получить запись

```
GET /api/v1/memory/{id}
```

Возвращает `MemoryEntry` с полными источниками и `links`.

#### Создать сведение

```
POST /api/v1/memory
{"title": "Рабочие часы", "content": "Начинаю в 9:00", "category": "habit", "certainty": "fact", "entities": ["работа"]}
```

`201` → `{"entry": MemoryEntry, "duplicate": false}`. Если тема уже известна и
содержание совпадает — `duplicate: true` и новый источник у существующей записи.
Если содержание противоречит сохранённому — в ответе появляется `conflict_id`, а
запись создаётся в статусе `disputed`. Некорректные поля → `400`.

#### Изменить запись

```
PATCH /api/v1/memory/{id}
If-Match: "1"
{"content": "Начинаю в 9:30", "certainty": "fact"}
```

`200` → `MemoryEntry` с версией +1. Без `If-Match` → `428`; устаревшая версия →
`412`. Правка не удаляет прежнюю формулировку из истории версий.

#### Удалить запись

```
DELETE /api/v1/memory/{id}
If-Match: "2"
```

`204`; без `If-Match` → `428`; устаревшая версия → `412`; неизвестный id → `404`.

#### Связи

```
POST   /api/v1/memory/{id}/links   {"target_type": "task", "target_id": 5, "relation": "относится к"}
DELETE /api/v1/memory/{id}/links/{linkID}
```

`target_type` — `memory`, `task`, `event`, `conversation`, `document`, `inbox`,
`goal`. Цель проверяется в той же транзакции: несуществующий объект → `422`
(`invalid-link`). Повторная такая же связь не создаёт дубль. Удаление → `204`,
неизвестная связь → `404`.

#### Противоречия

```
GET  /api/v1/memory/conflicts            # только ожидающие решения
GET  /api/v1/memory/conflicts?all=true   # включая разрешённые
POST /api/v1/memory/conflicts/{id}/resolve
{"resolution": "existing" | "candidate" | "both" | "merge",
 "merged_title": "…", "merged_content": "…"}
```

Ответ списка: `{"data": [{"id", "reason", "status", "existing": MemoryEntry, "candidate": MemoryEntry, "created_at", "resolved_at"}], "meta": {"total"}}`.

`existing` — оставить прежнюю версию; `candidate` — принять новую; `both` —
оставить обе активными; `merge` — объединить текст (поля обязательны). Решение
применяется атомарно, проигравшая версия становится `superseded` со ссылкой на
замену. Повторное решение → `409`; неизвестный конфликт → `404`; неизвестный
`resolution` → `400`.

#### Статистика

```
GET /api/v1/memory/stats
{"personal": 7, "knowledge": 3, "pending_conflicts": 1}
```

#### События

`memory.created`, `memory.updated`, `memory.deleted`, `memory.source.added`,
`memory.linked`, `memory.unlinked`, `memory.conflict.detected`,
`memory.conflict.resolved` приходят через `GET /api/v1/events` вместе с
остальными.

### 8.2 Долгосрочные цели

Жизненный цикл: `proposed` → (утверждение) `active` ⇄ `paused`, `active` →
`achieved`/`abandoned`. Недопустимый переход → `412`.

#### Схема Goal

```json
{
  "id": 12,
  "title": "Выучить английский до C1",
  "description": "Свободно говорить и читать",
  "status": "active",
  "target_date": "2027-12-01",
  "approved_at": "2026-11-15T10:00:00Z",
  "created_at": "2026-11-10T10:00:00Z",
  "updated_at": "2026-11-15T12:00:00Z",
  "version": 4,
  "milestones": [
    {"id": 30, "position": 0, "title": "Грамматика", "criteria": "120 карточек",
     "status": "done", "due_date": "2027-01-15", "completed_at": "2027-01-10T10:00:00Z", "version": 2}
  ],
  "links": [{"id": 5, "target_type": "conversation", "target_id": 3,
             "relation": "обсуждение", "created_at": "2026-11-15T10:05:00Z"}],
  "proposals": [{"id": 2, "summary": "Перестроить план", "status": "pending",
                 "changes": {"add_milestones": [{"title": "Клуб практики"}]},
                 "created_at": "2026-11-15T12:00:00Z"}],
  "progress_log": [{"id": 40, "kind": "milestone", "summary": "Этап «Грамматика» → done",
                    "recorded_at": "2027-01-10T10:00:00Z"}],
  "progress": {"milestones_total": 3, "milestones_done": 1, "milestones_completed": 2,
               "linked_tasks": 2, "linked_tasks_done": 1, "percent": 33,
               "last_activity_at": "2027-01-10T10:00:00Z"},
  "signals": {"stalled": false, "stall_reason": "", "overdue": false,
              "no_open_work": false, "pending_proposal": true}
}
```

`progress.percent` — по фактически выполненным этапам (`milestones_done`), а без
этапов — по связанным задачам; без того и другого — `null`. `milestones_completed`
включает и выполненные, и пропущенные этапы: план закрыт, но пропуск прогресс не
увеличивает. `signals` вычисляется: `stalled` — нет записей о прогрессе
больше 14 дней (с причиной в `stall_reason`), `overdue` — истёк `target_date` у
активной цели, `no_open_work` — всё сделано, но цель не завершена,
`pending_proposal` — ждёт решение по плану.

#### Endpoints

```
GET    /api/v1/goals?status=active&q=&limit=30&offset=0
POST   /api/v1/goals                        # создание; статус всегда proposed
GET    /api/v1/goals/{id}                   # полная карточка с планом, связями, журналами
PATCH  /api/v1/goals/{id}                   # title/description/target_date + expected_version → 409 при устаревшей
DELETE /api/v1/goals/{id}                   # каскадно удаляет план, связи, журнал и предложения
POST   /api/v1/goals/{id}/approve           # proposed → active
POST   /api/v1/goals/{id}/status            # {"status": "paused"|"active"|"achieved"|"abandoned"}
POST   /api/v1/goals/{id}/milestones        # добавление этапа (только пока план не утверждён или через предложение)
PATCH  /api/v1/goals/{id}/milestones/{mid}  # title/criteria/status/due_date + expected_version
DELETE /api/v1/goals/{id}/milestones/{mid}  # только пока план не утверждён
POST   /api/v1/goals/{id}/proposals         # предложение по изменению утверждённого плана
POST   /api/v1/goals/{id}/proposals/{pid}/apply   # применить (решение пользователя)
POST   /api/v1/goals/{id}/proposals/{pid}/reject  # отклонить
POST   /api/v1/goals/{id}/links             # связать с объектом (тип как у памяти; цель → 422)
DELETE /api/v1/goals/{id}/links/{lid}       # разорвать связь
POST   /api/v1/goals/{id}/progress          # заметка о ходе работы в журнал
```

Ошибки: неизвестный статус/тип/дата → `400`; цель, этап или предложение
отсутствуют → `404`; несуществующий или нулевой источник записи прогресса,
несуществующий объект связи → `400`;
устаревшая версия → `409`; недопустимый переход, правка утверждённого плана
напрямую (включая переименование этапа и смену срока), повторное
`pending`-предложение, применение уже решённого предложения, любое изменение
плана завершённой цели → `412`.

#### Правила, которые важно помнить клиенту

- `PATCH /milestones/{mid}` меняет только состояние этапа, когда цель в работе;
  `title`, `criteria` и `due_date` утверждённой цели правятся только через
  предложение.
- `achieved` и `abandoned` — история: этапы, связи и сам план больше не меняются.
- `percent` считает только фактически выполненные этапы; `skipped` закрывает
  план, но не увеличивает процент (`progress.milestones_completed`).
- `signals.stalled` выставляется только активной цели: `proposed` ждёт
  пользователя, `paused` остановлена осознанно.
- запись прогресса со ссылкой на источник проверяет его существование в той же
  транзакции, как и `POST /links`.
- завершение связанной задачи попадает в журнал через поток событий, а
  периодическая сверка восстанавливает записи, пропущенные при простое LifeOS.

#### События

`goal.created`, `goal.updated`, `goal.deleted` приходят через `GET /api/v1/events`.

Поток может прислать служебное событие `resync` (`{"reason":"gap","missed_after",
"until","recovered"}`), если разрыв в доставке больше кольцевого буфера сервера.
Это не доменное изменение: клиенту следует перечитать состояние через REST.
`resync` приходит и при переподключении с `Last-Event-ID`, который старше
буфера, — то есть в случае, когда дослать недостающие события уже невозможно.

ID событий имеют вид `epoch.seq`, монотонны в пределах установки и не
переиспользуются после перезапуска LifeOS: браузер может безопасно присылать
`Last-Event-ID` прошлого запуска. Кадр `resync` содержит собственный `id`, который
сдвигает курсор `EventSource`; клиенту не нужно хранить его отдельно.

ID из другой эпохи (база была восстановлена из более старой копии) и ID без эпохи
отвечаются `resync`: числа через такую границу не сравниваются.

### 8.3 Пользовательские автоматизации

Жизненный цикл: `proposed` → (утверждение) `active` ⇄ `paused`, любой → `archived`.
Недопустимый переход → `412`; архивная автоматизация не изменяется.

#### Схема Automation

```json
{
  "id": 7,
  "title": "Утренняя сводка",
  "description": "Каждое утро складывать открытые задачи во входящие",
  "status": "active",
  "trigger": {"kind": "schedule", "schedule": {"frequency": "daily", "time": "09:00"}},
  "steps": [{"position": 1, "name": "list_tasks", "arguments": {"status": "open"},
             "description": "Собрать открытые задачи"}],
  "expected_result": "Сводка во входящих до начала рабочего дня",
  "consequences": "Каждое утро создаёт одну входящую запись",
  "approved_at": "2026-11-16T10:00:00Z",
  "last_run_at": "2026-11-16T10:05:00Z",
  "next_run_at": "2026-11-17T06:00:00Z",
  "created_at": "2026-11-16T09:00:00Z",
  "updated_at": "2026-11-16T10:00:00Z",
  "version": 2,
  "runs": [{"id": 3, "trigger": "manual", "status": "succeeded",
            "steps": [{"name": "list_tasks", "status": "succeeded",
                       "output": "{\"total\":3}", "duration_ms": 12}],
            "summary": "Все шаги выполнены (1)",
            "started_at": "2026-11-16T10:05:00Z",
            "finished_at": "2026-11-16T10:05:00Z"}]
}
```

`trigger.kind` — `schedule` (`schedule`: `frequency` `daily`/`weekly`/`monthly`,
`time` `HH:MM` по местному времени, `weekdays` для weekly, `day_of_month` 1–31
или 0 = последний день месяца), `event` (`event`: `name`, необязательный `match`
со сравнением полей payload по строковому представлению, вложенные пути — через
точку) или `condition` (`condition`: `insight` — ключ наблюдения,
`cooldown_minutes`, по умолчанию 360). Триггер `event`/`condition` всегда имеет
`next_run_at = null`; для `schedule` это durable-закладка следующего момента,
пропущенные моменты догоняются один раз после простоя.

#### Endpoints

```
GET    /api/v1/automations?status=active&q=&limit=30&offset=0
POST   /api/v1/automations                     # создание; статус всегда proposed
GET    /api/v1/automations/{id}                # карточка + последние 10 запусков
PATCH  /api/v1/automations/{id}                # title/description/expected_result/consequences/trigger/steps + expected_version → 409 при устаревшей
DELETE /api/v1/automations/{id}                # каскадно удаляет журнал запусков
POST   /api/v1/automations/{id}/approve        # proposed → active; вычисляет следующий момент
POST   /api/v1/automations/{id}/status         # {"status": "active"|"paused"|"archived"}
POST   /api/v1/automations/{id}/run            # ручной запуск; 202 и карточка с журналом
GET    /api/v1/automations/{id}/runs           # журнал запусков (limit/offset)
GET    /api/v1/automations/{id}/runs/{runId}   # один запуск с пошаговым журналом
```

Ошибки: неизвестный статус/тип триггера, шаг без имени, шаг с некорректным JSON
аргументов → `400`; автоматизация или запуск отсутствуют → `404`; устаревшая
версия → `409`; недопустимый переход, правка сценария включённой автоматизации,
ручной запуск неактивной автоматизации → `412`; шаг инструментом, запрещённым
в сценарии → `422`.

#### Правила, которые важно помнить клиенту

- `POST /automations` никогда не активирует автоматизацию: выполнение
  начинается только после `approve` — это решение пользователя.
- `PATCH` включённой автоматизации принимает правки текста
  (`title`/`description`/`expected_result`/`consequences`); триггер и шаги
  меняются только после паузы (`412` иначе).
- Возобновление (`status: active` после паузы) пересчитывает следующий момент
  от текущего времени: пропущенные во время паузы моменты не навёрстываются.
- Ручной запуск и триггерные запуски выполняют одни и те же шаги; в журнал
  пишется источник запуска (`manual`/`schedule`/`event`/`condition`).
- Ошибка шага останавливает сценарий: оставшиеся шаги помечаются `skipped`,
  у запуска `status = failed` и понятная ошибка с номером шага.
- Отказ в запуске до первого шага остаётся в журнале как `skipped`-запись
  с причиной: «превышена глубина цепочки автоматизаций (3)» — событие,
  опубликованное во время выполнения автоматизации, не может поддерживать
  бесконечный цикл (включая реакцию на собственные эффекты);
  «предыдущий запуск ещё выполняется»; «очередь запусков переполнена».
- При старте LifeOS — до открытия HTTP-сервера — зависшие записи `running`
  от предыдущего процесса освобождаются и помечаются `failed` с причиной
  «выполнение прервано: LifeOS был перезапущен во время работы сценария»,
  и каждое из них приходит в SSE как `automation.run.finished`; расписание
  затем пересчитывается от последней известной точки и догоняется как
  обычно. Ошибка восстановления останавливает запуск LifeOS.
- Запись журнала переживает отмену контекста запроса (закрытие соединения
  клиентом не оставляет зависший резерв).
- Событийные триггеры обрабатываются вживую: матчинг идёт по кэшу подписок
  без запроса к БД на каждое событие, диспетчеризация — в ограниченной
  очереди, а журналирование отказов — в собственной асинхронной очереди,
  не задерживающей приём событий; при завершении LifeOS раннер дожидается
  уже выполняющихся запусков и сбрасывает накопленные отказы до закрытия БД.
  Ограничения по времени задают саму работу, а не ожидание: выполняющимся
  запускам даётся 10 с, после чего их контекст отменяется и они обязаны
  завершиться; журнал финализации ограничен 5 с на запись, drain отказов —
  10 с. Служебные операции приёмника (перечитывание подписок) выполняются на
  его собственном контексте, поэтому завершение прерывает их сразу, а не
  ждёт истечения grace-периода. После этого раннер ждёт фактического обнуления счётчика горутин, так
  что его возврат означает, что БД больше никто не использует (худший случай
  30 с, ожидание в приложении — 35 с). События во время простоя LifeOS не
  навёрстываются (в отличие от расписаний).
- События жизненного цикла запусков (`automation.run.started`,
  `automation.run.finished`) публикуются самим сервисом для каждого
  запуска — включая отказ в запуске — и не могут быть триггером
  автоматизации: попытка создаёт автоматизацию отклоняется с 400.
- Сценарий не может содержать инструменты изменений инфраструктуры
  (`run_command`, `write_file`, `edit_file`, `delete_path`), инструменты
  рабочих пространств и инструменты автоматизаций — создание проверяет это
  (`422`).

#### События

`automation.created`, `automation.updated`, `automation.deleted`,
`automation.run.started`, `automation.run.finished` приходят через
`GET /api/v1/events`.

### 8.4 Исследования и внешние источники

Исследование — это вопрос, ответ на который LifeOS ищет и проверяет сам.
`fact` получает только вывод, подтверждённый минимум двумя независимыми сайтами;
`assumption` — вывод одного источника (в том числе недоступной страницы, чья
поисковая выдержка использована как зацепка), `inference` — собственный вывод
LifeOS. Расхождение источников не скрывается: у вывода появляется `conflict`
с цитатой второй стороны, и фактом он уже не считается.

#### Схема ResearchReport

```json
{
  "id": 4,
  "question": "Что нового для generic type aliases в Go 1.24?",
  "queries": ["go 1.24 generic type aliases"],
  "status": "completed",
  "summary": "## ... markdown с разделами: подтверждено / один источник / расхождения",
  "findings": [{
    "statement": "Go 1.24 adds generic type aliases to the language.",
    "certainty": "fact",
    "confidence": 0.72,
    "corroboration": 2,
    "domains": ["example.org", "go.dev"],
    "citations": [{"url": "https://go.dev/doc/go1.24", "title": "Go 1.24",
                   "site": "go.dev", "excerpt": "…", "trust": "normal"}],
    "conflict": "Источники расходятся: example.com утверждает иначе — «…»."
  }],
  "sources": 2,
  "knowledge_entry_id": 31,
  "started_at": "2026-04-12T08:00:00Z",
  "finished_at": "2026-04-12T08:00:14Z",
  "version": 2
}
```

`certainty` — `fact`, `assumption`, `inference`; `trust` источника — `high`,
`normal`, `low` (низкое доверие у поисковой выдержки, чья страница не
открылась). `knowledge_entry_id` появляется, когда результат сохранён в базу
знаний.

#### Endpoints

```
GET    /api/v1/research/reports?q=&limit=20&offset=0
POST   /api/v1/research/reports                 # {"question","queries","max_pages","save_knowledge","knowledge_title","source_id"} → 201
GET    /api/v1/research/reports/{id}
DELETE /api/v1/research/reports/{id}            # запись в базе знаний остаётся
GET    /api/v1/research/sources?kind=&enabled=
POST   /api/v1/research/sources                 # {"name","kind","endpoint","mode","trust","format","notes","enabled"}
PATCH  /api/v1/research/sources/{id}            # name/notes/trust/mode/endpoint/format/enabled + expected_version
DELETE /api/v1/research/sources/{id}
POST   /api/v1/research/sources/{id}/call       # {"operation","method","body","query","write"}
GET    /api/v1/research/sources/{id}/calls?limit=20
GET    /api/v1/research/watches?include_disabled=true
POST   /api/v1/research/watches                 # {"topic","query","queries","cadence","enabled"}
PATCH  /api/v1/research/watches/{id}            # topic/query/queries/cadence/enabled + expected_version
DELETE /api/v1/research/watches/{id}
POST   /api/v1/research/watches/{id}/check      # {"watch": {...}, "change": {"summary","new","updated","unchanged"}}
```

Ошибки: пустой вопрос → `400`; отчёт, источник или наблюдение отсутствуют →
`404`; устаревшая версия → `409`; выключенный источник → `412`; изменяющий
запрос к источнику только на чтение или попытка ассистента создать скрипт →
`403`; веб-поиск не настроен → `503` при старте исследования без провайдера.

Сбой самого исследования — это не ошибка запроса: отчёт создан и сохранён, а
`POST /reports` отвечает `201` с `status: "failed"` и причиной в `error`, поэтому
клиент показывает объяснение, а не общий заголовок ошибки. Такой отчёт остаётся
в истории и открывается как обычный. Проверка наблюдения ведёт себя так же:
`POST /watches/{id}/check` отвечает `200` с `watch` и необязательным `error`.

#### Правила, которые важно помнить клиенту

- `POST /reports` выполняет исследование целиком в рамках запроса: поиск,
  чтение до `max_pages` страниц (по умолчанию 4, максимум 8), сопоставление и
  сохранение. Дольше минуты на медленной сети — это нормальный ход событий,
  а не зависание.
- Запросов на исследование не больше трёх, страниц — не больше восьми; без
  `queries` используется сам вопрос.
- `save_knowledge` по умолчанию `true`: результат сохраняется записью
  категории `research` с источником `web`, уверенностью по фактическому
  подтверждению и адресами сайтов. Если запись не удалась, отчёт остаётся, а
  в `error` появляется причина — молча «сохранено» не бывает.
- Источник, созданный ассистентом, приходит выключенным и только в режиме
  `read`; включение и расширение прав — только через `PATCH` пользователем.
  Скрипт (`kind: "script"`) создаёт только пользователь, и режим у него
  всегда `read`.
- `mode`: `read` — только чтение, `write` — чтение и отправка изменений,
  `sync` — двусторонняя работа. Изменяющий вызов (`write: true` или метод не
  `GET`/`HEAD`) к источнику `read` отклоняется с `403`, и отказ попадает в
  журнал.
- Наблюдение с `cadence: daily`/`weekly` перепроверяется автоматически,
  `manual` — только через `POST /watches/{id}/check`. Изменение фиксируется
  сравнением отпечатков: новая страница отличается от изменившейся, а
  неизменившаяся не создаёт события и не считается изменением.
- Изменение темы становится наблюдением `research-watch-<id>` и приходит в
  `GET /api/v1/insights`; отдельного канала уведомлений у исследований нет.

#### События

`research.completed`, `research.failed`, `research.deleted`,
`research.source.created`, `research.source.updated`, `research.source.deleted`,
`research.watch.created`, `research.watch.updated`, `research.watch.deleted`,
`research.watch.changed`, `research.watch.checked` приходят через
`GET /api/v1/events`.

### 9. Настройки

#### Список настроек

```
GET /api/v1/settings
```

**Response 200:** `{"data": {"key": <any JSON>}}`

#### Получить настройку

```
GET /api/v1/settings/{key}
```

#### Установить настройку

```
PUT /api/v1/settings/{key}
```

**Request:** `{"value": <any JSON>}`

`key` — `[a-z0-9_.-]{1,64}`, значение — валидный JSON до 64 KiB. Ответ — `{"key", "value"}`; невалидный ключ/значение → `400`.

---

## Правила повторных запросов (Idempotency)

- `POST /api/v1/tasks` — идемпотентен по `Idempotency-Key` header (сохраняется 24ч)
- `POST /api/v1/inbox` — идемпотентен по `Idempotency-Key`
- `POST /api/v1/assistant/messages` — **не** идемпотентен (каждое сообщение новое)
- `PATCH /api/v1/tasks/{id}` и все переходы статуса — обязательный `If-Match: <version>`

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