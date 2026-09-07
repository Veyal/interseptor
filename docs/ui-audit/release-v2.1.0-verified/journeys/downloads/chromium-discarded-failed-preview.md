# Interseptor — Engagement Report

_2 findings: 1 High, 1 Medium_

## Summary

| Severity | Count |
| --- | --- |
| High | 1 |
| Medium | 1 |
| **Total** | **2** |

| Status | Count |
| --- | --- |
| needs_verification | 1 |
| open | 1 |

## High

### 1. QA supplemental second finding
- **Status:** open
- **Readiness gaps:** summary, target, impact, why, evidence, fix, retest, confidence, action, result, control, execution
- **CVSS:** CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N
- **Calculated CVSS v4.0:** 8.7 (HIGH, CVSS-B)


## Medium

### 2. QA findings improvements
- **Status:** needs_verification
- **Impact verification:** not_executed
- **Verification limit:** Bounded loopback fixture; impact not executed.
- **Readiness gaps:** impact, why, fix, retest, confidence, action, control, execution, verification
- **CVSS:** CVSS:4.0/AV:L/AC:H/AT:P/PR:H/UI:P/VC:L/VI:H/VA:N/SC:N/SI:N/SA:N
- **Calculated CVSS v4.0:** 4.3 (MEDIUM, CVSS-B)

**Summary:** Rejected stale export text

**Target:** `https://example.com/records/1`

**Affected targets:**

1. `https://example.com/records/1`

- Methods: POST
- Relation: affected
- Role / prerequisite: anonymous
- Evidence flows: #2

2. `https://example.com/records/2`

- Methods: POST
- Relation: affected
- Role / prerequisite: anonymous
- Evidence flows: #1

**Reproduction & Evidence:**

_Evidence: role=result, proof=Generic loopback result., source=captured_flow, source flow #2._

> `POST 127.0.0.1/session-b` → **200**
>

**Request**

```http
POST /session-b HTTP/1.1
Accept-Encoding: identity
Content-Length: 16
Content-Type: application/json
Host: 127.0.0.1:54484
X-Interseptor-Audit: generic-loopback

{"generic":true}
```

**Response**

```http
HTTP/1.1 200 OK
Cache-Control: no-store
Content-Length: 61
Content-Type: application/json
Date: Mon, 07 Sep 2026 17:42:39 GMT
Server: BaseHTTP/0.6 Python/3.9.6

{"ok":true,"method":"POST","path":"/session-b","received":16}
```

_Evidence: role=result, proof=Generic loopback result., source=captured_flow, source flow #1._

> `POST 127.0.0.1/session-a` → **200**
>

**Request**

```http
POST /session-a HTTP/1.1
Accept-Encoding: identity
Content-Length: 16
Content-Type: application/json
Host: 127.0.0.1:54484
X-Interseptor-Audit: generic-loopback

{"generic":true}
```

**Response**

```http
HTTP/1.1 200 OK
Cache-Control: no-store
Content-Length: 61
Content-Type: application/json
Date: Mon, 07 Sep 2026 17:42:39 GMT
Server: BaseHTTP/0.6 Python/3.9.6

{"ok":true,"method":"POST","path":"/session-a","received":16}
```

