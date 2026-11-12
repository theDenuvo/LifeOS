# LifeOS

LifeOS — персональная операционная система для повседневной жизни и работы:
задачи, входящие, расписание, сводка дня, проактивные наблюдения и ассистент,
который умеет читать данные и предлагать действия.

Ключевая идея: **ассистент — не отдельный чат, а часть рабочей среды**. Он видит
те же задачи и входящие, что и вы, а любые изменения предлагает подтвердить.

## Что уже работает

- **Задачи**: создание, фильтры, поиск, сортировка, пагинация, дедлайны
  (календарные и точные), теги, завершение/отмена/восстановление/перенос,
  optimistic locking через `If-Match`/`ETag`.
- **Входящие**: быстрый захват, разбор, конвертация в задачу с сохранением
  полного текста, удаление.
- **Сегодня**: статистика, фокус-задача, списки на сегодня и просроченных,
  быстрый ввод, панель наблюдений.
- **Ассистент**: диалоги, история, tool calling. Read-only инструменты
  выполняются сразу, мутации создают `pending_action` с превью и выполняются
  только после подтверждения.
- **Проактивность**: просрочки, дедлайны сегодня, перегрузка дня, backlog
  входящих, задачи без срока, совпадающие дедлайны. Наблюдения можно скрыть или
  отложить; повторные уведомления не спамят.
- **Realtime**: SSE-поток `task.*`, `inbox.*`, `assistant.*`, `pending_action.*`,
  `insight.detected` с replay по `Last-Event-ID`.
- **MCP**: tools-only сервер поверх Streamable HTTP; по умолчанию read-only.
- **Настройки и поиск**: JSON key-value настройки, общий поиск по задачам и
  входящим.
- **Данные**: SQLite, миграции goose, WAL, атомарные task + audit event.

## Архитектура

Modular monolith на Go со встроенным SvelteKit-фронтендом:

```
Transport (HTTP, SSE, MCP) → Application → Domain
                 ↑
          Infrastructure (SQLite)
                 ↑
            Agent Runtime (LLM + tools)
```

- `internal/domain` — Task, InboxItem, TaskEvent, Conversation, Message,
  PendingAction; без внешних зависимостей.
- `internal/application` — use cases: tasks, inbox, summary, assistant,
  proactive, search, settings.
- `internal/infrastructure/sqlite` — репозитории, Unit of Work, миграции.
- `internal/transport` — REST/SSE, MCP-адаптер.
- `internal/agent` — OpenAI-совместимый провайдер и реестр инструментов.
- `web` — SvelteKit SPA, встраивается в Go-бинарник.

Подробности: [ARCHITECTURE.md](ARCHITECTURE.md), [DATA_MODEL.md](DATA_MODEL.md),
[API_CONTRACT.md](API_CONTRACT.md), [DECISIONS.md](DECISIONS.md),
[DEPLOYMENT.md](DEPLOYMENT.md), [MCP.md](MCP.md).

## Быстрый старт

Требования: Go 1.27.3, Node.js 20+, npm 10+.

```bash
cp .env.example .env
# заполните LIFEOS_LLM_API_KEY, если нужен ассистент

# 1. Собрать фронтенд
cd web && npm ci && npm run build && cd ..

# 2. Встроить сборку и запустить
rm -rf internal/web/dist/build && cp -r web/build internal/web/dist/build
go run ./cmd/lifeos
```

Откройте http://127.0.0.1:8787 — данные лежат в `./data/lifeos.db`.

Для разработки фронтенда:

```bash
cd web && npm run dev     # http://localhost:5173, API проксируется на :8787
```

## Конфигурация

Все переменные — в [.env.example](.env.example). Основные:

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `LIFEOS_LISTEN_ADDR` | `127.0.0.1:8787` | Адрес HTTP-сервера |
| `LIFEOS_DATA_DIR` | `./data` | Директория SQLite |
| `LIFEOS_TIMEZONE` | `Europe/Moscow` | Часовой пояс по умолчанию |
| `LIFEOS_LLM_PROVIDER` | `openrouter` | `openrouter` или `ollama` |
| `LIFEOS_LLM_BASE_URL` | `https://openrouter.ai/api/v1` | OpenAI-совместимый endpoint |
| `LIFEOS_LLM_API_KEY` | — | Ключ провайдера |
| `LIFEOS_LLM_MODEL` | `openai/gpt-4o-mini` | Модель |
| `LIFEOS_MCP_ENABLED` | `false` | Включить MCP |
| `LIFEOS_MCP_ALLOW_WRITES` | `false` | Разрешить мутации через MCP |

Без ключа (и без `LIFEOS_LLM_PROVIDER=ollama`) ассистент отвечает `503`, а
остальные экраны работают.

## Проверка

```bash
go vet ./...
go test -race ./...
cd web && npm run check && npm run build
```

CI выполняет эти же проверки для Windows и Linux AMD64 v1 (`CGO_ENABLED=0`).

## Ограничения текущей версии

- Один пользователь, без авторизации; доступ рассчитан на локальную сеть или
  Tailscale.
- Ассистент синхронный: ответ приходит в рамках запроса, без стриминга токенов.
- MCP по умолчанию read-only; мутации включаются явно.
- Поиск по входящим выполняется сканированием (Unicode case folding); при
  росте объёма данных потребуется индекс.
- Внешние интеграции (почта, календарь, файлы, голос) пока не подключены —
  архитектура готова к их добавлению как новых инструментов и модулей.
