---
name: activity-persistence-contract
description: Keep Interseptor MCP activity acknowledgements and live broadcasts consistent with durable SQLite state.
---

# Activity persistence contract

An MCP activity is successful only after `Store.InsertActivity` succeeds. Propagate its error through
internal HTTP ingestion, and do not broadcast an item that was not stored.

Bounded append tables must insert and prune in one transaction, and publish generated IDs only after
commit. This applies to activity and WebSocket frames: if retention fails, roll the new row back so an
error cannot hide a durable-but-unannounced record or let the table grow past its cap.

The authenticated activity socket's one-byte response is a durability acknowledgement. Write it only
after persistence and broadcast succeed; on storage failure, close the connection without the byte so
the producer cannot mistake a transient UI event for a durable audit record.

Socket reporters use `json.Encoder`, so the wire format is one newline-terminated JSON value. Read at
most `max+1` bytes through that newline before unmarshalling. A decoder over `LimitReader(max)` can
accept a small valid object immediately and acknowledge it while oversized padding remains unread.

The MCP reporter callback cannot return errors. Log persistence failure there, but keep the shared
checked helper so transports that support acknowledgements can return truthful outcomes.
