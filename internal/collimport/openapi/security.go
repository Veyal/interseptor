package openapi

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

var unsafeVarChars = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func varName(s string) string { return unsafeVarChars.ReplaceAllString(s, "_") }

func (im *importer) loadSecurity() {
	if im.v2 {
		im.schemes = asMap(im.root.get("securityDefinitions"))
	} else {
		im.schemes = asMap(asMap(im.root.get("components")).get("securitySchemes"))
	}
	im.hasGlobalSec = im.root.has("security")
	im.globalSec = asSlice(im.root.get("security"))
	if im.hasGlobalSec {
		im.res.Collection.Auth = im.authFor(im.globalSec, im.res.Collection.Name)
	}
}

// authFor converts a security requirement list into an auth object (nil when
// nothing applies). An empty list or an optional-only list means no auth.
func (im *importer) authFor(reqs []any, path string) json.RawMessage {
	var alts []*omap
	for _, r := range reqs {
		if m := asMap(r); m != nil && len(m.keys) > 0 {
			alts = append(alts, m)
		}
	}
	if len(alts) == 0 {
		return mustJSON(map[string]string{"type": "noauth"})
	}
	if len(alts) > 1 || len(alts) < len(reqs) {
		im.reportOnce(path, Degraded, path, "multiple-auth-alternatives",
			"several security alternatives are declared; the first was used", "Switch the auth type on the request if another applies")
	}
	first := alts[0]
	if len(first.keys) > 1 {
		im.reportOnce(path, Degraded, path, "combined-auth",
			"a requirement combines several schemes; only the first was applied", "Add the other credentials as headers")
	}
	name := first.keys[0]
	return im.schemeAuth(name, path)
}

func (im *importer) schemeAuth(name, path string) json.RawMessage {
	raw, ok := im.schemes.get(name), im.schemes.has(name)
	if !ok {
		im.reportOnce(name, NeedsReview, path, "unknown-security-scheme", "security scheme "+name+" is not defined", "")
		return nil
	}
	sc, _, err := deref(im.root, raw)
	m := asMap(sc)
	if err != nil || m == nil {
		im.reportOnce(name, NeedsReview, path, "bad-security-scheme", "security scheme "+name+" could not be resolved", "")
		return nil
	}
	vn := varName(name)
	cuid := im.res.Collection.UID
	secret := func(key string) string {
		im.addVar(store.VarOwnerCollection, cuid, key, store.VarTypeSecret, "")
		return "{{" + key + "}}"
	}
	typ := asString(m.get("type"))
	switch typ {
	case "http":
		switch strings.ToLower(asString(m.get("scheme"))) {
		case "basic":
			return im.basic(name, vn, secret)
		case "digest":
			im.addVar(store.VarOwnerCollection, cuid, vn+"_username", store.VarTypeDefault, "")
			im.noteAuth("digest", name)
			return authJSON("digest", [][2]string{{"username", "{{" + vn + "_username}}"}, {"password", secret(vn + "_password")}})
		default:
			im.noteAuth("bearer", name)
			return authJSON("bearer", [][2]string{{"token", secret(vn + "_token")}})
		}
	case "basic": // swagger 2
		return im.basic(name, vn, secret)
	case "apiKey":
		return im.apiKey(name, vn, m, path, secret)
	case "oauth2":
		return im.oauth2(name, vn, m, secret)
	case "openIdConnect":
		im.reportOnce(name, Degraded, path, "auth:openIdConnect",
			"openIdConnect scheme "+name+" became a bearer token variable; discovery is not run", "Paste an access token into the secret variable")
		return authJSON("bearer", [][2]string{{"token", secret(vn + "_token")}})
	case "mutualTLS":
		im.reportOnce(name, Unsupported, path, "auth:mutualTLS", "mutualTLS scheme "+name+" needs a client certificate", "Configure the client certificate in Interseptor settings")
		return nil
	}
	im.reportOnce(name, Unsupported, path, "auth:"+typ, "unknown security scheme type "+typ, "")
	return nil
}

func (im *importer) noteAuth(typ, name string) {
	im.reportOnce(typ+"|"+name, Converted, "", "auth:"+typ, "security scheme "+name+" became "+typ+" auth with a secret variable", "")
}

func (im *importer) basic(name, vn string, secret func(string) string) json.RawMessage {
	im.addVar(store.VarOwnerCollection, im.res.Collection.UID, vn+"_username", store.VarTypeDefault, "")
	im.noteAuth("basic", name)
	return authJSON("basic", [][2]string{{"username", "{{" + vn + "_username}}"}, {"password", secret(vn + "_password")}})
}

func (im *importer) apiKey(name, vn string, m *omap, path string, secret func(string) string) json.RawMessage {
	hdr := asString(m.get("name"))
	in := asString(m.get("in"))
	val := secret(vn)
	im.noteAuth("apikey", name)
	switch in {
	case "query":
		return authJSON("apikey", [][2]string{{"key", hdr}, {"value", val}, {"in", "query"}})
	case "cookie":
		im.reportOnce(name, Degraded, path, "auth:apikey-cookie", "cookie API key "+name+" is sent as a Cookie header", "")
		return authJSON("apikey", [][2]string{{"key", "Cookie"}, {"value", hdr + "=" + val}, {"in", "header"}})
	}
	return authJSON("apikey", [][2]string{{"key", hdr}, {"value", val}, {"in", "header"}})
}

func (im *importer) oauth2(name, vn string, m *omap, secret func(string) string) json.RawMessage {
	grant, authURL, tokenURL := "", "", ""
	var scopes []string
	collect := func(f *omap) {
		if authURL == "" {
			authURL = asString(f.get("authorizationUrl"))
		}
		if tokenURL == "" {
			tokenURL = asString(f.get("tokenUrl"))
		}
		if sc := asMap(f.get("scopes")); sc != nil && len(scopes) == 0 {
			scopes = append(scopes, sc.keys...)
		}
	}
	if im.v2 {
		grant = map[string]string{"implicit": "implicit", "password": "password_credentials", "application": "client_credentials", "accessCode": "authorization_code"}[asString(m.get("flow"))]
		authURL, tokenURL = asString(m.get("authorizationUrl")), asString(m.get("tokenUrl"))
		if sc := asMap(m.get("scopes")); sc != nil {
			scopes = sc.keys
		}
	} else if fl := asMap(m.get("flows")); fl != nil {
		for _, k := range fl.keys {
			if grant == "" {
				grant = map[string]string{"implicit": "implicit", "password": "password_credentials", "clientCredentials": "client_credentials", "authorizationCode": "authorization_code"}[k]
			}
			collect(asMap(fl.m[k]))
		}
	}
	if grant == "" {
		grant = "authorization_code"
	}
	im.reportOnce("oauth2|"+name, Converted, "", "auth:oauth2", "oauth2: a manual access token variable is applied; token fetch flows are not run at import", "")
	return authJSON("oauth2", [][2]string{{"grant_type", grant}, {"authUrl", authURL}, {"accessTokenUrl", tokenURL},
		{"scope", strings.Join(scopes, " ")}, {"accessToken", secret(vn + "_token")}, {"tokenType", "Bearer"}, {"addTokenTo", "header"}})
}

func authJSON(typ string, kv [][2]string) json.RawMessage {
	m := postman.NewOMap()
	m.SetValue("type", typ)
	rows := make([]map[string]string, 0, len(kv))
	for _, p := range kv {
		rows = append(rows, map[string]string{"key": p[0], "value": p[1], "type": "string"})
	}
	m.SetValue(typ, rows)
	return mustJSON(m)
}

func mustJSON(v any) json.RawMessage {
	b, err := postman.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
