---
id: reflected-xss
title: Reflected XSS in HTML responses
applies_to:
  - mime:text/html
  - param:q
  - param:search
  - param:redirect
  - param:error
severity_hint: Medium
tools:
  - send_request
  - get_flow
  - add_finding_image
  - create_finding
evidence: >-
  A real browser screenshot of the alert executing in the target's origin, not
  the reflected markup in the response body.
references:
  - OWASP WSTG-CLNT-01
  - CWE-79
---

# Reflected XSS

Echoed markup is not execution. A finding needs the payload running in a
browser, in the target's origin, under the same headers the app serves.

## Do it

1. Find reflected input: search terms, error messages, redirect targets, page
   names, anything that comes back in an HTML body.
2. Note the context the value lands in — attribute, script block, JSON inside a
   script, or plain text — because it decides the payload.
3. Send a context-breaking payload, e.g. `"><img src=x onerror=alert(1)>` in an
   attribute, or `</script><script>alert(1)</script>` inside a script block.
4. Load the **response URL** in a real browser (not the raw body), so the app's
   own CSP, cookies and origin apply, and capture the alert with a screenshot.
5. If the markup is escaped but an attribute or JS sink still interpolates it
   unescaped, try the encoding-free variant (`javascript:` in an href, a
   `data:` URL, or a DOM sink in the app's own bundle).

## Evidence

Attach the executed alert screenshot plus the triggering request flow. State the
browser and the exact URL. A response body containing the payload is not
evidence of execution and must not be filed as visual proof.

## Escalation

Reflected XSS becomes High when it runs without interaction in a privileged
origin, or when it can read a token the app stores in a readable place. Say
which, and only if you demonstrated it.
