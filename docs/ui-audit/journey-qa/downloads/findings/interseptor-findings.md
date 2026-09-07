# Interseptor — Engagement Report

_1 finding: 1 High_

## Summary

| Severity | Count |
| --- | --- |
| High | 1 |
| **Total** | **1** |

| Status | Count |
| --- | --- |
| verified | 1 |

## High

### 1. Generic Findings journey 1788757764570921000
- **Status:** verified
- **Confidence:** firm
- **Tags:** generic, qa

**Summary:** Generic UI-only summary

**Impact:** Generic fixture impact

**Why:** Generic fixture reason

**Target:** `https://example.com/findings-journey`

**Reproduction & Evidence:**

_Step: role=observation._

Generic local reproduction step

_Evidence: role=result, source=captured_flow, source flow #1._

> `POST 127.0.0.1/fixture/request-response?example=1` → **200**
>

**Request**

```http
POST /fixture/request-response?example=1 HTTP/1.1
Accept-Encoding: identity
Content-Length: 16
Content-Type: application/json
Host: 127.0.0.1:52146
X-Interseptor-Audit: generic-loopback

{"fixture":true}
```

**Response**

```http
HTTP/1.1 200 OK
Cache-Control: no-store
Content-Length: 86
Content-Type: application/json
Date: Mon, 07 Sep 2026 05:09:24 GMT
Server: BaseHTTP/0.6 Python/3.9.6

{"ok":true,"method":"POST","path":"/fixture/request-response?example=1","received":16}
```

_Evidence: role=result, source=operator_upload._

![generic-fixture.png](/api/findings/images/d4afab195b2a5b89ec242453a62850ef99577a3aef891aa353de29cf1bb6b0bf)

**Remediation:** Generic remediation

**Retest:** Generic secure retest

