# Action handler: summarize a captured flow for UI and MCP callers.

def _field(obj, key, default):
    if obj == None:
        return default
    if type(obj) == "dict":
        return obj.get(key, default)
    return getattr(obj, key, default)

def run(ctx):
    if type(ctx) != "dict":
        return {
            "method": "",
            "host": "",
            "path": "",
            "status": 0,
            "summary": "No context provided",
        }

    flow = ctx.get("flow")
    inp = ctx.get("input")
    if type(inp) != "dict":
        inp = ctx

    src = flow if flow != None else inp
    raw_method = str(_field(src, "method", "") or "")
    scheme = str(_field(src, "scheme", "") or "https").lower()
    host = str(_field(src, "host", "") or "")
    path = str(_field(src, "path", "") or "/")
    status = _field(src, "status", 0)
    if status == None:
        status = 0

    flow_id = _field(src, "id", ctx.get("flowId", inp.get("flowId", None)))
    tags = _field(src, "tags", [])
    if type(tags) != "list":
        tags = []

    method = raw_method.upper() if raw_method else ("GET" if host else "")

    if host:
        status_label = str(status) if status else "pending"
        summary = method + " " + scheme + "://" + host + path + " -> " + status_label
    else:
        flow_hint = str(flow_id) if flow_id != None else ""
        if flow_hint:
            summary = "Flow #" + flow_hint + " has no captured metadata"
        else:
            summary = "No flow selected (pass flowId or flow metadata)"

    out = {
        "method": method,
        "host": host,
        "path": path if host else "",
        "status": status,
        "summary": summary,
    }
    if flow_id != None:
        out["flowId"] = flow_id
    if len(tags) > 0:
        out["tags"] = tags
    return out
