package postman

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

// Static script analysis: reads the source as text, never executes it and
// never rewrites it (no regex translator). It lists the pm.* surface used so
// the report can say honestly how much will run.

var (
	apiRe     = regexp.MustCompile(`\b(pm|postman)\.([A-Za-z_]+)(?:\.([A-Za-z_]+))?`)
	requireRe = regexp.MustCompile(`\brequire\(\s*['"]([^'"]+)['"]\s*\)`)
	hostRe    = regexp.MustCompile(`https?://([A-Za-z0-9][A-Za-z0-9.\-]*)`)
	evalRe    = regexp.MustCompile(`\beval\s*\(|\bnew\s+Function\s*\(|\bFunction\s*\(`)
	hexEscRe  = regexp.MustCompile(`\\x[0-9a-fA-F]{2}`)
	obfIDRe   = regexp.MustCompile(`\b_0x[0-9a-f]{4,}\b`)
	forbidRe  = regexp.MustCompile(`\bfetch\s*\(|\bnew\s+XMLHttpRequest\b|\bprocess\.(env|exit|argv|binding)|\bchild_process\b|\bimportScripts\b`)
)

var supportedPM = map[string]bool{
	"environment": true, "globals": true, "collectionVariables": true, "variables": true,
	"iterationData": true, "request": true, "response": true, "test": true, "expect": true,
	"sendRequest": true, "execution": true, "cookies": true, "info": true,
}

var unsupportedPM = map[string]bool{"visualizer": true, "vault": true, "require": true}

var supportedModules = map[string]bool{"crypto": true, "crypto-js": true, "buffer": true, "uuid": true, "url": true}
var deferredModules = map[string]bool{"lodash": true, "moment": true, "cheerio": true, "xml2js": true, "tv4": true, "ajv": true, "chai": true}

// scriptAnalysis is the result of scanning one script.
type scriptAnalysis struct {
	hash        string
	lines       int
	apis        []string
	modules     []string
	hosts       []string
	flags       []string
	unsupported map[string]int // feature -> first line
	deferred    map[string]int
}

func (a *scriptAnalysis) status() string {
	switch {
	case len(a.unsupported) > 0:
		return "unsupported"
	case len(a.deferred) > 0:
		return "partial"
	}
	return "supported"
}

func analyzeScript(src string) scriptAnalysis {
	sum := sha256.Sum256([]byte(src))
	a := scriptAnalysis{hash: hex.EncodeToString(sum[:]), unsupported: map[string]int{}, deferred: map[string]int{}}
	lines := strings.Split(src, "\n")
	a.lines = len(lines)
	apis, mods, hosts := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, line := range lines {
		n := i + 1
		for _, m := range apiRe.FindAllStringSubmatch(line, -1) {
			root, sub, leaf := m[1], m[2], m[3]
			name := root + "." + sub
			if leaf != "" && root == "pm" && (sub == "environment" || sub == "response" || sub == "request" || sub == "test" || sub == "execution" || sub == "cookies" || sub == "variables" || sub == "globals" || sub == "collectionVariables") {
				name += "." + leaf
			}
			apis[name] = true
			if root == "pm" {
				switch {
				case unsupportedPM[sub]:
					setFirst(a.unsupported, "pm."+sub, n)
				case !supportedPM[sub]:
					setFirst(a.unsupported, "pm."+sub, n)
				}
			}
		}
		for _, m := range requireRe.FindAllStringSubmatch(line, -1) {
			mods[m[1]] = true
			switch {
			case supportedModules[m[1]]:
			case deferredModules[m[1]]:
				setFirst(a.deferred, "require('"+m[1]+"')", n)
			default:
				setFirst(a.unsupported, "require('"+m[1]+"')", n)
			}
		}
		for _, m := range forbidRe.FindAllString(line, -1) {
			setFirst(a.unsupported, strings.TrimSpace(strings.TrimSuffix(m, "(")), n)
		}
		for _, m := range hostRe.FindAllStringSubmatch(line, -1) {
			hosts[strings.ToLower(m[1])] = true
		}
	}
	a.apis, a.modules, a.hosts = keys(apis), keys(mods), keys(hosts)
	var flags []string
	if evalRe.MatchString(src) {
		flags = append(flags, "eval-or-Function")
	}
	if len(hexEscRe.FindAllString(src, -1)) >= 8 || obfIDRe.MatchString(src) {
		flags = append(flags, "obfuscation")
	}
	if len(src) > 512*1024 {
		flags = append(flags, "oversize")
	}
	a.flags = flags
	return a
}

func setFirst(m map[string]int, k string, line int) {
	if _, ok := m[k]; !ok {
		m[k] = line
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
