# LifeOS — project instructions

## Product

LifeOS is a personal operating system whose primary interface is an AI assistant. The assistant is not a chat feature beside a task manager: it is the main way the user expresses intent, and the GUI is where the user sees and corrects the resulting state.

## Before any UI work

Read, in this order:

1. `docs/design/UI_DIRECTION.md` — product and visual direction.
2. `docs/design/DESIGN_SYSTEM.md` — tokens, type, color, components, states.
3. `docs/design/UX_PATTERNS.md` — interaction and screen patterns.
4. `docs/design/UI_AUDIT.md` — known baseline problems and priorities.

Then load the project skill with `/skill:lifeos-ui` when available, or read `.pi/skills/lifeos-ui/SKILL.md` directly.

Do not start editing because a screen “looks plain”. State the user-facing problem, the composition change, and the states affected. If the change introduces a new pattern, add it to the design docs or explain why it is a one-off.

## Non-negotiable UI rules

- The assistant is the primary surface. Default entry point and visual hierarchy must make that true.
- Conversation expresses intent; the workspace shows state. Do not turn LifeOS into a messenger.
- Use the shared tokens and components. No new hard-coded hex colors, gradients, radii, shadows, or one-off local button styles.
- Design the whole screen, not an isolated component. Check header, content, rails, composer, empty/loading/error states, and mobile composition together.
- Use semantic HTML, visible `:focus-visible`, keyboard paths, labelled controls, and touch targets of at least 44×44 CSS px where practical.
- Respect `prefers-reduced-motion`. Motion explains state change; it does not decorate.
- Test at desktop (≥1280), tablet (~820), and mobile (390). Check overflow, safe areas, and the composer with the keyboard open.
- Visual quality is a completion criterion. A compiling screen with correct data is not finished.

## Workflow for interface changes

1. Inspect the existing page, components, tokens, and related routes.
2. Reproduce the current state in a browser at desktop and mobile widths.
3. Write down the specific problems: hierarchy, density, rhythm, contrast, state coverage, interaction.
4. Choose the smallest coherent change that fixes the screen composition.
5. Implement with existing tokens/components; extend the system deliberately if needed.
6. Verify: `npm run check`, `npm run build`, browser screenshots, keyboard path, reduced motion, mobile.
7. Critique the result against `docs/design/UI_DIRECTION.md`; fix what the critique finds before reporting done.

## Scope discipline

- UI work must not change backend, API, database, or business logic unless the task explicitly requires it.
- Keep unrelated changes out of the branch. Do not rewrite a page that the task did not ask for.
- Preserve working functionality and existing design-preview assets. `design-preview/` is a reference archive, not production UI.

## Verification commands

```bash
cd web
npm run check
npm run build
```

Run the real app and inspect it in a browser when possible. Screenshots are evidence, not decoration: check the rendered result at the sizes it ships at.

## Design knowledge maintenance

`docs/design/` is the source of truth. When a token, component, or pattern changes, update the relevant doc in the same change. Do not duplicate the same rule across documents; link to the owning document instead.
