package control

func (h *Hub) routes() {
	f := &flowAPI{h}
	ic := &interceptAPI{h}
	set := &settingsAPI{h}
	sc := &scopeAPI{h}
	fd := &findingsAPI{h}
	tools := &toolsAPI{h}
	scan := &scannerAPI{h}
	chk := &checksAPI{h}
	ai := &aiAPI{h}
	proj := &projectAPI{h}
	and := &androidAPI{h}
	iosH := &iosAPI{h}
	oob := &oobAPI{h}
	az := &authzAPI{h}
	as := &activescanAPI{h}
	sess := &sessionAPI{h}
	meta := &metaAPI{h}

	h.registerFlowRoutes(f)
	h.registerInterceptRoutes(ic)
	h.registerSettingsRoutes(set, and, iosH, sess)
	h.registerScopeRoutes(sc)
	h.registerFindingsRoutes(fd)
	h.registerToolsRoutes(tools)
	h.registerScannerRoutes(scan)
	h.registerChecksRoutes(chk, as)
	h.registerAIRoutes(ai)
	h.registerProjectRoutes(proj)
	h.registerOobRoutes(oob)
	h.registerAuthzRoutes(az)
	h.registerMetaRoutes(meta)
	h.registerAutopwnRoutes()
	h.registerPacksRoutes()

	h.mux.HandleFunc("/", h.serveUI)
}

func (h *Hub) registerFlowRoutes(f *flowAPI) {
	h.mux.HandleFunc("GET /api/flows", f.listFlows)
	h.mux.HandleFunc("GET /api/flows/inscope", f.trafficInScope)
	h.mux.HandleFunc("GET /api/params", f.listParams)
	h.mux.HandleFunc("GET /api/flows/{id}", f.getFlow)
	h.mux.HandleFunc("GET /api/flows/{id}/raw", f.getFlowRaw)
	h.mux.HandleFunc("GET /api/flows/{id}/preview.png", f.getFlowPreviewPNG)
	h.mux.HandleFunc("GET /api/flows/{id}/body", f.getFlowBody)
	h.mux.HandleFunc("GET /api/flows/{id}/ws", f.flowWS)
	h.mux.HandleFunc("GET /api/flows/{id}/analyze", f.analyzeFlow)
	h.mux.HandleFunc("GET /api/flows/{id}/curl", f.flowCurl)
	h.mux.HandleFunc("GET /api/flows/diff", f.diffFlows)
	h.mux.HandleFunc("PUT /api/flows/{id}/note", f.setFlowNote)
	h.mux.HandleFunc("PUT /api/flows/{id}/tags", f.setFlowTags)
	h.mux.HandleFunc("POST /api/flows/tags", f.addFlowTagsBulk)
	h.mux.HandleFunc("GET /api/tags", f.listTags)
	h.mux.HandleFunc("PUT /api/tags/{tag}/color", f.setTagColor)
	h.mux.HandleFunc("POST /api/flows/delete", f.deleteFlows)
	h.mux.HandleFunc("POST /api/flows/purge", f.purgeFlows)
	h.mux.HandleFunc("POST /api/flows/gc", f.gcBodies)
	h.mux.HandleFunc("GET /api/flows/retention", f.getRetention)
	h.mux.HandleFunc("PUT /api/flows/retention", f.putRetention)
	h.mux.HandleFunc("POST /api/flows/retention/run", f.runRetention)
	h.mux.HandleFunc("GET /api/hosts/stats", f.hostStats)
	h.mux.HandleFunc("GET /api/endpoints", f.listEndpoints)
	h.mux.HandleFunc("GET /api/rules", f.listRules)
	h.mux.HandleFunc("POST /api/rules", f.createRule)
	h.mux.HandleFunc("PUT /api/rules/{id}", f.updateRule)
	h.mux.HandleFunc("DELETE /api/rules/{id}", f.deleteRule)
}

