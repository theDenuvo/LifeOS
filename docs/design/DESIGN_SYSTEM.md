# LifeOS — design system

Tokens are CSS custom properties on `:root` in `web/src/app.css`. Components consume semantic tokens, never raw hex values. `app.css` carries the reference token set plus semantic aliases kept for older screens; extend it rather than adding page-local palettes.

## Color

### Primitives

| Token | Value | Use |
|---|---|---|
| `--ink-950` | `#0a0a0e` | app background |
| `--ink-900` | `#0a0a0d` | sidebar / deep surface |
| `--ink-850` | `#121216` | primary surface / rail card |
| `--ink-800` | `#17171d` | raised surface / task card |
| `--ink-750` | `#1d1d24` | hover surface |
| `--ink-700` | `#2a2a33` | strong hover / border emphasis |
| `--line` | `#24242c` | default border |
| `--line-soft` | `#1b1b22` | quiet divider |
| `--text-strong` | `#f2f2f5` | titles |
| `--text` | `#d8d8e0` | body |
| `--text-muted` | `#8f8f9c` | metadata |
| `--text-faint` | `#6f6f7d` | disabled / hints |
| `--violet` | `#7c5cfc` | assistant / brand |
| `--violet-bright` | `#9a83ff` | hover / active text |
| `--violet-soft` | `rgba(124,92,252,.14)` | selected surface |
| `--cyan` | `#22d3ee` | live / informational signal |
| `--success` | `#34d399` | done / positive |
| `--warning` | `#fbbf24` | attention |
| `--danger` | `#f87171` | error / destructive |

### Semantic aliases

```css
--bg-primary: var(--ink-950);
--bg-sidebar: var(--ink-900);
--bg-card: var(--ink-850);
--bg-raised: var(--ink-800);
--bg-hover: var(--ink-750);
--fg-primary: var(--text-strong);
--fg-body: var(--text);
--fg-muted: var(--text-muted);
--fg-faint: var(--text-faint);
--border: var(--line);
--border-soft: var(--line-soft);
--accent: var(--violet);
--accent-bright: var(--violet-bright);
--accent-surface: var(--violet-soft);
--accent-border: rgba(139, 92, 246, 0.28);
--on-accent: #0b0b10;
--info: var(--cyan);
--success: var(--green);
--warning: var(--amber);
--danger: var(--red);
--danger-text: #fecaca;
--danger-surface: rgba(248, 113, 113, 0.08);
--danger-border: rgba(248, 113, 113, 0.4);
--warning-surface: rgba(251, 191, 36, 0.06);
--warning-border: rgba(251, 191, 36, 0.35);
--success-surface: rgba(52, 211, 153, 0.12);
```

Rules:

- Body text uses `--fg-body`; metadata uses `--fg-muted`; disabled uses `--fg-faint`.
- Accent areas use `--accent-surface` plus `--accent` text/border. Do not use a gradient as a generic button fill.
- Semantic color is never decorative. A red border means error/destructive; a green dot means success/live.
- Test body text at 4.5:1 and large text at 3:1 against its actual surface.

## Typography

Fonts: **Inter** for UI, **JetBrains Mono** for IDs, counts, timestamps, keyboard hints, and technical labels. Until Inter is bundled locally, keep the system-sans fallback and do not add a blocking remote import.

| Token | Size / line height | Use |
|---|---|---|
| `--text-xs` | 11 / 1.35 | badges, mono metadata |
| `--text-sm` | 12 / 1.4 | captions, helper text |
| `--text-base` | 13 / 1.5 | dense UI, table rows |
| `--text-md` | 14 / 1.55 | body, controls |
| `--text-lg` | 16 / 1.45 | assistant prose, card titles |
| `--text-xl` | 18 / 1.3 | section titles |
| `--text-2xl` | 20 / 1.2 | screen titles |
| `--text-3xl` | 24 / 1.2 | assistant hero / empty state |

Rules:

- Screen title: `--text-2xl`, weight 650, letter-spacing `-0.02em`.
- Section label: `--text-xs`, uppercase, weight 600, letter-spacing `0.08em`, `--fg-muted`.
- Body line length: 60–75 characters. Assistant prose max-width ~68ch.
- Do not use more than three type sizes in one panel.
- Use `font-variant-numeric: tabular-nums` for counts and times.

## Spacing, radius, elevation

- Spacing scale: 4, 8, 12, 16, 20, 24, 32, 40, 48. Use `--space-1` … `--space-12`.
- Radii: `--radius-sm: 8px`, `--radius-md: 12px`, `--radius-lg: 16px`, `--radius-xl: 22px`, `--radius-full: 999px`.
- Cards/panels use `--radius-lg`; controls use `--radius-md`; pills use full.
- Default elevation is a border plus surface change, not a shadow. Shadows only for overlays/popovers.
- One-pixel borders: `1px solid var(--border)`. Quiet dividers: `--border-soft`.

## Layout

| Token | Value | Use |
|---|---|---|
| `--sidebar-w` | `200px` | desktop sidebar |
| `--rail-w` | `288px` | assistant context rail |
| `--content-max` | `1440px` | workspace max width |
| `--topbar-h` | `56px` | workspace column headers |
| `--mobile-nav-h` | `64px` | bottom navigation |
| `--composer-h` | `112px` | assistant composer area |

Breakpoints:

