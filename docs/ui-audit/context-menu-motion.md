# Context menus, tag colors and interaction motion

This focused follow-up addresses the two reported screenshots and adds restrained
interaction feedback. The earlier [menu and journey audit](journey-qa.md) keeps
its original source identity; its coverage does not substitute for testing these
changes.

## Design direction

The existing Interseptor interface remains the design system: local UI/monospace
font stacks, neutral raised surfaces, semantic green accent, 4/8px spacing, and
the existing radius and shadow tokens. No runtime dependency or remote asset is
needed for this pass.

The requested ui-ux-pro-max skill and Garden's
[Web Design Engineer](https://github.com/ConardLi/garden-skills/blob/main/skills/web-design-engineer/SKILL.md)
informed a preserve-mode update. Calibration: variance 2, motion 3, density 8,
asset dependence 2, brand fidelity 9. In practice, that means familiar compact
layouts with short feedback tied to an action, rather than decorative loops.

| Interaction | Result |
| --- | --- |
| Context menu / custom dropdown | Measure and place first, then enter over 120ms with a 3px movement from the opening edge. Dismissal remains synchronous. |
| Dropdown caret | Rotate over 120ms to reflect expanded state. |
| Navigation, section links and menu rows | Transition selection/hover colors over 120ms. |
| Button press | Brief inset shadow without changing control geometry. |
| Settings section / Activity detail | One 180ms content reveal; the Settings heading does not animate separately. |
| Enabled toggle indicator | Steady state; no continuous idle pulsing. |
| Reduced motion | The existing global override removes animation and transitions. Content and actions remain immediately available. |

## Repairs

- Context-menu labels and secondary values have separate columns. Labels wrap;
  secondary values can ellipsize. Each opening recomputes its own width and its
  wrapped height, so a previous short menu cannot constrain a later long one.
- Tag preset actions persist API-compatible hex values. Known presets render
  through the appropriate theme color, including the green accent token. Custom
  saved hex colors remain supported.
- Tag menus support keyboard opening and restore the invoking chip's focus.
  An acknowledged color refresh preserves that focus when rebuilding the bar.
- A context-menu opening or focus transfer clears the previous control's hint.

## Verification

The API regression executes every actual palette action against the real tag
endpoint and checks stored values. The original palette failed all seven presets
with HTTP 400; the corrected presets and Clear pass.

The [manifest](context-menu-motion/manifest.json) binds the executed probes,
reports and 13 final-runtime screenshots to
`5f8866062d1f1b61e84cd7d84b3caca5488b09c26e2913560fb33b18e8ea1c2f`
across 257 runtime files. Five earlier screenshots retain the original failing
palette and narrow-menu layout for comparison.

- Chromium, Firefox and WebKit each exercise all seven presets and Clear:
  24 actual HTTP 204 responses with saved values verified after reload.
- Menu text Range bounds stay within their rows and separate from secondary
  values. Phone, desktop, reduced-motion phone and additional Chromium tablet
  and landscape captures cover constrained menu placement.
- Keyboard menu opening, three Escape/focus-return repetitions, and Enter to
  save a palette choice verify that the refreshed chip retains focus.
- Normal menu entrances use 120ms; reduced-motion menus have no animation.
  Chromium also checks the custom dropdown, 180ms Settings/Activity reveals,
  steady toggle indicators, and all ten navigation tabs in normal/reduced mode.
- Persisted green chips resolve to the appropriate light/dark theme colors in
  all three engines. The API test covers every preset's stored value.

The [Chromium/Firefox report](context-menu-motion/reports/chromium-firefox.json)
retains an intermediate harness error: WebKit cannot provide a response body for
that empty HTTP 204. The separate [WebKit follow-up](context-menu-motion/reports/webkit.json)
passes the remaining checks without requesting that nonexistent body. The
original executed probe is retained alongside the corrected probe; no earlier
evidence has been relabeled as a later execution.

These are focused UI checks using generic fixtures in disposable local
candidates. Activity details use a labeled fixture response. Every owned
candidate was cleaned up; this follow-up does not repeat the earlier full menu
and operational coverage matrix.
