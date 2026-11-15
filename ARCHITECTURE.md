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

### `internal/domain/event` и `internal/application/events`

События календаря: интервал, календарь-источник, all-day, optimistic locking. Read-model `internal/application/calendar` объединяет события и задачи и вычисляет пересечения.

### `internal/application/documents`

Загрузка документов, локальное извлечение текста (DOCX/XLSX/PPTX/PDF/текст), vision-извлечение задач из изображений, предложения задач и их принятие через Task Service. Файлы — `internal/infrastructure/documents`, LLM — `internal/agent/documents`.

### `internal/application/profile`

Профиль поверх `app_settings` (`profile.name`, `profile.role`), инициалы выводятся на сервере.

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
- Tools-only MCP поверх Streamable HTTP (JSON-RPC 2.0): `initialize`, `ping`, `tools/list`, `tools/call`
- Тот же реестр инструментов, что у ассистента (`internal/agent/tools`)
- Read-only по умолчанию; мутации только при `LIFEOS_MCP_ALLOW_WRITES=true`
- Один экземпляр на процесс; подробности в `MCP.md`

### `internal/application/assistant`
- `SendMessage` — диалог: история, системный промпт, цикл tool calling (до 5 раундов)
- `ConfirmPendingAction` / `RejectPendingAction` — подтверждение мутаций
- `ListConversations` / `GetConversation`
- Провайдер вызывается вне транзакций БД; каждая запись — короткая транзакция

### `internal/agent`
- `OpenAIProvider` — любой OpenAI-совместимый endpoint (OpenRouter, Ollama `/v1`, локальный шлюз)
- SSE-ответы (`/assistant/messages/stream`, `/events`) снимают `WriteTimeout` через `http.ResponseController`; `responseWriter` в логирующем middleware реализует `Unwrap`, иначе дедлайн не снимается и стрим рвётся через 30 с
- Ассистентский ход живёт в модульном хранилище `web/src/lib/assistantRun.ts`, а не в компоненте чата: переключение раздела интерфейса размонтирует чат, но не отменяет ответ; при возврате вкладка подхватывает текущий ход и догружает финальное сообщение из API. Отмена возможна только явно («Стоп», новый диалог).
- Событие можно изменить и удалить из чата: `update_event` (частичное обновление) и `delete_event` (безвозвратно), оба проходят через обычный механизм подтверждений

- Массовые действия: `create_events` создаёт до 200 событий одним вызовом инструмента, поэтому импорт расписания из Excel укладывается в один раунд и одно подтверждение вместо десятков
- Календарные события меняются и удаляются прямо в интерфейсе календаря: `PATCH /api/v1/calendar/events/{id}` и `DELETE ...` под `If-Match` с версией. Удаление аппаратное (строка `calendar_events` удаляется), поэтому событие исчезает из календаря, сводок и выдачи ассистента сразу; архивной копии нет.
- Файловые инструменты отказывают в доступе к собственной базе `lifeos.db` (и её `-wal`/`-shm`): данные меняются только через сервисы LifeOS
- `RetryProvider` (`internal/agent/resilience.go`) — ретраи с экспоненциальной задержкой (`429`, `5xx`, обрывы сети, пустой ответ) и фоллбек на следующую модель; стрим, уже отдавший текст, не перезапускается. Ретраи/фоллбеки приходят в UI событием `retry`
- `internal/agent/tools` — реестр инструментов LifeOS: чтение (`list_tasks`, `get_today_summary`, `list_inbox`, `plan_day`, `replan`, `estimate_duration`, `review_habit`, `search_memory`, `list_memory`, `memory_conflicts`, `list_goals`, `get_goal`) и мутации (`create_task`, `update_task`, `complete_task`, `cancel_task`, `restore_task`, `reschedule_task`, `capture_inbox`, `convert_inbox`, `dismiss_inbox`, `apply_plan`, `capture`, `remember`, `save_knowledge`, `update_memory`, `forget_memory`, `link_memory`, `create_goal`, `update_goal_plan`, `update_goal_milestones`, `propose_goal_change`, `approve_goal`, `apply_goal_proposal`, `reject_goal_proposal`, `change_goal_status`, `link_goal`, `unlink_goal`, `record_goal_progress`)