- `1280px`: full sidebar + context rail.
- `1360px`, `1600px`, `1920px`, `2560px`: the assistant reading column (`--chat-measure`) grows from 776 px to 856, 920, 1000 and 1120 px (+10 % and up). A transcript capped at 776 px wastes most of a large display; the measure grows only above these widths, so smaller laptops, tablets and phones keep the comfortable column.
- `1100px`: context rail moves below transcript.
- `820px`: sidebar becomes bottom navigation; content uses full width.
- `560px`: single-column controls, 16 px page padding, composer respects safe area.

Rules:

- Touch targets: 44×44 CSS px minimum for primary controls; 36 px only for dense desktop-only rows with equivalent spacing.
- No horizontal scrolling at 390 px.
- Use `env(safe-area-inset-*)` on fixed mobile chrome.

## Motion

- `--ease-out: cubic-bezier(.16,1,.3,1)` for entrances; `--ease-in-out` for state loops.
- `--duration-fast: 120ms`, `--duration-normal: 200ms`, `--duration-slow: 320ms`.
- Animate opacity/transform only. Never animate height/width for large panels.
- Interactive surfaces (buttons, chips, rows, nav items, inputs) transition `background-color`, `border-color`, `color`, `opacity` and `transform` at `--duration-fast`; focus and hover never snap.
- Message entry: 180 ms fade + 4 px rise. Cards, action cards and tool activity: `card-in` (200 ms fade + 6 px rise). Pending card resolution: 160 ms fade.
- Popovers and menus: `pop-in` (fade + 4 px rise + 0.98 scale) with `transform-origin` at the trigger edge.
- Scrolling: programmatic “jump to latest” uses smooth scrolling; streaming auto-follow stays instant so tokens do not lag behind the viewport.
- `@media (prefers-reduced-motion: reduce)` drops animation and transform transitions and keeps 60 ms color/opacity feedback; do not globally set `0.01ms` because focus/state feedback still matters.

## Components

### Button

- **Primary:** accent surface with `--accent` text or solid violet with dark text. One per view.
- **Secondary:** `--bg-raised`, `--border`, `--fg-body`.
- **Ghost:** transparent, border on hover, muted text.
- **Danger:** transparent with danger text/border; solid danger only for destructive confirmation.
- Height: 36 px desktop, 44 px mobile. Always visible `:focus-visible` ring.
- Disabled: `--fg-faint`, no pointer events, preserve layout.

### Icon button

- 36×36 desktop / 44×44 mobile. `aria-label` required. Tooltip for unfamiliar icons.

### Input / textarea / select

- `--bg-raised`, `--border`, `--radius-md`, 36–44 px height.
- Focus: `--accent` border + 2 px `--accent-surface` ring, never remove outline without replacement.
- Error: danger border + message below, not color alone.
- Native `<select>` renders its popup with the operating system palette; `:root` therefore sets `color-scheme: dark`. Prefer a token-styled listbox for pickers the user opens often.

### Model picker (assistant header)

- Compact trigger: 28 px height, `--bg-panel`, `--border`, truncated model name, chevron that rotates while open.
- Popup: `--bg-raised`, `--border`, `--radius-md`, `--shadow-overlay`, `min(340px, 100vw - 32px)` wide, max 60 vh with its own scroll; `pop-in` entrance anchored top-right.
- Options show the full model name with ellipsis and a check on the selected row; selected row uses `--accent-surface` and `--violet-bright`.
- Keyboard: trigger opens with `ArrowUp`/`ArrowDown`, options support arrows/`Home`/`End`, `Escape` closes and returns focus to the trigger, click outside closes.

### Listbox / popover (scrollable)

- Use the app's own listbox instead of a native dropdown when the list is long, needs token colors, or carries state (badges, checks).
- Always provide `role="listbox"`/`role="option"`, `aria-selected`, and a focus path that does not trap the keyboard.

### Panel / card

- `--bg-card`, `1px solid --border`, `--radius-lg`, padding 16–20.
- A panel has one job. Do not nest cards more than one level.
- Header row: title left, action right, 12 px gap.

### Badge / pill

- 20–24 px height, `--text-xs`, full radius. Variants: neutral, accent, success, warning, danger.
- Use for state, not decoration.

### Screen header

- Title + one-line subtitle; primary action on the right.
- Optional stats as small inline pills, not five equal cards.
- Bottom margin 24 px; no heavy underline.

### Empty state

- One clear message, one primary action, optional secondary hint.
- No illustration unless it carries product meaning.

### Assistant turn

- Role row: small mark, name, time. Assistant body full-width; user body right-aligned on `--accent-surface` with max-width 80%.
- Assistant prose uses `--text-lg` and 68ch max; code/tool output uses mono.
- Tool activity is a compact inline row, not a fake chat message.
- Pending action is a distinct card with consequence, confirm, reject.

### Context rail

- 320 px; sections separated by 24 px, not nested cards.
- Stats are a compact row, not five large tiles.
- One focus item; max 3 insights; links to Today/Inbox/Tasks.
- On mobile, becomes a collapsible summary above the transcript.

## State coverage

Every interactive surface needs: default, hover, focus-visible, active, disabled, loading, empty, error, and long-content behavior. Async actions must show progress and prevent double submission. Errors must say what failed and offer retry when safe.

## Accessibility baseline

- Semantic landmarks: `nav`, `main`, `header`, `section`, `ul/li`, `button`.
- Every input has a visible or programmatic label.
- Focus order follows visual order; no keyboard traps.
- Color is never the only signal.
- `prefers-reduced-motion` is honored.
- Assistant messages are in a live region; pending actions are reachable by keyboard.
- Target WCAG 2.2 AA.
