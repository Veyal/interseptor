# Tag captured flows targeting example.com or /api/ endpoints.

def _field(obj, key, default):
    if obj == None:
        return default
    if type(obj) == "dict":
        return obj.get(key, default)
    return getattr(obj, key, default)

def on_flow(flow):
    if flow == None:
        return None
    host = str(_field(flow, "host", "") or "").lower()
    path = str(_field(flow, "path", "") or "").lower()
    if "example.com" in host or "/api/" in path:
        return {"tags": ["hello-api"]}
    return None