### `internal/application/planning`
- `PlanDay` — читает события и блоки дня, считает свободные интервалы с буфером и предлагает расстановку задач по срочности; ничего не пишет. Свободное время отсчитывается от текущего момента (`ClipFrom`), прошедший день помечается как `past` и не планируется
- `Apply` — валидирует весь набор блоков сразу (пересечение с встречей или с блоком пользователя → `409`; прошлое и выход за дедлайн → `400`) и только потом пишет. Запрос не стирает день: заменяются блоки той же задачи на этом дне и автоматические блоки, пересекающиеся с новым интервалом, поэтому принятие одного нового блока не уничтожает ранее принятый план
- `Replan` — предложение переносов: день выбирается только при наличии одного непрерывного окна нужной длины, точный `due_at` ограничивает конец блока, а работа, у которой уже есть блок в горизонте, не предлагается повторно (`already_placed`)
- `resolveEstimate` — единое правило длительности: явная оценка → медиана похожих завершённых задач → `planning.default_estimate_minutes`; источник (`explicit`/`history`/`default`) возвращается вместе с планом
- `SyncTaskReminders` — пересчитывает моменты напоминаний при каждом изменении задачи; `Dispatcher` (тик 30 с) публикует `reminder` в SSE и отмечает `notified_at`, а доставкой считается только подтверждение клиента (`POST /reminders/{id}/ack`), поэтому напоминание переживает перезапуск и закрытую вкладку
- `ReconcileReminders` — лечит задачу, оставшуюся без расписания напоминаний после сбоя пересчёта
- Правила, а не настройки: длительность текста напоминаний зависит от приоритета и оценки; число напоминаний на задачу ограничено тремя

### `internal/application/recurrence`
- Завершение повторяющейся задачи создаёт следующее вхождение (дата по правилу, окно переноса, оценка и теги сохраняются) и оставляет завершённое в истории
- Незавершённая работа выводится из самих задач: завершённая повторяющаяся задача без `repeat_parent_id`-потомка — это и есть пропущенное вхождение. `Reconcile` и фоновый `Worker` создают его после сбоя и после перезапуска, `Next` проверяет существующего потомка и не дублирует вхождение
- `Review` — сравнение фактических дат завершения с планом: рекомендация изменить интервал или разрешить окно переноса, без автоматического изменения правила

### `internal/application/completion`
- `Check` сопоставляет `expected_result` с источниками, которые реально доступны: `documents` (текст загруженных документов), `files` (файлы на машине новее задачи), `calendar` (прошедшие события). `github` и `messages` возвращаются как недоступные с причиной
- Вердикт `confirmed` требует сильного совпадения (фраза результата в документе, файл с ожидаемым содержимым); слабое совпадение даёт `insufficient`, а противоречие — `contradictory`. Совпадение отдельных слов не считается доказательством
- Заключение, основание и решение пользователя сохраняются в `completion_checks`. Статус задачи меняет только решение пользователя, либо автоматическое завершение при явно включённой настройке `planning.auto_complete_verified`; запомненный отказ блокирует автоматику

### `internal/application/memory` и `internal/domain/memory`

Долговременная память и живая база знаний (этап 2). Модуль не зависит от
документов, задач и ассистента: он работает с записями и источниками, а связи с
другими объектами описываются типом и идентификатором.

- `Entry` — личное сведение или материал знаний: категория (определяет область),
  содержание, сущности, уверенность, статус (`active`/`disputed`/`superseded`) и
  версия. `Certainty` отделяет факт от предположения и вывода.
- `Source` — происхождение записи: пользователь, ассистент, диалог, документ,
  задача, событие или система, с фрагментом и временем наблюдения. Одинаковые
  сведения не дублируются: добавляется источник.
