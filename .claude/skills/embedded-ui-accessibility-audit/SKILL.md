---
name: embedded-ui-accessibility-audit
description: Audit Interseptor's generated static UI for accessible names, unique IDs, composite-widget semantics, and keyboard focus after JavaScript has rendered the real interface.
---

# Embedded UI accessibility audit

Use this when changing navigation, filters, dense selectable lists, generated controls, dialogs, or repeated status banners in `internal/control/ui/`.

## Inspect the rendered UI

Source markup is not sufficient because `core.js` enhances native selects and feature modules generate rows, tabs, banners, and actions. Run an isolated binary and inspect every panel after its data and empty states render.

Check the browser accessibility tree as well as the DOM:

- IDs remain unique after repeated components render in more than one panel.
- Every visible control has a task-specific accessible name. Do not rely on an example placeholder or an ID-derived fallback such as `f Method`.
- Symbol-only controls have an `aria-label`; a bare `×`, `✕`, `+`, or abbreviated column title is not a sufficient task name.
- Every `aria-labelledby`, `aria-describedby`, `aria-controls`, and `aria-activedescendant` reference resolves to an existing unique element.
- Dialogs expose `role="dialog"`, `aria-modal="true"`, a real label, contained Tab focus, Escape dismissal, and focus return.

## Composite widgets

Choose one standard focus model per widget and implement its complete keyboard contract.

- A `listbox` owns `option` elements, not `button` rows. Use either roving DOM focus among options or keep focus on a combobox with `aria-activedescendant`; do not mix both models.
- Tabs keep exactly one `tabindex="0"`, synchronize `aria-selected`, and support the orientation's arrows plus Home and End.
- Avoid nested interactive controls. If a selectable tab or row also has a Delete/Close action, make them sibling controls with separate names and focus targets.
- When JavaScript replaces a selected or focused row, restore focus to the equivalent live element without scrolling it unexpectedly.

## Regression checks

Add a failing Go UI contract before the fix, then verify the real browser state at desktop and narrow widths. At minimum assert:

- no duplicate rendered IDs;
- no broken ARIA references;
- expected role ownership for generated listboxes/options;
- explicit names for compact filters and destructive/symbol-only actions;
- keyboard navigation, focus visibility, and reduced-motion behavior remain intact.

Keep visual density and existing shortcuts. Accessibility changes must clarify the same workstation workflow, not add explanatory clutter or move focus for presentation.
