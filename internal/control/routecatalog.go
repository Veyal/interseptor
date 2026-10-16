package control

// RouteInfo is one entry of the REST route catalog served by GET /api/reference.
type RouteInfo struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Desc   string `json:"desc"`
}

// RouteCatalog returns a copy of the REST route catalog, in catalog order. The
// documentation generator reads it so the published agent reference cannot
// drift from GET /api/reference.
func RouteCatalog() []RouteInfo {
	out := make([]RouteInfo, 0, len(apiRoutes))
	for _, r := range apiRoutes {
		out = append(out, RouteInfo{Method: r.Method, Path: r.Path, Desc: r.Desc})
	}
	return out
}
