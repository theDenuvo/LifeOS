# LifeOS — UI direction

## Product idea

LifeOS is a personal operating system. The user states an intention in natural language; the assistant understands the state of the system, uses its capabilities, and the interface shows the result. The assistant is the primary interface, not a chat tab beside a task manager.

The interface must feel like a working environment used every day: calm, precise, fast, and trustworthy. It is not a landing page, a demo dashboard, or a generic AI SaaS template.

## Surface mode

**Operate.** The user is completing real work. Scanability, consistency, predictable placement, low friction, and truthful state outrank visual expression. Brand character lives in precise details, not in decoration.

## Visual world: refined Intelligent Command Center

Concept B is the starting point, not a pixel template. Keep its spatial confidence and assistant-centric composition; remove its demo excess.

- **Dark, low-noise base.** Near-black background, slightly lifted surfaces, one-pixel borders. No page-wide glow.
- **Violet is the assistant/brand accent.** Cyan is a secondary signal for information and live state. Semantic colors are reserved for meaning.
- **Spatial hierarchy.** The assistant workspace is a contained canvas: transcript, context rail, composer. Panels are separated by rhythm and surface, not by stacking cards everywhere.
- **Typographic contrast.** Strong screen titles, quiet metadata, readable body. Technical metadata uses JetBrains Mono.
- **Medium density.** Enough information to work without scrolling for every fact; no decorative filler.
- **Motion explains change.** Short fades/slides for messages and panels; no ambient animation loops in the working surface.

### Avoid

- Gradient-filled buttons on every action; gradients are for identity and one primary emphasis, not a default.
- Glassmorphism, neon outlines, glowing cards, floating orbs inside the workspace.
- A messenger layout where every assistant answer is a bubble and every result is prose.
- Card soup: five equal cards competing for attention.
- Icon-only controls without labels or tooltips.
- Fake precision: decorative charts, stats, or badges that do not change a decision.
- Marketing copy inside the product.

## Composition

### App shell

- Persistent left sidebar on desktop (240–260 px), collapsed to a bottom navigation on mobile.
- Main canvas is the workspace, not a centered marketing column.
- The assistant is the default route. “Today”, “Inbox”, and “Tasks” are views the assistant can also operate on.
- The global composer remains reachable from every screen; it opens the assistant with the current screen as context.

### Assistant workspace

Desktop:

```text
┌ sidebar ┬──────────────── assistant canvas ────────────────┬ context rail ┐
│ nav     │ header: title · status · new conversation        │ today stats  │
│ ask     │ context chips                                    │ focus        │
│         │ transcript                                       │ insights     │
│         │ pending action cards                             │ quick links  │
│         │ composer                                         │              │
└─────────┴──────────────────────────────────────────────────┴──────────────┘
```

- Transcript is the dominant column. It should read as a sequence of turns, not a chat widget.
- Context rail is a calm, scannable summary of the user's system. It collapses below the transcript on tablet/mobile.
- Composer is visually anchored and always available; it is the primary input, not a secondary form.
- The assistant's identity is present but restrained: small mark, role label, live status.

### Other screens

- “Today” is the daily briefing: focus, timeline, overdue, insights.
- “Inbox” is a processing queue, not a second chat.
- “Tasks” is a working list with filters and inline actions.
- All screens share the same header rhythm, tokens, and control styles.

## Assistant-native interaction

The assistant must show more than text:

- **Context:** current screen, selected object, today’s state.
- **Tool activity:** what the assistant is checking or changing, in human language.
- **Pending actions:** a clear proposal with consequence, confirm/reject, and no claim of completion before confirmation.
- **Rich results:** task/event cards, schedules, tables, and links, not only paragraphs.
- **States:** thinking, streaming, success, empty, error, retry, cancel (as capabilities land).
- **Control:** stop a run, reject a proposal, start a new conversation, return to an earlier one.

Conversation is where intent is expressed. The workspace and cards are where state is understood.

## Visual quality bar

A screen is finished when:

1. The primary action and current state are obvious in under three seconds.
2. The eye moves title → primary content → supporting context without competing focal points.
3. Every color, radius, and spacing value comes from the system.
4. Empty, loading, error, disabled, and long-content states are designed.
5. Desktop, tablet, and 390 px mobile layouts are checked in a browser.
6. Keyboard focus is visible and the main flow works without a mouse.
7. The assistant still feels central after the screen is populated with real data.
