# LifeOS — ARCHITECTURE.md

## 1. Общая архитектура

LifeOS — **Modular Monolith** на Go 1.27.3 с встроенным статическим фронтендом (SvelteKit 2 + Svelte 5).

```
┌─────────────────────────────────────────────────────────────────┐
│                        LifeOS Process                           │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐  │
│  │   Transport  │  │  Application │  │       Domain         │  │
│  │  ┌────────┐  │  │  ┌────────┐  │  │  ┌────────────────┐  │  │
│  │  │ HTTP   │  │  │  │ Tasks  │  │  │  │ Task           │  │  │
│  │  │ REST   │──┼──┤  │ Inbox  │  │  │  │ InboxItem      │  │  │
│  │  │ API    │  │  │  │ Summary│  │  │  │ TaskEvent      │  │  │
│  │  └────────┘  │  │  └────────┘  │  │  │ Conversation   │  │  │
│  │  ┌────────┐  │  │              │  │  │ Message        │  │  │
│  │  │ MCP    │──┼──┤              │  │  │ PendingAction  │  │  │
│  │  │ Adapter│  │  │              │  │  │ AppSettings    │  │  │
│  │  └────────┘  │  └──────┬───────┘  │  └────────┬───────┘  │  │
│  └──────────────┘         │          └───────────┼──────────┘  │
│                           │                      │             │
│  ┌────────────────────────┼──────────────────────┼──────────┐  │
│  │         Infrastructure (SQLite)               │          │  │
│  │  ┌────────────────┐  ┌────────────────┐       │          │  │
│  │  │ Repositories   │  │ Migrations     │       │          │  │
│  │  │ (sqlc-generated)│  │ (goose)        │       │          │  │
│  │  └────────────────┘  └────────────────┘       │          │  │
│  └────────────────────────┬──────────────────────┼──────────┘  │
│                           │                      │             │
│  ┌────────────────────────┼──────────────────────┼──────────┐  │
│  │         Agent Runtime                          │          │  │
│  │  ┌─────────────┐  ┌─────────────┐              │          │  │
│  │  │ Providers   │  │ Tools       │              │          │  │
│  │  │ (OpenRouter,│  │ (App svc    │◄─────────────┘          │  │
│  │  │  Ollama)    │  │  wrappers)  │                         │  │
│  │  └─────────────┘  └─────────────┘                         │  │
│  └────────────────────────┬──────────────────────────────────┘  │
│                           │                                     │
│  ┌────────────────────────┴──────────────────────────────────┐  │
│  │  Config (env)          │  Embedded Frontend (SvelteKit)   │  │
│  └────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

### Принципы разделения

| Слой | Ответственность | Зависимости |
|------|-----------------|-------------|
| **Domain** | Чистые доменные сущности, бизнес-правила, валидация | Никаких внешних |
| **Application** | Use cases, оркестрация, координация репозиториев | Domain |
| **Infrastructure** | SQLite, репозитории (sqlc), миграции (goose) | Domain, Application |
| **Transport/HTTP** | REST API, SSE, встроенный фронтенд | Application |
| **Transport/MCP** | MCP Adapter (тот же Application слой) | Application |
| **Agent Runtime** | LLM провайдеры, инструменты (обёртки Application) | Application, Domain |
| **Config** | Чтение env, defaults | Никаких |

### Направление зависимостей (строгое)

```
Transport (HTTP, MCP) ──► Application ──► Domain
      ▲                       ▲
      │                       │
      └──── Infrastructure ◄─┘
                    ▲
                    │
            Agent Runtime ──► Application (tools only)
                    ▲
                    │
              Providers (external)