func (h *Hub) registerInterceptRoutes(ic *interceptAPI) {
	h.mux.HandleFunc("GET /api/intercept", ic.getIntercept)
	h.mux.HandleFunc("GET /api/intercept/held/{id}/raw", ic.getInterceptHeldRaw)
	h.mux.HandleFunc("POST /api/intercept/toggle", ic.toggleIntercept)
	h.mux.HandleFunc("POST /api/intercept/filter", ic.setInterceptFilter)
	h.mux.HandleFunc("POST /api/intercept/{id}/forward", ic.forwardIntercept)
	h.mux.HandleFunc("POST /api/intercept/{id}/drop", ic.dropIntercept)
	h.mux.HandleFunc("POST /api/intercept/response/toggle", ic.toggleResponseIntercept)
	h.mux.HandleFunc("POST /api/intercept/response/{id}/forward", ic.forwardResponse)
	h.mux.HandleFunc("POST /api/intercept/response/{id}/drop", ic.dropResponse)
}

func (h *Hub) registerSettingsRoutes(set *settingsAPI, and *androidAPI, ios *iosAPI, sess *sessionAPI) {
	h.mux.HandleFunc("GET /api/settings", set.getSettings)
	h.mux.HandleFunc("PUT /api/settings", set.putSettings)
	h.mux.HandleFunc("GET /api/network/hosts", set.getNetworkHosts)
	h.mux.HandleFunc("GET /api/proxy/device-endpoint", set.getDeviceProxyEndpoint)
	h.mux.HandleFunc("POST /api/proxy/device-endpoint", set.setDeviceProxyEndpoint)
	h.mux.HandleFunc("GET /api/sysproxy", set.getSysProxy)
	h.mux.HandleFunc("POST /api/sysproxy", set.setSysProxy)
	h.mux.HandleFunc("GET /api/ca.crt", set.getCA)
	h.mux.HandleFunc("GET /openapi.json", (&metaAPI{h}).openapi)
	h.mux.HandleFunc("GET /api/android/status", and.getAndroidStatus)
	h.mux.HandleFunc("POST /api/android/proxy", and.postAndroidProxy)
	h.mux.HandleFunc("POST /api/android/unproxy", and.postAndroidUnproxy)
	h.mux.HandleFunc("POST /api/android/install-ca", and.postAndroidInstallCA)
	h.mux.HandleFunc("POST /api/android/setup", and.postAndroidSetup)
	h.mux.HandleFunc("GET /api/ios/status", ios.getIOSStatus)
	h.mux.HandleFunc("GET /api/ios/profile.mobileconfig", ios.getIOSProfile)
	h.mux.HandleFunc("POST /api/ios/setup", ios.postIOSSetup)
	h.mux.HandleFunc("POST /api/ios/install-ca", ios.postIOSInstallCA)
	h.mux.HandleFunc("POST /api/ios/open-profile", ios.postIOSOpenProfile)
	h.mux.HandleFunc("GET /api/ios/ssh/status", ios.getIOSSSHStatus)
	h.mux.HandleFunc("POST /api/ios/ssh/status", ios.postIOSSSHStatus)
	h.mux.HandleFunc("POST /api/ios/ssh/setup", ios.postIOSSSHSetup)
	h.mux.HandleFunc("POST /api/ios/ssh/install-ca", ios.postIOSSSHInstallCA)
	h.mux.HandleFunc("GET /api/session", sess.getSession)
	h.mux.HandleFunc("POST /api/session", sess.setSession)
	h.mux.HandleFunc("POST /api/session/login/run", sess.runLoginMacro)
	h.mux.HandleFunc("POST /api/session/login/test", sess.testLoginMacro)
	h.mux.HandleFunc("POST /api/session/login/from-flow/{id}", sess.loginMacroFromFlow)
}

func (h *Hub) registerScopeRoutes(sc *scopeAPI) {
	h.mux.HandleFunc("GET /api/scope", sc.listScope)
	h.mux.HandleFunc("POST /api/scope", sc.createScope)
	h.mux.HandleFunc("PUT /api/scope/{id}", sc.updateScope)
	h.mux.HandleFunc("DELETE /api/scope/{id}", sc.deleteScope)
}

