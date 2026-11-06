# UI declutter rules

The control UI is a workbench: dense is fine, noisy is not. This page is the
rulebook applied when a panel is reviewed, so a new control does not quietly
re-introduce the clutter the 2026-11 pass removed. The desktop panel skin
(`js/desktop-panel-*.js`) is the reference implementation for most of it — it
was written with these rules and the web surface is being brought in line.

## Rules

1. **One primary action per pane.** A pane gets at most one filled/primary
   button (Send, Start, Run scan, New finding, Attach). Everything else is a
   secondary or an overflow item.
2. **Secondary actions live in one overflow.** If a toolbar needs more than
   four visible controls, the rest go into a single `⋯` menu — not a second
   row, not a second menu, not both. Every moved control keeps its element id
   and handler so contracts and keyboard paths keep working.
3. **One entry point per concept.** Scope is opened from the header Scope chip.
   Environments are opened from the environment menu. If a panel offers a
   second button for the same sheet, delete the button and keep a text link in
   the menu ("Edit scope…", "Manage environments…").
4. **Filters are one button.** A filter bar is search plus a `Filters` button
   whose label carries the active count (`Filters · 2`). The popover holds the
   selects, toggles and chips; it stays open while toggling and closes on
   Escape/outside click. This applies on phone identically (a sheet instead of
   a popover).
5. **No duplicate create affordances.** One "New …" button. A square `+` beside
   a search field is not a create button unless it is the only one.
6. **No duplicate labels.** Two controls on one screen never share a label
   ("Apply listener changes" ×2 became "Apply proxy listener" / "Apply control
   UI listener"). The label states the scope of the change.
7. **State is shown once.** If a badge, a chip, a card and a status line all
   assert the same fact (health, mode, readiness), keep the one attached to the
   action that changes it and drop the rest.
8. **Metadata stays out of the way.** Authorship/provenance/timestamps belong in
   a tooltip or the activity log, not in a card that costs 90 px of a panel.
9. **Chrome budget.** Desktop: at most two chrome rows above content (toolbar +
   filter/search row). Phone: at most two rows total above content — panel
   switcher, then search + Filters. A banner is a row; it must be dismissible
   and collapse to one line on phone.
10. **Destructive actions sit inside the overflow**, visually marked, never as a
    bare toolbar button next to a positive action.
11. **Empty states are singular.** One empty state per pane, telling the next
    action; not three competing placeholders in a three-column layout.
12. **Reachability beats presence.** Anything removed from a rail or a toolbar
    must stay reachable from the command palette and, where it is a concept, from
    the header chip or Settings section that owns it.

## Inventory — 2026-11 declutter pass

| Area | Before | After |
|---|---|---|
| Repeater response pane | 8 actions over two rows (Attach, Save as proof, Diff, Copy as, Copy as PNG / Intruder, + Finding, Copy cURL) | 4 visible (Attach, Diff, Copy as ▾, ⋯) — proof/finding/intruder in the overflow, cURL/PNG inside Copy as |
| Findings header | "+ New finding" **and** a square `+` next to the search box, plus Writing guide / Deleted / Report as buttons | one primary + one overflow; the duplicate create is gone |
| History filter bar (desktop) | In scope only / Edit scope / Filters / Views / Pause / Cols as separate buttons | search + Filters (scope toggle and "Edit scope…" inside it); Pause stays |
| History (phone ≤640 px) | ~7 rows of chrome before the first flow | panel switcher + search + Filters sheet; first flow visible without scrolling; Pause moves to the panel header |
| Map | second toolbar row with six filter toggles/menus | one `Filters · n` popover; graph/table/params unchanged |
| Collections | env select + Variables + New environment + Environments (four env controls); Import/Export/Console/… on two rows | env select + `Environments ▾` + one collection `⋯`; single toolbar row |
| Identities | Provenance card (created / last written) + five health buttons in a card | provenance dropped from the pane (kept in the API), health actions in one `⋯` |
| Settings | two buttons both labelled "Apply listener changes" | "Apply proxy listener" / "Apply control UI listener" |
| Header (Flight Bar) | three unlabelled icons: split left/right, split top/bottom, close document | one labelled `Layout` menu holding the same three actions and states; the buttons stay in the DOM for their handlers |
| Collections (empty) | three competing empty states — tree card, "Select a request", "No response yet" — across three panes | one card in the pane that can act on it; the panes with nothing to show collapse, so an empty collection is a single full-width state |
| Findings header | mode chip read "Editing on" directly beside the `Edit` button | chip reads "Writing enabled" (it is the project-wide write gate, not this finding's edit mode) |

## Checklist for a new control

- Is there already a control that does this? (grep the panel + the palette)
- Which of the four visible slots does it take, or which overflow does it join?
- Does its label state its scope, or does another control share the label?
- Does it need a second row? Then it needs a menu instead.
- Is it reachable by keyboard, and does focus return to the trigger?
- Does the phone layout still show content within two rows?