- `Link` — именованная связь с задачей, событием, диалогом, документом,
  входящим или другой записью. Существование цели проверяется **в той же
  транзакции**: отдельное соединение внутри транзакции блокирует SQLite с одним
  пишущим соединением.
- `Conflict` — две несовместимые версии одной темы. Проигравшая не удаляется, а
  помечается `superseded` со ссылкой на замену, поэтому вывод остаётся
  проверяемым.

Правила ведения памяти:

| Ситуация | Поведение |
|---|---|
| Точный дубль | Запись не дублируется, добавляется источник |
| Уверенное исправление той же темы | Новая версия, прежняя помечается `superseded` |
| Слабое утверждение или предположение по известной теме | Запись `disputed`, создаётся `Conflict` |
| Тема без истории | Новая активная запись |
| Противоречие | Решение принимает пользователь (прежняя, новая, обе, объединение) |
| Правка и удаление | Только с `If-Match` по версии |

Автоматическое ведение: `LearnConversation` вызывается ассистентом после ответа
(вне запроса, со своим таймаутом) и не влияет на сам ответ; `ImportDocument`
вызывается после успешной конвертации документа. Обработанный документ всегда
индексируется целиком как `documentation`, даже если извлечение фактов
недоступно; ошибка извлечения попадает в результат импорта, а не ломает
документ.

Поиск — `FTS5` (`unicode61 remove_diacritics 2`) с префиксным совпадением по
последнему слову; пользовательский запрос экранируется, поэтому символы `FTS`
не меняют смысл запроса.

Контекст ассистента дополняется `ContextSnapshot`: только активные личные
сведения, с категорией, уверенностью и источником, плюс счётчик неразрешённых
противоречий. Снимок — подсказка, а не источник истины: за точными данными
модель идёт в `search_memory`.

### `internal/domain/planning`
- `Block` — зарезервированное время под задачу (`auto`/`manual`/`assistant`); договорённости пользователя неприкосновенны, двигаются только `auto`
- `PlanReminders` — чистая функция расписания напоминаний: только будущие моменты, без дат — без напоминаний, у завершённых — ничего

- `internal/agent/tools/workspace.go` — инструменты машины: `read_file`, `list_files`, `search_files`, `write_file`, `edit_file`, `delete_path`, `run_command`. Корень (`LIFEOS_TOOLS_ROOT`) по умолчанию не ограничен, оболочку можно отключить (`LIFEOS_TOOLS_SHELL=false`). Все изменения — мутации, то есть проходят через `pending_action`, если не включено автоподтверждение
- Приложенные к сообщению документы передаются модели дословно в prompt (до `MaxDocumentPromptChars` на документ), а не только их ID
- Мутации не выполняются моделью напрямую: они создают `pending_action` с превью и ждут подтверждения пользователя

### `internal/agent/providers`
- OpenRouter: `openai-go/v3` с base URL
- Ollama: локальный, совместимый с OpenAI API

### `internal/agent/tools`
- Каждый инструмент = тонкая обёртка над Application service
- Схемы в JSON Schema (для LLM)
- Валидация входных данных
- Возврат структурированных результатов

### `internal/application/proactive`
- Детекторы: просрочки, дедлайны сегодня, перегрузка, backlog входящих, задачи без срока, совпадающие точные дедлайны, остановившиеся цели (этап 4)
- Наблюдения вычисляются из живых данных; в БД хранятся только dismiss/snooze (`insight_states`)
- `Runner` периодически пересчитывает и публикует `insight.detected` только для новых ключей — без потока повторных уведомлений

### `internal/application/goals` и `internal/domain/goal`

Долгосрочные цели и управление инициативами (этап 4). Модуль не зависит от
ассистента и транспорта: сервис работает с целями, этапами, связями, журналом
прогресса и предложениями, а задачи подключаются узким портом `TaskProvider`.

