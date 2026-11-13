---
name: lifeos-ui
description: Design, critique, or implement LifeOS interface changes with the project's assistant-first visual system. Use for screens, components, CSS, layout, typography, color, responsive behavior, accessibility, assistant workspace patterns, and visual verification.
---

# LifeOS UI

Read these before changing UI:

1. `docs/design/UI_DIRECTION.md` — product/visual direction and composition.
2. `docs/design/DESIGN_SYSTEM.md` — tokens, typography, color, components, states.
3. `docs/design/UX_PATTERNS.md` — interaction and screen patterns.
4. `docs/design/UI_AUDIT.md` — known baseline problems.

Do not duplicate rules from those files here. They are the source of truth.

## Default stance

- LifeOS is an **Operate** surface: a daily working environment, not a landing page.
- The assistant is the primary interface. Conversation expresses intent; the workspace shows state.
- Medium density, dark, calm, precise. Violet is the assistant/brand accent; cyan is informational. No generic AI-SaaS decoration.
- Use shared tokens and components. A new hard-coded color, radius, shadow, or button style is a design-system change, not a local shortcut.

## Workflow

1. **Inspect** the route, its components, `app.css`, and related patterns. Reproduce the current screen at desktop and mobile widths.
2. **Diagnose** the user-facing problem: hierarchy, composition, density, typography, contrast, state coverage, interaction, or responsiveness. Do not start from “make it prettier”.
3. **Compose the whole screen**: header, primary content, rail/aside, composer/actions, empty/loading/error states, mobile reflow.
4. **Implement** with existing tokens/components. Extend the system deliberately and update `docs/design/` if the rule changes.
5. **Verify**:
   - `cd web && npm run check && npm run build`
   - real browser at 1440, 820, 390; check overflow, keyboard focus, reduced motion, long content
   - assistant flow: send, pending action, confirm/reject, error/retry, new conversation
6. **Critique** the rendered result against `UI_DIRECTION.md`. Fix what the critique finds before saying the task is done.

## Assistant-specific rules

- Default route is the assistant workspace.
- Show context, tool activity, pending actions, and rich results; do not reduce assistant work to chat bubbles.
- Never claim an action completed before confirmation.
- Keep the composer anchored and reachable; pass the current screen/object as interaction context.
- Context rail is supporting information, not a second dashboard.

## Quality gates

A UI change is not done if any of these fail:

- One clear primary action and visible current state.
- All colors/spacing/radii from tokens.
- Hover, focus-visible, active, disabled, loading, empty, error, long-content states.
- Keyboard path and AA contrast.
- Desktop/tablet/mobile checked visually.
- No duplicate component or page-local restyling of an existing pattern.

## Optional external reference

`Z:\Hermes\skills\impeccable` contains a compatible Agent Skill with deeper critique/audit material. Read it only as reference; do not copy its hooks, detector, or scripts into LifeOS. This project's own docs and this skill take precedence.
