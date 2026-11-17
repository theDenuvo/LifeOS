# LifeOS — UX patterns

## Information architecture

Primary navigation:

1. **Assistant** (`/`) — default workspace, conversation + context rail.
2. **Today** (`/today`) — daily briefing: focus, timeline, overdue, insights. Reached from the rail and the secondary links.
3. **Inbox** (`/inbox`) — processing queue for unprocessed captures. Reached from the secondary links and mobile navigation.
4. **Tasks** (`/tasks`) — full task list, filters, inline actions.
5. **Calendar** (`/calendar`) — month grid plus the selected day's tasks. Reached from primary navigation.
6. **Memory** (`/memory`) — what LifeOS knows about the user and the accumulated knowledge base, with provenance. Reached from primary navigation.
7. **Goals** (`/goals`) — long-term intentions, their plans and factual progress. Reached from primary navigation.
8. **Automations** (`/automations`) — approved recurring scenarios: trigger, steps, expected result, consequences and the run journal. Reached from primary navigation.

Future modules (documents) become assistant capabilities and views, not disconnected mini-apps. They use the same header rhythm, tokens, and assistant integration.

## App shell sidebar

- Workspace row: “Рабочее пространство” and the collapse control.
- Assistant identity: circular mark and “Ассистент” label.
- Primary action: “+ Новый диалог” (accent surface, full width).
- Primary navigation: Чат, Задачи, Календарь.
- Recent dialogs from `GET /assistant/conversations`; selecting one loads it in the assistant.
- Secondary links to Сегодня and Входящие stay available without adding more primary items.
- Calendar route replaces the recent list with “Мои календари” and the assistant hint.
- Profile card at the bottom; identity is a placeholder until the settings API exposes it.
- Collapsed state: 64 px icon rail, labels and secondary sections hidden.

## Assistant workspace

### Default experience

The assistant is the landing screen. The user should not have to find it.

- Header: “Личный ассистент”, sync line, in-dialog search, and the conversation menu.
- Context: the current screen is shown as a chip in the composer when the workspace was opened with `from`; there is no separate context strip.
- Transcript: turns, rich result cards, pending actions.
- Context rail: “Сегодня” progress and day plan, then the week’s open tasks with day links. Focus and insights live on Today, not in the rail.
- Composer: always visible, multiline, context-aware; attach/voice affordances are present but disabled until the backend supports them.

### Streaming and assistant controls

- Answers stream token by token; a cursor shows generation, and `Остановить` cancels it while keeping the partial response.
- Reasoning is a separate collapsible block (`Размышления`), open while streaming and collapsed in history.
- Tool activity is shown as quiet rows (`Проверяю задачи…` → done), not as fake chat messages.
- The model picker lists free tool-capable models from the configured provider and persists the choice.
- Auto-confirm is an explicit setting with a warning. When off, mutations remain pending cards; when on, the tool row shows completion and the transcript records the result.
- Copy is available per assistant answer; smart scrolling only follows the stream when the user is already near the bottom.

### Turn anatomy

**User turn:** right-aligned, `--accent-surface`, role “Вы”, time. Keep it compact; the user’s words are not the main visual.

**Assistant turn:** full-width body, small role row, prose fills the transcript column (about 90ch at 13 px). Do not wrap every answer in a bubble.

**Tool activity:** a single quiet row such as “Проверяю календарь…” with status icon. It can expand to arguments/result for debugging, but the default is human language.

**Pending action:** distinct card:

```text
Действие · create_task
Создать задачу «Купить молоко» на завтра, 13 ноября
[Подтвердить] [Отклонить]
```

Never say “готово” before confirmation. After confirm, show the resulting object as a card and a quiet success state.

**Rich result:** task/event card, schedule/table, or list. The assistant should not describe a schedule only in prose when a compact schedule card is possible.

### Empty state

One short question: “Чем займёмся?”. Four intent suggestions:
- “Что у меня сегодня?”
- “Разбери входящие”
- “Создай задачу…”
- “Помоги расставить приоритеты”

Below: one line explaining that the assistant reads the system and asks before changes. No long onboarding.

### Errors and recovery

- Provider unavailable: “Ассистент сейчас недоступен” + retry + settings hint.
- Context unavailable: assistant must say it cannot see current state; do not guess.
- Tool failure: show failed action, reason, and retry/reject. Keep the conversation intact.
- Pending action stale: explain that the object changed, offer to re-read and propose again.
- Stop/cancel: when streaming exists, a visible “Остановить” control; keep partial text.

### Conversation management

- “Новый диалог” always available.
- Conversation history is a future rail/drawer; do not turn the workspace into a messenger list.
- Each conversation keeps its title and last activity.
- Starting from another screen passes that screen as context; the transcript shows a small “из /tasks” chip.

### Project workspaces

A long topic can live in a project workspace: a separate conversation with its own history and purpose. The main chat remains the only everyday surface — there is no “Проекты” nav item.

- Sidebar: workspaces live in a “Рабочие пространства” section under the recent dialogs, marked with a layers icon; “Новое пространство” opens a two-field inline form (title, topic) and navigates into the created conversation.
- Workspace header: the conversation title replaces “Личный ассистент”, a chip names the space and its topic, and a “В главный чат” button returns to the latest main conversation.
- Workspace card: when the assistant creates or routes work into a space, the main-chat transcript shows a compact card (title, purpose, “Открыть”) — the same visual family as task cards.
- Handoff result: a result reported back from a workspace appears in the main chat as an assistant message with a badge (“Результат из пространства …”), including an “Открыть” action.

