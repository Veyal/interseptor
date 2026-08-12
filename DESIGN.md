# Interseptor Documentation Design System

## 1. Atmosphere & Identity
Editorial field manual for a security tool: warm paper, ink-black structure, orange signal accents, and monospaced operational labels. Signature: the orange `I` marker and dense reference cards that make technical prose feel navigable, not decorative.

## 2. Color
| Role | Token | Value | Usage |
|---|---|---|---|
| Paper | `--paper` | `#f5f4ef` | Page surface |
| Ink | `--ink` | `#10151d` | Text, terminal, primary action |
| Muted | `--muted` | `#68717c` | Supporting copy |
| Line | `--line` | `#d9d9d1` | Dividers and cards |
| Signal | `--accent` | `#d95835` | Focus, labels, brand marker |
| Link | `--blue` | `#2156a5` | Navigational links |

## 3. Typography
- Display/headings: Georgia, serif; fluid `clamp()` scale.
- Body/navigation: ui-sans-serif system stack.
- Operational labels/code: ui-monospace system stack.
- Body minimum: 16px; labels may use 11–12px.

## 4. Spacing & Layout
- Base unit: 4px. Content uses `--space` fluid page padding.
- Desktop shell: 230px documentation rail plus max 900px reading column.
- Mobile breakpoint: 800px, sidebar becomes horizontal scroll navigation.
- Content remains intrinsic and readable at 200% zoom.

## 5. Components
### Documentation shell
- Structure: skip link, sticky header, primary nav, sidebar nav, main, footer.
- States: default, hover, keyboard focus, reduced-motion.
- Accessibility: native links, labelled landmarks, visible focus, skip target.

### Search
- Structure: labelled native search input and generated result links.
- States: empty, query results, no results, keyboard focus.
- Accessibility: no false combobox role; results are ordinary links and remain usable without JavaScript.

### Source card
- Structure: labelled canonical-source panel and action link.
- States: default, hover, focus.

## 6. Motion & Interaction
Only short color/background transitions on interactive controls. Reduced motion removes smooth scrolling. No decorative animation.

## 7. Depth & Surface
Mixed: one-pixel lines for structure, tonal paper panels for grouping, dark terminal block for contrast. No shadows.

## 8. Accessibility Constraints & Accepted Debt
Use semantic HTML, keyboard navigation, 3:1+ focus indicators, readable contrast, 44px interactive targets, no horizontal overflow at mobile widths, and no content hidden for scoring. Accepted debt: visual QA uses local static rendering because browser automation was explicitly unavailable for this task; CI validates generated output and links instead.