```

**Запрещено:**
- Domain зависеть от Infrastructure, Transport, Agent
- Application зависеть от Transport, Agent
- Infrastructure зависеть от Transport, Agent
- Прямой доступ Agent Runtime → SQLite
- UI содержит бизнес-логику Task Engine

---

## 2. Модули и ответственность

### `internal/domain/task`
- `Task` — агрегат: идентичность, заголовок, заметка, приоритет, даты, статус, теги
- `InboxItem` — быстрый захват: текст, метаданные, время создания
- `TaskEvent` — append-only история: created, updated, completed, cancelled, rescheduled, linked
- `Conversation` / `Message` — диалог с ассистентом
- `PendingAction` — ожидающие подтверждения операции от ассистента
- `AppSettings` — пользовательские настройки
- Валидация: даты, переходы статусов, обязательные поля
- Domain детерминирован относительно времени: все временные метки передаются вызывающим кодом, `time.Now()` в Domain не используется

### `internal/application/tasks`
- `ListTasks` — фильтрация, пагинация, сортировка
- `GetTask` — получение по ID
- `CreateTask` — создание с валидацией
- `UpdateTask` — частичное обновление
- `CompleteTask` / `CancelTask` / `RestoreTask` — переходы статуса
- `RescheduleTask` — изменение дат с сохранением истории

**Структура пакета:**

| Файл | Содержимое |
|---|---|
| `ports.go` | `Clock`, `TaskRepository`, `TaskEventRepository`, `UnitOfWork`, `Transaction` |
| `service.go` | `Service` и все Use Cases, транзакционная оркестрация, сборка payload событий |
| `inputs.go` | типизированные входные структуры (`CreateTaskInput`, `UpdateTaskInput`, `TaskFilter`), `Optional[T]`, `ParseSort` |
| `errors.go` | `ErrTaskNotFound`, `ErrVersionConflict`, `ErrInvalidLimit/Offset/Sort`, `TaskWithVersion` |

**Repository ports.** Порты принадлежат Application; Infrastructure реализует их в `internal/infrastructure/sqlite` (Phase 2.3, реализовано).

- `TaskRepository` — `FindByID`, `List`, `Insert`, `Update(id, expectedVersion, task)`.
  `Update` обязан выполнять сравнение и инкремент одной инструкцией (`WHERE id = ? AND version = ?`, `version = version + 1`).
- `TaskRepository.List` — при установленном фильтре `overdue` использует `TaskFilter.AsOf`
  как reference instant для `Task.IsOverdue(asOf)`. Репозиторий не читает часы сам: `AsOf`
  заполняет `Service.ListTasks` из внедрённого `Clock`. Если `AsOf` не задан, фильтр `overdue` не применяется.
- `TaskEventRepository` — только `Insert`: история append-only.
- `UnitOfWork` — `Do(ctx, fn)` открывает транзакцию, передаёт `Transaction` с репозиториями, делает commit при `nil` и rollback при ошибке или отмене контекста.
- `Clock` — `Now() time.Time`; production-реализация использует `time.Now().UTC()`, в тестах подменяется фиксированным временем.

**Версионирование (optimistic locking).** `tasks.version` — concurrency metadata на границе Application/Repository, а не поле домена. `TaskWithVersion` возвращает `Task` и `Version` отдельно. Чтение версию не меняет; каждая успешная мутация увеличивает её ровно на 1; запись условна по `expectedVersion`. Устаревшая версия → `ErrVersionConflict`, отсутствующая задача → `ErrTaskNotFound`. `updated_at` не является механизмом блокировки. Финальная проверка версии выполняется атомарно в репозитории, а не только предварительным чтением в сервисе.

**Атомарность Task + TaskEvent.** Каждая мутация выполняется внутри одного `UnitOfWork`: задача и её событие (`created`, `updated`, `completed`, `cancelled`, `restored`, `rescheduled`) записываются в одной транзакции. Ошибка записи события откатывает и изменение задачи, и версию. Событие создаётся тем же `now`, что и отметка задачи, поэтому `Task.CreatedAt` и `TaskEvent.CreatedAt` совпадают.

**Направление зависимостей.** Application зависит только от `internal/domain/task` и стандартной библиотеки. `internal/config` в Application не импортируется — настройки передаются через конструктор (`WithDefaultTimezone`).

### `internal/application/inbox`
- `List` — получение входящих (пагинация, `unprocessed_only`)
- `Capture` — быстрый захват
- `ConvertToTask` — преобразование в задачу: task + событие + отметка `processed_at` в одной транзакции
- `Dismiss` — удаление необработанного элемента
- `CountUnprocessed` — счётчик для Summary

### `internal/application/summary`
- `Today` — агрегат для главного экрана: задачи на сегодня, просроченные, высокий приоритет, статистика, focus task
- Read model: использует те же доменные правила просрочки и локального дня, что и список задач

### `internal/infrastructure/sqlite`
- Репозитории задач и событий на `database/sql` (порты Application)
- `TaskUnitOfWork`: одна транзакция на use case, task + event атомарны
- `goose` миграции
- WAL mode, busy_timeout, foreign_keys=ON
- Connection pooling (std database/sql)

### `internal/transport/http`
- `net/http` (stdlib)
- REST handlers → Application services
- Реализованы: `/healthz`, все `/api/v1/tasks*` (фильтры, пагинация, optimistic locking через `If-Match`/`ETag`), `/api/v1/inbox*` и `/api/v1/summary/today`
- SSE endpoint для realtime
- Embedded frontend serving
- Error handling: `application/problem+json`
- Structured logging (`slog`)

### `internal/transport/mcp`
- MCP Adapter: инструменты = обёртки над Application services
- Streamable HTTP transport
- Один экземпляр на процесс

### `internal/application/assistant`
- `SendMessage` — диалог: история, системный промпт, цикл tool calling (до 5 раундов)
- `ConfirmPendingAction` / `RejectPendingAction` — подтверждение мутаций
- `ListConversations` / `GetConversation`
- Провайдер вызывается вне транзакций БД; каждая запись — короткая транзакция

### `internal/agent`
- `OpenAIProvider` — любой OpenAI-совместимый endpoint (OpenRouter, Ollama `/v1`, локальный шлюз)
- `internal/agent/tools` — реестр инструментов LifeOS: чтение (`list_tasks`, `get_today_summary`, `list_inbox`) и мутации (`create_task`, `update_task`, `complete_task`, `cancel_task`, `restore_task`, `reschedule_task`, `capture_inbox`, `convert_inbox`, `dismiss_inbox`)
- Мутации не выполняются моделью напрямую: они создают `pending_action` с превью и ждут подтверждения пользователя

### `internal/agent/providers`
- OpenRouter: `openai-go/v3` с base URL
- Ollama: локальный, совместимый с OpenAI API

### `internal/agent/tools`
- Каждый инструмент = тонкая обёртка над Application service
- Схемы в JSON Schema (для LLM)
- Валидация входных данных
- Возврат структурированных результатов

### `internal/events`
- In-process broker для SSE: подписки, буфер последних 256 событий, `Last-Event-ID` replay
- Application services публикуют события после успешного commit, не после rollback
- Транспорт не знает о домене: payload — минимальные `{id, version, status}`

### `internal/config`
- Чтение `.env` и env vars
- Defaults, валидация
- Типизированная структура `Config`

### `internal/web/dist`
- `embed.FS` для статических файлов фронтенда
- Served через HTTP transport; SPA-fallback на `index.html`

### `web` (SvelteKit 2 + Svelte 5)
- SPA (`ssr = false`), статический adapter, встраивается в Go-бинарник
- Экраны: `/` (Сегодня), `/inbox` (Входящие), `/tasks` (Все задачи), `/assistant` (заглушка до Stage 5)
- `web/src/lib/api.ts` — типизированный клиент REST API; мутации отправляют `If-Match` с версией задачи
- `web/src/lib/components/TaskRow.svelte` — общая карточка задачи (завершение, отмена, восстановление, перенос)
- Визуальное направление — Concept B (тёмная тема, фиолетово-голубые акценты, боковая навигация, mobile bottom nav)

---

## 3. Взаимодействие компонентов

### REST API → Application
```
HTTP Request → Router → Handler → Application Service → Domain + Repository → Response
```

### MCP Adapter → Application
```
MCP Call → Adapter → Application Service (тот же, что и REST) → Domain + Repository → MCP Result
```

### Agent Runtime → Application
```
User Message → Agent → LLM (с инструментами) → Tool Call → Application Service → Result → LLM → Response
```

### SSE (Realtime)
```
Client → GET /api/v1/events (Last-Event-ID) → Broker.Subscribe + Replay → stream
Application service → commit → Broker.Publish → все подписчики
```
Реализовано: `task.*`, `inbox.*`; фронтенд перезагружает данные по событию (debounce 200 мс).

### Frontend → Backend
```
Static files (embed) → Browser → fetch/openapi-fetch → REST API + SSE
```

---

## 4. Работа с SQLite

| Параметр | Значение | Обоснование |
|----------|----------|-------------|
| Driver | `modernc.org/sqlite` v1.62.0 | Pure Go, без CGO |
| Mode | WAL | Concurrency, durability |
| Busy timeout | 5000 ms | Устойчивость к блокировкам |
| Foreign keys | ON | Интегритет данных |
| Pragmas | `journal_mode=WAL`, `synchronous=NORMAL`, `cache_size=-32768`, `temp_store=MEMORY` | Производительность на Atom |
| Migrations | `pressly/goose` v3 | Версионирование схемы |
| Queries | `sqlc` v1.27 | Типобезопасный Go код из SQL |

**Схема БД (первичная, детально в DATA_MODEL.md):**
- `tasks` — основная таблица задач
- `inbox_items` — входящие
- `task_events` — append-only история
- `conversations` / `messages` — диалоги
- `pending_actions` — ожидающие подтверждения
- `app_settings` — key-value настройки

---

## 5. Конфигурация

### Переменные окружения (`.env.example`)

```env
# Server
LIFEOS_LISTEN_ADDR=127.0.0.1:8787
LIFEOS_DATA_DIR=./data
LIFEOS_TIMEZONE=Europe/Moscow