func (h *Hub) registerFindingsRoutes(fd *findingsAPI) {
	h.mux.HandleFunc("GET /api/findings", fd.listFindings)
	h.mux.HandleFunc("GET /api/findings/report", fd.findingsReport)
	h.mux.HandleFunc("GET /api/findings/images/{hash}", fd.getFindingImage)
	h.mux.HandleFunc("POST /api/findings", fd.createFinding)
	h.mux.HandleFunc("GET /api/findings/{id}", fd.getFinding)
	h.mux.HandleFunc("PATCH /api/findings/{id}", fd.updateFinding)
	h.mux.HandleFunc("DELETE /api/findings/{id}", fd.deleteFinding)
	h.mux.HandleFunc("POST /api/findings/{id}/flows", fd.attachFindingFlow)
	h.mux.HandleFunc("DELETE /api/findings/{id}/flows/{flowId}", fd.detachFindingFlow)
	h.mux.HandleFunc("POST /api/findings/{id}/images", fd.attachFindingImage)
	h.mux.HandleFunc("POST /api/findings/{id}/flow-preview", fd.attachFindingFlowPreview)
}

func (h *Hub) registerToolsRoutes(tools *toolsAPI) {
	h.mux.HandleFunc("POST /api/repeater/send", tools.repeaterSend)
	h.mux.HandleFunc("GET /api/repeater/history", tools.repeaterHistory)
	h.mux.HandleFunc("POST /api/intruder/start", tools.intruderStart)
	h.mux.HandleFunc("GET /api/intruder/state", tools.intruderState)
	h.mux.HandleFunc("POST /api/ws/send", tools.wsSend)
	h.mux.HandleFunc("POST /api/decode", tools.decode)
	h.mux.HandleFunc("POST /api/flows/{id}/replay", tools.replayFlow)
	h.mux.HandleFunc("GET /replay/{id}", tools.replayPage)
}

func (h *Hub) registerScannerRoutes(scan *scannerAPI) {
	h.mux.HandleFunc("POST /api/scanner/run", scan.scannerRun)
	h.mux.HandleFunc("GET /api/scanner/issues", scan.scannerIssues)
	h.mux.HandleFunc("GET /api/scanner/report", scan.scannerReport)
}

func (h *Hub) registerChecksRoutes(chk *checksAPI, as *activescanAPI) {
	h.mux.HandleFunc("GET /api/checks", chk.listChecks)
	h.mux.HandleFunc("PUT /api/checks/disabled", chk.setChecksDisabled)
	h.mux.HandleFunc("GET /api/checks/reference", chk.checksReference)
	h.mux.HandleFunc("POST /api/checks/test", chk.testCheck)
	h.mux.HandleFunc("GET /api/checks/{id}", chk.getCheck)
	h.mux.HandleFunc("PUT /api/checks/{id}", chk.saveCheck)
	h.mux.HandleFunc("DELETE /api/checks/{id}", chk.deleteCheck)
	h.mux.HandleFunc("GET /api/active-checks", chk.listActiveChecks)
	h.mux.HandleFunc("POST /api/active-checks/test", chk.testActiveCheck)
	h.mux.HandleFunc("GET /api/active-checks/{id}", chk.getActiveCheck)
	h.mux.HandleFunc("PUT /api/active-checks/{id}", chk.saveActiveCheck)
	h.mux.HandleFunc("DELETE /api/active-checks/{id}", chk.deleteActiveCheck)
	h.mux.HandleFunc("GET /api/activescan", as.asGet)
	h.mux.HandleFunc("GET /api/activescan/history", as.activescanHistory)
	h.mux.HandleFunc("POST /api/activescan/arm", as.asArm)
	h.mux.HandleFunc("POST /api/activescan/start", as.asStart)
	h.mux.HandleFunc("POST /api/activescan/stop", as.asStop)
}

func (h *Hub) registerPacksRoutes() {
	h.mux.HandleFunc("GET /api/packs", h.listPacks)
	h.mux.HandleFunc("POST /api/packs/install", h.installPack)
	h.mux.HandleFunc("GET /api/packs/{name}", h.getPack)
	h.mux.HandleFunc("DELETE /api/packs/{name}", h.removePack)
}