- Жизненный цикл задаётся в домене (`CanTransition`): цель ассистента создаётся в `proposed` и начинает работу только после `Approve`; терминальные статусы остаются в истории
- Прямой доступ к утверждённому плану закрыт (`ErrGoalNotProposed`): существенные изменения проходят через `ProposeChange` → ожидание → `ApplyProposal`/`RejectProposal`, всё атомарно одной транзакцией, одновременно не более одного `pending`-предложения
- Прогресс вычисляется по фактическим данным: выполненные этапы, связанные задачи (завершение задачи попадает в журнал через поток событий, `OnTaskCompleted`), заметки пользователя и ассистента
- Отклонения называются с причиной: «нет записей о прогрессе N дней» (> 14, только для активной цели), истёк срок, вся работа сделана без отметки о достижении; остановившиеся цели отдаёт `Stalled`, и они попадают в наблюдения (`proactive.WithGoals`) и в контекст ассистента
- Прогресс читается вне транзакции цели: движок задач ходит в ту же БД с одним соединением, и вызов внутри открытой транзакции ждал бы освобождения этого соединения
- Завершение связанной задачи пишется в журнал **синхронно**: `tasks.Service.SetCompletionHook` вызывает `OnTaskCompleted` до возврата из `CompleteTask`, поэтому потеря события или последующее восстановление задачи не стирают факт выполнения
- `OnTaskCompleted` возвращает ошибку, а не только пишет её в лог: сбой записи виден вызывающему коду, при этом само завершение задачи уже зафиксировано и не откатывается
- Журнал идемпотентен по источнику, а `ReconcileTaskProgress` восстанавливает пропущенное. Она не полагается на текущий статус задачи: завершение — долговечный факт, записанный в `task_events` в той же транзакции, поэтому сверка считает задачу выполненной и после того, как её восстановили. Запись, чей синхронный вызов упал, таким образом всё равно появляется

### `internal/events`
- In-process broker для SSE: подписки, буфер последних 256 событий, `Last-Event-ID` replay
- `Publish` назначает ID и рассылает событие под одним mutex; доставка в канал подписчика неблокирующая. Это даёт два свойства, на которые опирается SSE: события приходят в порядке возрастания ID и отменённая подписка не может быть закрыта между копированием списка и отправкой (нет `send on closed channel`)
- Если подписчик не успел и событие выпало из его буфера, SSE-обработчик видит разрыв в ID и досылает пропущенное из кольцевого буфера
- Кольцо ограничено 256 событиями, поэтому разрыв может оказаться больше него. Тогда `closesGap` возвращает false, и обработчик отправляет клиенту событие `resync` с диапазоном пропущенного: страницы перечитывают состояние через REST вместо того, чтобы молча показывать устаревшие данные. Это честная деградация, а не «потерь не бывает»
- ID событий долговечны: `event_sequence` в SQLite выдаёт блоками, процесс продолжает нумерацию после перезапуска. Иначе браузер, помнящий ID прошлого запуска, отбросил бы все новые события как «старые», а `resync` отправлялся бы повторно
- `resync` несёт поле `id`: оно двигает курсор `EventSource`, поэтому повторное переподключение не сообщает тот же разрыв заново. Пустой replay — не разрыв: клиент может быть ровно на голове потока
- `catchUp` сбрасывает буфер в поток сразу после `resync`: иначе кадр ждал бы следующего события или heartbeat, и интерфейс оставался бы устаревшим
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
- Экраны: `/` (ассистент), `/today` (Сегодня), `/inbox` (Входящие), `/tasks` (Все задачи), `/goals` (Цели), `/calendar` (Календарь), `/memory` (Память и база знаний)
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
- `conversations` / `messages` — диалоги; `kind` разделяет главный чат и рабочие пространства (`purpose`, `source_conversation_id`)
- `pending_actions` — ожидающие подтверждения
- `app_settings` — key-value настройки
- `memory_entries`, `memory_sources`, `memory_links`, `memory_conflicts`, `memory_fts` — память, происхождение, связи и очередь противоречий

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