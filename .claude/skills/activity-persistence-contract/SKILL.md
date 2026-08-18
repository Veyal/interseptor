---
name: activity-persistence-contract
description: Keep Interseptor MCP activity acknowledgements and live broadcasts consistent with durable SQLite state.
---

# Activity persistence contract

An MCP activity is successful only after `Store.InsertActivity` succeeds. Propagate its error through
internal HTTP ingestion, and do not broadcast an item that was not stored.

The authenticated activity socket's one-byte response is a durability acknowledgement. Write it only
after persistence and broadcast succeed; on storage failure, close the connection without the byte so
the producer cannot mistake a transient UI event for a durable audit record.

The MCP reporter callback cannot return errors. Log persistence failure there, but keep the shared
checked helper so transports that support acknowledgements can return truthful outcomes.