## Global composer

- Visible on every desktop screen in the sidebar and on mobile above the bottom navigation.
- Submitting asks the assistant with `interaction.screen` set to the current route.
- If already on the assistant route, it sends without navigation.
- On mobile, the composer is fixed above the nav and respects `safe-area-inset-bottom`.

## Document intake

- The composer paperclip and image buttons upload a file and convert it to Markdown; the document is not interpreted for tasks at upload time.
- After conversion the document is attached to the next message as a chip. Attachments are visible in the user turn and survive reloads.
- The assistant receives document metadata and a `read_document` tool. It reads Markdown in chunks and decides which tasks to create during the conversation.
- Auto-confirm applies to the resulting task mutations exactly like any other assistant action.
- Conversion failure shows the actual reason and never claims tasks were found.
- Supported files: DOCX, XLSX, XLS, PPTX, PDF (with `pdftotext`), text formats, and images via a free vision model. Legacy `.doc/.ppt` are rejected with a conversion hint.

## Calendar

- Header: month, month picker, previous/today/next, “Создать задачу”.
- Month grid: Monday-first weeks, day numbers, task chips from task dates; selected day outlined in accent; legend for task/overdue/completed.
- Day panel: selected date, open/completed counts, daily progress, grouped task rows with completion control, priority, and time; “Добавить задачу на этот день”.
- Footer: assistant sync line and “показано N из M”.
- Calendar events (meetings) require a dedicated events model; until it lands the grid renders tasks only.

## Memory

Memory is the screen where the user audits what the assistant knows, not a second notebook.

- **Header:** “Память”, one line explaining that everything is shown with sources, and one primary action “Добавить сведение”.
- **Tabs:** Личное / База знаний / Противоречия, each with a count. The conflict tab shows a warning count only when action is needed; it is never the default tab.
- **Search:** one field over title, content and entities (`FTS5` prefix search). Total count sits beside it as metadata, not a card.
- **Entry:** category label, certainty badge (`факт` / `предположение` / `вывод`), entities, title, content. Disputed entries carry a warning border and a “ждёт решения” badge.
- **Provenance:** collapsed behind a source control showing the source count. Expanded, each source shows kind, label, date and excerpt. A note explains that edits create a new version and keep the previous wording in history.
- **Editing:** inline title/content fields with save/cancel. Delete is an icon button with a destructive hover, never a primary action.
- **Conflicts:** the reason, both claims side by side with their own provenance, then “Оставить прежнее”, “Принять новое”, “Оба верны”. The losing claim stays as a superseded version, so the screen never implies data was erased.
- **Empty states:** explain the workflow and offer the next step. Empty knowledge explains that processed documents are indexed automatically.

Rules:

- Certainty is always visible for a claim; a fact and an inference must never look the same.
- Never present memory as complete or certain because it is stored; the source and confidence are part of the entry.
- A conflict is a decision for the user. The screen never resolves ambiguity silently.
- Deleting or rewriting memory requires the explicit control in this screen; assistant writes go through confirmation like any other mutation.

## Today

- Header: date + one-line state.
- Quick capture is a single row, not a form.
- Insights are actionable: explain, then “Завтра” / “Скрыть”.
- Focus is the one loud element; everything else is supporting.
- Today and overdue tasks use the shared TaskRow, not a local list style.

## Inbox

- Capture first; processing second.
- Each item: text, age, “В задачу”, “Удалить”.
- Converting shows a success notice with a link to the created task.
- Empty state explains the workflow in one sentence.

## Tasks

- Search and filters are one row on desktop; stacked on mobile.
- Row: completion control, title, metadata, actions. No hidden critical actions.
- Destructive actions require confirmation or an undo.
- Pagination is stable and announces the current range.

## Cross-screen principles

1. **State is visible.** Counts, overdue, inbox, and assistant activity are never hidden behind a menu.
2. **One primary action per screen.** Secondary actions are ghost/quiet.
3. **Context travels.** Screen and selected object are passed to the assistant; the assistant may use them but must verify with tools.
4. **Confirmation is proportional.** Low-risk reversible actions may be immediate; destructive or schedule-changing actions require confirmation. Future: autonomy settings per action class.
5. **Results are objects.** Show the task/event/note card, not only a sentence about it.
6. **No dead ends.** Every empty/error state offers a next step.
7. **No surprise navigation.** A global ask opens the assistant; it does not silently replace the current screen.

## Keyboard

| Key | Action |
|---|---|
| `Ctrl/Cmd + K` | focus global ask (future command palette) |
| `Enter` | send message |
| `Shift + Enter` | newline |
| `Esc` | close transient panel / dismiss error |
| `Tab` | visible focus order; no traps |

## Responsive behavior

- **≥1280:** sidebar + assistant transcript + context rail.
- **1100–1279:** sidebar + transcript; rail below transcript, collapsible.
- **820–1099:** bottom navigation; transcript full width; rail as a summary block.
- **<820:** bottom navigation, global composer above nav, 16 px page padding, 44 px targets.
- **<560:** single-column controls; no horizontal scroll; assistant prose uses `--text-base`/`--text-md`, not hero sizes.

## Accessibility

- Live region for assistant messages and pending actions.
- Tool activity rows announce state changes, not every token.
- Focus moves to the new pending action card when it appears; focus returns to the composer after resolution.
- Buttons have text or `aria-label`; icon-only buttons have tooltips.
- Color contrast AA; reduced motion respected.
- Mobile composer must not be covered by the on-screen keyboard; test at 390×844 with keyboard open.
