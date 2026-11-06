---
id: price-tampering
title: Price, quantity and total tampering in checkout flows
applies_to:
  - path:/order
  - path:/checkout
  - path:/cart
  - path:/payment
  - param:amount
  - param:price
  - param:quantity
severity_hint: High
tools:
  - list_flows
  - send_request
  - diff_flows
  - create_finding
evidence: >-
  The edited request plus a server response that accepts the fabricated value
  (an order confirmation, a payment intent, a receipt) and the baseline pair.
references:
  - OWASP WSTG-BUSL-01
  - CWE-472
---

# Price and quantity tampering

The server must price the basket. If it accepts a price, subtotal, discount or
total produced by the client, an attacker buys at that price.

## Do it

1. Capture a checkout request and identify every monetary field: unit price,
   line total, subtotal, tax, shipping, discount, coupon, currency, quantity.
2. Establish the baseline: the unmodified request and its response, so a
   rejection later cannot be confused with a control that always rejected.
3. Edit **one** field at a time — a negative price, a zero price, a tiny
   quantity with a large total — and send.
4. Watch the *response and the later state*, not the status code: an order
   created at the wrong amount is the finding even if creation returned 201.
5. Check the client-side trust boundary: a field the UI computes but never
   displays (a "unitPrice" in a JSON body) is a strong signal.
6. Repeat to the payment step if the app re-derives the amount there — many
   apps price the cart correctly and then trust `amount` for the intent.

## Evidence

Attach the baseline (correct price) and the tampered request whose response
confirms acceptance. If the order row is retrievable, capture the order read
that shows the fabricated total: that is the durable proof.

## Do not

Do not complete a real payment. Stop at the intent or the order row and delete
or cancel the created object afterwards; note in the finding that the state
change was bounded and reversed.
