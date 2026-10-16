package postman

import (
	"regexp"

	"github.com/Veyal/interseptor/internal/store"
)

func (p *parser) parseEnvFile(root *OMap) error {
	scope := jsonString(root.Get("_postman_variable_scope"))
	kind, storeKind := KindEnvironment, "env"
	if scope == "globals" {
		kind, storeKind = KindGlobals, "globals"
	}
	if p.res.Kind == "" {
		p.res.Kind = kind
	}
	env := store.Environment{UID: p.opt.NewID(), Name: jsonString(root.Get("name")), Kind: storeKind}
	if env.Name == "" {
		env.Name = "Imported " + storeKind
	}
	p.envVars = nil
	side := EnvSidecar{Keys: root.Keys(), Extra: root.Rest("name", "values")}
	if v := root.Get("values"); !isNull(v) {
		side.Values = p.importVars(v, store.VarOwnerEnvironment, env.UID, "environment "+env.Name, "", true)
	}
	if len(side.Extra) == 0 {
		side.Extra = nil
	}
	p.res.Environments = append(p.res.Environments, EnvImport{Environment: env, Variables: p.envVars, Sidecar: mustJSON(side)})
	p.res.Report.add(Entry{Level: Converted, Path: env.Name, Feature: "environment", Message: "imported as " + storeKind + " with initial values only; current values are local"})
	p.envVars = nil
	return nil
}

var dynVarRe = regexp.MustCompile(`\{\{\s*\$([A-Za-z]+)`)

var knownDynamic = map[string]bool{
	"guid": true, "randomUUID": true, "timestamp": true, "isoTimestamp": true, "randomInt": true,
	"randomAlphaNumeric": true, "randomBoolean": true, "randomFirstName": true, "randomLastName": true,
	"randomFullName": true, "randomEmail": true, "randomUserName": true, "randomIP": true,
	"randomPassword": true, "randomHexColor": true, "randomWord": true, "randomPhoneNumber": true,
}

// scanDynamicVars flags {{$name}} dynamic variables the resolver does not
// know, because unresolved variables block the send.
func (p *parser) scanDynamicVars() {
	seen := map[string]bool{}
	for _, m := range dynVarRe.FindAllStringSubmatch(p.text, -1) {
		n := m[1]
		if knownDynamic[n] || seen[n] {
			continue
		}
		seen[n] = true
		p.res.Report.add(Entry{Level: NeedsReview, Feature: "dynamic-variable:$" + n, Message: "dynamic variable {{$" + n + "}} is not built in; it stays unresolved and blocks the send", Suggestion: "Replace it with a defined variable or a pre-request script"})
	}
}
