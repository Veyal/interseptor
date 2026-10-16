package insomnia

import (
	"sort"

	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
)

// parseV4 reads the flat "resources" export (formats 3 and 4).
func parseV4(root map[string]any) (*parsed, error) {
	p := &parsed{skipped: map[string]int{}}
	res := arr(root, "resources")
	nodes := map[string]*node{}
	envs := map[string]*env{}
	type link struct {
		id, parent string
	}
	var order []link
	workspaces := map[string]string{}
	for _, r := range res {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		id, parent := str(m, "_id"), str(m, "parentId")
		switch t := str(m, "_type"); t {
		case "workspace":
			workspaces[id] = str(m, "name")
			if p.name == "" {
				p.name = str(m, "name")
			}
		case "request_group", "request":
			n := &node{id: id, name: str(m, "name"), desc: str(m, "description"), folder: t == "request_group",
				sortKey: num(m, "metaSortKey")}
			if n.folder {
				n.env = obj(m, "environment")
				n.headers = rows(arr(m, "headers"))
				n.auth = obj(m, "authentication")
			} else {
				fillRequest(n, m)
			}
			nodes[id] = n
			order = append(order, link{id, parent})
		case "environment":
			envs[id] = &env{id: id, name: str(m, "name"), data: obj(m, "data"), private: boolean(m, "isPrivate"),
				sortKey: num(m, "metaSortKey")}
			order = append(order, link{"env:" + id, parent})
		default:
			p.skipped[t]++
		}
	}
	for _, l := range order {
		if len(l.id) > 4 && l.id[:4] == "env:" {
			continue
		}
		n := nodes[l.id]
		if parent, ok := nodes[l.parent]; ok && parent.folder && parent != n {
			parent.children = append(parent.children, n)
		} else if _, isWS := workspaces[l.parent]; isWS || l.parent == "" {
			p.root = append(p.root, n)
		} else {
			p.root = append(p.root, n)
			p.notes = append(p.notes, impkit.Entry{Level: impkit.Degraded, Path: n.name, Feature: "orphan",
				Message: "parent " + l.parent + " not found; moved to the collection root"})
		}
	}
	p.root = breakCycles(p.root, nodes)
	// environments: base env has a workspace parent; sub-envs have an env parent
	var ids []string
	for id := range envs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := envs[id]
		var parent string
		for _, l := range order {
			if l.id == "env:"+id {
				parent = l.parent
			}
		}
		if pe, ok := envs[parent]; ok {
			pe.subs = append(pe.subs, e)
		} else {
			if p.base == nil {
				p.base = e
			} else {
				p.envs = append(p.envs, e)
			}
		}
	}
	for _, e := range envs {
		sort.SliceStable(e.subs, func(i, j int) bool { return e.subs[i].sortKey < e.subs[j].sortKey })
	}
	return p, nil
}

// breakCycles re-attaches request_group cycles (A under B under A) to the
// root so no item is lost and walkers terminate.
func breakCycles(root []*node, all map[string]*node) []*node {
	seen := map[*node]bool{}
	var mark func(n *node, depth int)
	mark = func(n *node, depth int) {
		if seen[n] || depth > 4*impkit.MaxDepth {
			return
		}
		seen[n] = true
		for _, c := range n.children {
			mark(c, depth+1)
		}
	}
	for _, n := range root {
		mark(n, 0)
	}
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if n := all[id]; !seen[n] {
			n.children = nil // cut the cycle at this node's subtree below
			root = append(root, n)
			mark(n, 0)
		}
	}
	return root
}

func fillRequest(n *node, m map[string]any) {
	n.method = str(m, "method")
	n.url = str(m, "url")
	n.params = rows(arr(m, "parameters"))
	n.headers = rows(arr(m, "headers"))
	n.body = obj(m, "body")
	n.auth = obj(m, "authentication")
	n.preScript = str(m, "preRequestScript")
	n.postScript = str(m, "afterResponseScript")
	s := map[string]any{}
	for _, k := range []string{"settingFollowRedirects", "settingSendCookies", "settingStoreCookies", "settingEncodeUrl", "settingRebuildPath", "settingDisableRenderRequestBody"} {
		if v, ok := m[k]; ok {
			s[k] = v
		}
	}
	n.settings = s
}

// parseV5 reads the YAML/JSON collection format (type collection.insomnia.rest/5.x).
func parseV5(root map[string]any) (*parsed, error) {
	p := &parsed{skipped: map[string]int{}, name: str(root, "name")}
	var walk func(list []any, depth int) []*node
	walk = func(list []any, depth int) []*node {
		var out []*node
		for i, e := range list {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			meta := obj(m, "meta")
			n := &node{id: str(meta, "id"), name: str(m, "name"), desc: str(meta, "description"), sortKey: num(meta, "sortKey")}
			if n.sortKey == 0 {
				n.sortKey = float64(i)
			}
			if kids, isFolder := m["children"]; isFolder {
				n.folder = true
				n.env = obj(m, "environment")
				n.headers = rows(arr(m, "headers"))
				n.auth = obj(m, "authentication")
				if depth < impkit.MaxDepth {
					l, _ := kids.([]any)
					n.children = walk(l, depth+1)
				}
			} else {
				if desc := str(m, "description"); desc != "" {
					n.desc = desc
				}
				n.method = str(m, "method")
				n.url = str(m, "url")
				n.params = rows(arr(m, "parameters"))
				n.headers = rows(arr(m, "headers"))
				n.body = obj(m, "body")
				n.auth = obj(m, "authentication")
				sc := obj(m, "scripts")
				n.preScript, n.postScript = str(sc, "preRequest"), str(sc, "afterResponse")
				n.settings = obj(m, "settings")
			}
			out = append(out, n)
		}
		return out
	}
	p.root = walk(arr(root, "collection"), 0)
	if envs := obj(root, "environments"); envs != nil {
		p.base = &env{id: str(obj(envs, "meta"), "id"), name: str(envs, "name"), data: obj(envs, "data")}
		for i, s := range arr(envs, "subEnvironments") {
			if m, ok := s.(map[string]any); ok {
				p.base.subs = append(p.base.subs, &env{id: str(obj(m, "meta"), "id"), name: str(m, "name"),
					data: obj(m, "data"), private: boolean(m, "isPrivate"), sortKey: float64(i)})
			}
		}
	}
	if cj := root["cookieJar"]; cj != nil {
		p.skipped["cookie_jar"]++
	}
	return p, nil
}
