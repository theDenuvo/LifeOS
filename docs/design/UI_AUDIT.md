# LifeOS — UI audit baseline

Date: 2026-11-12. Target: `web/src` (Today, Inbox, Tasks, Assistant, shell). Method: source review, Concept B comparison, rendered desktop/mobile screenshots. Automated Impeccable detector was not run; its scripts/hooks are not part of the project. This is the baseline the redesign must improve.

## Health score

| # | Dimension | Score | Key finding |
|---|---|---|---|
| 1 | Accessibility | 2/4 | Muted text fails 4.5:1 on both app and card surfaces; no global `:focus-visible`; no reduced-motion handling |
| 2 | Performance | 3/4 | Small app, no heavy assets; but no skeletons, and pages reload everything on each SSE revision |
| 3 | Theming | 1/4 | 9 root tokens; 20 hard-coded color values in the assistant page, 21 in Today, 17 in the shell |
| 4 | Responsive | 2/4 | Layout works, but five different breakpoints (760/720/640/560) and composer/nav collisions on mobile |
| 5 | Implementation integrity | 2/4 | Repeated local `.page-header`, `.banner`, `.primary`, `.ghost`, `.empty` styles; assistant reads as a messenger |
| **Total** | | **10/20** | **Acceptable — significant work needed** |

## What works

- Semantic page structure (`header`, `main`, `nav`, `section`, `ul/li`) is mostly sound.
- Mutations have visible pending/confirmation states; task completion and inbox conversion are clear.
- Dark palette is coherent at a glance; violet/cyan identity is recognizable.
- The assistant already receives system context and screen/object context.
- Mobile bottom navigation and safe-area handling exist.

## Priority findings

### [P1] The assistant is a tab, not the center

**Evidence:** `/` is Today; Assistant is the fourth sidebar item and a separate route. The Assistant screen is a narrow bubble transcript with no context rail, tool activity, rich result cards, or persistent system state.

**Impact:** The product still reads as “task manager with a chat page”. The user must choose the assistant instead of the assistant being the default way to operate LifeOS.

**Fix:** Make `/` the assistant workspace. Put Today at `/today`. Give the assistant a transcript + context rail + anchored composer. Keep global ask on every screen.

### [P1] Design tokens are nominal, not a system

**Evidence:** `app.css` defines 9 tokens. Pages hard-code accent gradients and semantic colors (`#8b5cf6`, `#06b6d4`, `#fca5a5`, `#fcd34d`, `#86efac`, `#f87171`, `#fbbf24`, `#38bdf8`). Every page redefines `.page-header`, `.banner`, `.primary`, `.ghost`, `.muted`, `.empty`.

**Impact:** Small changes drift immediately; contrast and spacing are inconsistent; a redesign cannot be applied globally without touching every page.

**Fix:** Extend `app.css` with semantic tokens from `DESIGN_SYSTEM.md`. Introduce shared primitives (`Button`, `Badge`, `Panel`, `ScreenHeader`, `EmptyState`, `Banner`) and migrate pages incrementally.

### [P1] Muted text is below WCAG AA

**Evidence:** `--fg-muted: #71717a` on `#0a0a0f` = **4.09:1**, on `#12121a` = **3.86:1**. This token is used for subtitles, metadata, timestamps, helper text, and disabled labels.

**Impact:** Normal-size metadata fails 4.5:1, especially on card surfaces. Users with low vision or low-quality displays lose context.

**Fix:** Use `--text-muted: #9292a3` on cards (6.09:1) for normal text; reserve `#71717a` for large text or non-essential decoration. Verify all variants with measured contrast.

### [P1] Keyboard focus is incomplete

**Evidence:** No `:focus-visible` rule in `app.css`; only inputs/textareas/selects receive a custom focus ring. Buttons, links, checkboxes, and the bottom navigation rely on browser defaults or lose focus styling.

**Impact:** Keyboard users cannot reliably see where they are; the task completion checkbox and assistant controls are especially exposed.

**Fix:** Global `:focus-visible` ring using `--accent` + `--accent-surface`, with `outline-offset: 2px`. Never remove outline without a replacement. Test Tab/Shift+Tab through every screen.

### [P2] Typography has no real scale or font loading

**Evidence:** `font-family: 'Inter'` is declared but Inter is never imported or bundled. Only JetBrains Mono has an `@font-face` pointing to `local()`. Headings are one-off `1.9rem`; metadata ranges across 0.68–0.9rem without tokens.

**Impact:** Rendering depends on whatever the OS has; hierarchy is inconsistent across machines. On Windows, the UI likely falls back to Segoe UI rather than the intended Inter.

**Fix:** Bundle Inter locally or accept and document the system fallback. Add the type scale from `DESIGN_SYSTEM.md`; use consistent 11/12/13/14/16/20/26 sizes.

### [P2] The assistant transcript has no AI-native states

**Evidence:** `assistant/+page.svelte` renders `.bubble` messages, hides tool messages, and shows pending actions as a single amber card. There is no tool timeline, no rich result card, no streaming/cancel state, no context chip, no conversation rail.

**Impact:** Assistant work is opaque and text-only. The user cannot see what was checked or changed, and complex results (schedule, list, comparison) collapse into prose.

**Fix:** Design turns, tool activity rows, rich result cards, pending-action cards, context chips, and loading/error/retry states in `UX_PATTERNS.md`; implement incrementally with the assistant workspace redesign.

### [P2] Responsive rules are fragmented

**Evidence:** Breakpoints: shell 760, tasks 720, assistant 640, task row 640, inbox 560. Mobile composer sits above the bottom nav while content padding is `8rem`; the assistant page has no dedicated mobile layout.

**Impact:** Layouts behave differently page to page; the composer/nav/safe-area relationship is fragile, especially with the keyboard open.

**Fix:** Standardize on 1280/1100/820/560. Treat 390×844 as the primary mobile test size. Put the mobile composer and bottom nav in the shell, not per page.

### [P2] Generic loading and error treatment

**Evidence:** Loading is a single “Загружаю…” line; errors are repeated red banners with a close button; empty states are centered text with no primary action.

**Impact:** The app feels unfinished during normal network/LLM latency and gives no recovery path for empty screens.

**Fix:** Skeletons for transcript/rail/task rows; error banners with retry; empty states with one clear action; disable double submission and preserve drafts.

## Concept B gap

Concept B supplied a richer vocabulary: spatial content surface, screen header with stats, timeline, command bar, context panel, restrained ambient depth. The implementation kept the colors and some cards, but dropped the composition. It also copied the demo’s gradient/glow language into buttons without the surrounding hierarchy.

The redesign should keep Concept B’s spatial confidence and assistant-centric intent, but use fewer effects and more precise typography, spacing, and state design.

## Redesign order

1. Make Assistant the default route and give it a transcript + context rail + anchored composer.
2. Introduce semantic tokens and shared primitives in `app.css`; migrate the assistant shell first.
3. Add assistant-native states: tool activity, pending cards, rich results, loading/error/retry.
4. Migrate Today, Inbox, Tasks to shared header/button/panel/empty patterns.
5. Standardize responsive behavior and keyboard/focus treatment.
6. Re-audit with measured contrast and browser screenshots at 1440, 820, 390.
