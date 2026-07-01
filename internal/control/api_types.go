package control

// Domain-scoped API facades embed *Hub so handlers stay in focused files while
// sharing store, broadcast, and subsystem wiring.

type flowAPI struct{ *Hub }
type interceptAPI struct{ *Hub }
type settingsAPI struct{ *Hub }
type scopeAPI struct{ *Hub }
type findingsAPI struct{ *Hub }
type toolsAPI struct{ *Hub }
type scannerAPI struct{ *Hub }
type checksAPI struct{ *Hub }
type aiAPI struct{ *Hub }
type projectAPI struct{ *Hub }
type androidAPI struct{ *Hub }
type iosAPI struct{ *Hub }
type oobAPI struct{ *Hub }
type authzAPI struct{ *Hub }
type discoveryAPI struct{ *Hub }
type activescanAPI struct{ *Hub }
type sessionAPI struct{ *Hub }
type metaAPI struct{ *Hub }
