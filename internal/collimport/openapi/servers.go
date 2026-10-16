package openapi

import (
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

type server struct{ name, url string }

// servers turns servers (or Swagger 2 host/basePath/schemes) into the
// baseUrl collection variable and one environment per server.
func (im *importer) servers() {
	var list []server
	if im.v2 {
		list = im.v2Servers()
	} else {
		list = im.v3Servers()
	}
	if len(list) == 0 {
		list = []server{{name: "Default", url: "http://localhost"}}
		im.report(Degraded, "servers", "no-servers", "the document declares no servers; http://localhost was used as the base URL", "Edit the baseUrl variable")
	}
	im.baseURL = list[0].url
	cuid := im.res.Collection.UID
	im.addVar(store.VarOwnerCollection, cuid, "baseUrl", store.VarTypeDefault, list[0].url)
	used := map[string]bool{}
	for _, s := range list {
		if !strings.Contains(s.url, "://") {
			im.reportOnce(s.url, NeedsReview, "servers", "relative-server-url",
				"server URL "+s.url+" is relative; there is no origin to send to", "Set the baseUrl variable to a full URL")
		}
		name := s.name
		for n := 2; used[name]; n++ {
			name = s.name + " (" + itoa(n) + ")"
		}
		used[name] = true
		env := store.Environment{UID: im.opt.NewID(), Name: name, Kind: "env", CollectionUID: cuid}
		vars := []store.Variable{{OwnerKind: store.VarOwnerEnvironment, OwnerUID: env.UID, Key: "baseUrl",
			Type: store.VarTypeDefault, InitialValue: s.url, Enabled: true}}
		im.res.Environments = append(im.res.Environments, EnvImport{Environment: env, Variables: vars})
		im.res.Report.Stats.EnvironmentVariables++
	}
	im.report(Converted, "servers", "servers", itoa(len(list))+" server(s) became environments with a baseUrl variable", "")
}

func (im *importer) v3Servers() []server {
	var out []server
	for _, sv := range asSlice(im.root.get("servers")) {
		m := asMap(sv)
		u := asString(m.get("url"))
		if u == "" {
			continue
		}
		u = expandServerVars(u, asMap(m.get("variables")))
		name := asString(m.get("description"))
		if name == "" || len(name) > 60 {
			name = u
		}
		out = append(out, server{name, strings.TrimRight(u, "/")})
	}
	return out
}

func (im *importer) v2Servers() []server {
	host := asString(im.root.get("host"))
	base := strings.TrimRight(asString(im.root.get("basePath")), "/")
	var schemes []string
	for _, s := range asSlice(im.root.get("schemes")) {
		schemes = append(schemes, asString(s))
	}
	if len(schemes) == 0 {
		schemes = []string{"https"}
		if host != "" {
			im.report(Degraded, "schemes", "no-schemes", "no schemes declared; https was assumed", "")
		}
	}
	if host == "" {
		if base == "" {
			return nil
		}
		return []server{{base, base}}
	}
	var out []server
	for _, sc := range schemes {
		u := sc + "://" + host + base
		out = append(out, server{u, u})
	}
	return out
}

// expandServerVars substitutes {name} with the variable's default.
func expandServerVars(u string, vars *omap) string {
	for _, k := range vars.keysOrNil() {
		def := asString(asMap(vars.m[k]).get("default"))
		u = strings.ReplaceAll(u, "{"+k+"}", def)
	}
	return u
}

func (o *omap) keysOrNil() []string {
	if o == nil {
		return nil
	}
	return o.keys
}
