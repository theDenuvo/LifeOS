# LifeOS — UX patterns

## Information architecture

Primary navigation:

1. **Assistant** (`/`) — default workspace, conversation + context rail.
2. **Today** (`/today`) — daily briefing: focus, timeline, overdue, insights.
3. **Inbox** (`/inbox`) — processing queue for unprocessed captures.
4. **Tasks** (`/tasks`) — full task list, filters, inline actions.

Future modules (calendar, notes, goals, documents) become assistant capabilities and views, not disconnected mini-apps. They use the same header rhythm, tokens, and assistant integration.

## Assistant workspace

### Default experience

The assistant is the landing screen. The user should not have to find it.

- Header: “Ассистент”, short status, “Новый диалог”.
- Context strip: current screen/selected object, today’s state, inbox count.
- Transcript: turns, tool activity, rich result cards, pending actions.
- Context rail: today stats, focus, insights, quick links.
- Composer: always visible, multiline, context-aware.

### Turn anatomy

**User turn:** right-aligned, `--accent-surface`, role “Вы”, time. Keep it compact; the user’s words are not the main visual.

**Assistant turn:** full-width body, small role row, prose max 68ch. Do not wrap every answer in a bubble.

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

## Global composer

- Visible on every desktop screen in the sidebar and on mobile above the bottom navigation.
- Submitting asks the assistant with `interaction.screen` set to the current route.
- If already on the assistant route, it sends without navigation.
- On mobile, the composer is fixed above the nav and respects `safe-area-inset-bottom`.

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