# LLM
LIFEOS_LLM_PROVIDER=openrouter
LIFEOS_LLM_BASE_URL=https://openrouter.ai/api/v1
LIFEOS_LLM_API_KEY=
LIFEOS_LLM_MODEL=openai/gpt-4o-mini

# MCP
LIFEOS_MCP_ENABLED=false
LIFEOS_MCP_PATH=/mcp

# Frontend (dev)
LIFEOS_FRONTEND_DEV=false
LIFEOS_FRONTEND_URL=http://localhost:5173
```

### Принципы
- Секреты только в env / `.env` (не в Git)
- Defaults в коде, переопределение через env
- Валидация при старте
- Типизированный доступ через `config.Load()`

---

## 6. Безопасность

- **Нет авторизации v1** — персональная система, доступ через Tailscale
- **TLS** — терминируется на Tailscale Serve (не в приложении)
- **CORS** — только для dev frontend URL
- **Rate limiting** — базовый на уровне HTTP middleware
- **Input validation** — на уровне handlers + domain
- **SQL injection** — предотвращается `sqlc` (parameterized queries)
- **XSS** —/frontend не рендерит пользовательский HTML, CSP в dev
- **Secrets** — никогда в логах, `.env` в `.gitignore`

---

## 7. Развёртывание на Atom (Debian 13, Intel Atom D525)

### Сборка
```bash
GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o lifeos ./cmd/lifeos
```

### Файловая структура на сервере
```
/opt/lifeos/
├── lifeos              # бинарник
├── data/               # SQLite БД (отдельно от бинарника)
│   └── lifeos.db
├── backups/            # Резервные копии
├── .env                # Конфигурация (секреты)
└── lifeos.service      # systemd unit
```

### systemd unit
```ini
[Unit]
Description=LifeOS Personal Operating System
After=network-online.target tailscale.service
Wants=network-online.target

