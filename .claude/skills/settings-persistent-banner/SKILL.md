---
name: settings-persistent-banner
description: Keep persistent alerts in Interseptor's embedded settings UI visible and correctly laid out while sections, search, and async loading state change. Use when adding or modifying settings warnings, banners, or global status messages in internal/control/ui/.
---

# Persistent settings alerts

Place an alert that must survive settings navigation, filtering, or load failures outside `.settings-wrap`. That container is a horizontal flex row containing the navigation rail and settings body; inserting a full-width alert inside it makes the alert a third row sibling and can distort the workspace.

Use this structure:

```html
<div class="panel" id="panel-settings">
  <div id="persistentAlert" class="settings-persistent-alert" role="alert">…</div>
  <div class="settings-wrap">
    …navigation and settings body…
  </div>
</div>
```

Give the alert `flex:none`, readable spacing, semantic severity colors from the existing CSS tokens, and a stable ID. Drive its `hidden` state from the same state loader that renders the setting it describes. On settings-load failure, show the alert conservatively so stale success state cannot hide a safety warning.

Required checks:

1. Assert the alert is outside `.settings-wrap`, has `role="alert"`, and remains present when another `.set-sec` is hidden or search filters settings.
2. Assert the loader sets the alert from the API value and forces it visible on load failure.
3. Run the embedded UI journey/contract tests and JavaScript syntax check.