func (h *Hub) registerAIRoutes(ai *aiAPI) {
	h.mux.HandleFunc("POST /api/ai/notes/organize", ai.aiNotesOrganize)
	h.mux.HandleFunc("POST /api/ai/notes/organize/stream", ai.aiNotesOrganizeStream)
	h.mux.HandleFunc("POST /api/ai/checks/generate", ai.aiChecksGenerate)
	h.mux.HandleFunc("POST /api/ai/assist", ai.aiAssist)
	h.mux.HandleFunc("POST /api/ai/assist/stream", ai.aiAssistStream)
	h.mux.HandleFunc("POST /api/ai/findings/triage", ai.aiFindingsTriage)
	h.mux.HandleFunc("POST /api/ai/actions", ai.aiActions)
	h.mux.HandleFunc("POST /api/ai/intruder-payloads", ai.aiIntruderPayloads)
	h.mux.HandleFunc("GET /api/ai/openrouter/models", ai.aiOpenRouterModels)
	// Saved AI provider profiles (switch the active provider with one click).
	h.mux.HandleFunc("GET /api/ai/providers", h.listAiProviders)
	h.mux.HandleFunc("POST /api/ai/providers", h.saveAiProvider)
	h.mux.HandleFunc("DELETE /api/ai/providers/{id}", h.deleteAiProvider)
	h.mux.HandleFunc("POST /api/ai/providers/{id}/activate", h.activateAiProvider)
}

func (h *Hub) registerProjectRoutes(proj *projectAPI) {
	h.mux.HandleFunc("GET /api/notes", proj.getNotes)
	h.mux.HandleFunc("PUT /api/notes", proj.putNotes)
	h.mux.HandleFunc("PATCH /api/notes", proj.patchNotes)
	h.mux.HandleFunc("POST /api/notes/images", proj.postNotesImage)
	h.mux.HandleFunc("GET /api/notes/images/{id}", proj.getNotesImage)
	h.mux.HandleFunc("GET /api/export/har", proj.exportHAR)
	h.mux.HandleFunc("POST /api/import/har", proj.importHAR)
	h.mux.HandleFunc("GET /api/export/project", proj.exportProject)
	h.mux.HandleFunc("POST /api/import/project", proj.importProject)
	h.mux.HandleFunc("GET /api/export/full", proj.exportFull)
	h.mux.HandleFunc("POST /api/import/full", proj.importFull)
	h.mux.HandleFunc("POST /api/export/full/file", proj.exportFullFile)
	h.mux.HandleFunc("POST /api/import/full/file", proj.importFullFile)
	h.mux.HandleFunc("GET /api/views", proj.listViews)
	h.mux.HandleFunc("POST /api/views", proj.createView)
	h.mux.HandleFunc("DELETE /api/views/{id}", proj.deleteView)
	h.mux.HandleFunc("GET /api/project", proj.apiProject)
	h.mux.HandleFunc("POST /api/project/switch", proj.switchProject)
}

func (h *Hub) registerOobRoutes(oob *oobAPI) {
	h.mux.HandleFunc("/oob/", oob.oobCatch)
	h.mux.HandleFunc("GET /api/oob/state", oob.oobState)
	h.mux.HandleFunc("POST /api/oob/new", oob.oobNew)
	h.mux.HandleFunc("POST /api/oob/base", oob.oobSetBase)
	h.mux.HandleFunc("DELETE /api/oob/interactions", oob.oobClear)
}