[Service]
Type=simple
User=lifeos
WorkingDirectory=/opt/lifeos
ExecStart=/opt/lifeos/lifeos
EnvironmentFile=/opt/lifeos/.env
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/lifeos/data /opt/lifeos/backups

[Install]
WantedBy=multi-user.target
```

### Tailscale Serve
```bash
tailscale serve --bg --https=443 --set-path / /opt/lifeos/lifeos
# Или: tailscale serve --bg 8787 http://127.0.0.1:8787
```

### Резервное копирование
```bash
# Онлайн backup (SQLite поддерживает)
sqlite3 /opt/lifeos/data/lifeos.db ".backup /opt/lifeos/backups/lifeos-$(date +%F).db"
# При сжатии: .backup | gzip
```

### Обновление
1. Собрать новый бинарник на Windows
2. Скопировать на Atom (scp/rsync)
3. `systemctl stop lifeos`
4. Заменить бинарник (старый сохранить как `.bak`)
5. `systemctl start lifeos`
6. Проверить health endpoint

### Откат
```bash
systemctl stop lifeos
mv /opt/lifeos/lifeos /opt/lifeos/lifeos.failed
mv /opt/lifeos/lifeos.bak /opt/lifeos/lifeos
systemctl start lifeos
```

---

## 8. Схемы взаимодействия

### Запрос задачи (REST)
```
GET /api/v1/tasks?status=open&priority=high
    │
    ▼
HTTP Handler → ListTasks(filters) → TaskRepository.Query → []Task → JSON Response
```

### Создание задачи через ассистента
```
User: "Создай задачу купить молоко"
    │
    ▼
Agent → LLM (tools: create_task) → Tool Call: create_task({title, priority})
    │
    ▼
Application.CreateTask → Domain.Task.Validate → Repository.Insert → TaskEvent.Created
    │
    ▼
LLM receives result → Response to user
    │
    ▼
SSE broadcasts task.created → Frontend updates
```

### Преобразование входящего
```
POST /api/v1/inbox/42/convert
    │
    ▼
Application.ConvertToTask → InboxItem → Task (copy fields) → Repository.InsertTask + DeleteInbox + TaskEvent.Created
    │
    ▼
Response: Task
    │
    ▼
SSE: inbox.dismissed, task.created
```

---

## 9. Версионирование и совместимость

- **API**: `/api/v1` — breaking changes только через v2
- **DB**: goose миграции — только additive, не удалять колонки
- **Frontend**: встроен в бинарник — версия = версия бинарника
- **Config**: новые env vars — optional с defaults

---

## 10. Открытые вопросы (требуют продуктового решения)

1. **Мягкое удаление задач** — `deleted_at` + фильтр vs hard delete + event?
2. **Повторяющиеся задачи** — в v1 или позже? Если позже — как заложить в схеме?
3. **Приоритеты** — только 3 уровня (high/medium/low) или числовая шкала?
4. **Теги** — свободные или из фиксированного словаря?
5. **Вложенность задач** (subtasks) — нужна ли в v1?
6. **Прикрепления/файлы** — в v1 только ссылки или загрузка?
7. **Уведомления** — только in-app (SSE) или push/email позже?
8. **Множественные пользователи** — строго single-user или предусмотреть `user_id`?

> **Правило:** не принимай необратимые продуктовые решения самостоятельно. Зафиксируй варианты в DECISIONS.md и предоставь выбор.