// registerAuthzRoutes also carries /api/readiness and /api/tls-diagnosis even
// though neither is about role-based authz replay: buildReadiness (readiness.go)
// calls h.authzIdentities() to populate its "auth_identities" check, and
// buildTLSDiagnosis (tlsdiag.go) is colocated on the same authzAPI receiver only
// because buildReadiness calls it directly. See the doc comments on
// buildReadiness/buildTLSDiagnosis for the full rationale — moving them to a
// neutral receiver (e.g. metaAPI) would require either duplicating
// authzIdentities() or introducing a cross-group call, which isn't worth it for a
// cosmetic regroup.
func (h *Hub) registerAuthzRoutes(az *authzAPI) {
	h.mux.HandleFunc("GET /api/authz", az.getAuthz)
	h.mux.HandleFunc("POST /api/authz", az.setAuthz)
	h.mux.HandleFunc("GET /api/readiness", az.getReadiness)
	h.mux.HandleFunc("GET /api/tls-diagnosis", az.getTLSDiagnosis)
	h.mux.HandleFunc("GET /api/authz/flow-auth/{id}", az.authzFlowAuth)
	h.mux.HandleFunc("POST /api/authz/from-flow/{id}", az.authzPromoteFromFlow)
	h.mux.HandleFunc("POST /api/authz/check-sessions", az.authzCheckSessions)
	h.mux.HandleFunc("POST /api/authz/run", az.authzRun)
	h.mux.HandleFunc("POST /api/authz/cross-host-replay", az.authzCrossHostReplay)
}

// registerAutopwnRoutes wires the autonomous-pentest ("Autopilot") run lifecycle.
// The handlers hang off *Hub directly (the engine is a Hub-owned singleton built
// lazily), so there is no dedicated API facade type.
func (h *Hub) registerAutopwnRoutes() {
	h.mux.HandleFunc("POST /api/autopwn/start", h.autopwnStart)
	h.mux.HandleFunc("POST /api/autopwn/stop", h.autopwnStop)
	h.mux.HandleFunc("GET /api/autopwn/state", h.autopwnStateHandler)
	h.mux.HandleFunc("GET /api/autopwn/runs", h.autopwnRuns)
}

func (h *Hub) registerMetaRoutes(meta *metaAPI) {
	h.mux.HandleFunc("GET /api/keys", meta.listKeys)
	h.mux.HandleFunc("POST /api/keys", meta.createKey)
	h.mux.HandleFunc("DELETE /api/keys/{id}", meta.deleteKey)
	h.mux.HandleFunc("GET /api/version", meta.apiVersion)
	h.mux.HandleFunc("GET /api/activity", meta.listActivity)
	h.mux.HandleFunc("POST /api/activity", meta.postActivity)
	h.mux.HandleFunc("DELETE /api/activity", meta.clearActivity)
	h.mux.HandleFunc("POST /api/human-input", meta.createHumanInput)
	h.mux.HandleFunc("GET /api/human-input", meta.listHumanInput)
	h.mux.HandleFunc("GET /api/human-input/{id}", meta.getHumanInput)
	h.mux.HandleFunc("POST /api/human-input/{id}/respond", meta.respondHumanInput)
	h.mux.HandleFunc("GET /api/reference", meta.apiReference)
	h.mux.HandleFunc("GET /api/mcp", meta.apiMCP)
	h.mux.HandleFunc("POST /mcp", h.handleMCP)
	h.mux.HandleFunc("GET /mcp", h.handleMCP)
	h.mux.HandleFunc("OPTIONS /mcp", h.handleMCP)
	h.mux.HandleFunc("GET /api/events", meta.handleEvents)
	// Browser session auth (remote access): login page + cookie mint/clear.
	h.mux.HandleFunc("GET /login", h.serveLogin)
	h.mux.HandleFunc("POST /api/session/auth", h.sessionLogin)
	h.mux.HandleFunc("POST /api/session/logout", h.sessionLogout)
	h.mux.HandleFunc("GET /api/session/access-key", h.sessionAccessKey)
	// Share (remote access via Cloudflare quick tunnel).
	h.mux.HandleFunc("GET /api/share/status", h.shareStatus)
	h.mux.HandleFunc("POST /api/share/start", h.shareStart)
	h.mux.HandleFunc("POST /api/share/stop", h.shareStop)
	// Project merge (pull/push union with a peer).
	h.mux.HandleFunc("POST /api/merge/file", h.mergeFile)
	h.mux.HandleFunc("POST /api/merge/pull", h.mergePull)
	h.mux.HandleFunc("POST /api/merge/push", h.mergePush)
}
