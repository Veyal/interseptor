package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// CollectionMergeStats reports a collections merge. Merge is additive and
// idempotent: re-running it converges without duplicates.
type CollectionMergeStats struct {
	CollectionsAdded   int `json:"collectionsAdded"`
	CollectionsUpdated int `json:"collectionsUpdated"`
	CollectionsSkipped int `json:"collectionsSkipped"`
	ItemsAdded         int `json:"itemsAdded"`
	ItemsUpdated       int `json:"itemsUpdated"`
	ItemsSkipped       int `json:"itemsSkipped"`
	ItemsOrphaned      int `json:"itemsOrphaned"`
	EnvironmentsAdded  int `json:"environmentsAdded"`
	VariablesAdded     int `json:"variablesAdded"`
}

// MergeCollectionsFrom unions a peer project's collections, items and
// environments (initial values only) into this project. A peer without the
// collection tables (older version) is a no-op. mergeFrom in merge.go should
// call this after the flow/finding union so one project merge carries
// collections too.
func (s *Store) MergeCollectionsFrom(peerDBPath string) (CollectionMergeStats, error) {
	peer, err := sql.Open("sqlite", "file:"+peerDBPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return CollectionMergeStats{}, fmt.Errorf("open peer db: %w", err)
	}
	defer peer.Close()
	b, err := loadCollBundle(peer)
	if err != nil {
		return CollectionMergeStats{}, fmt.Errorf("read peer collections: %w", err)
	}
	return s.mergeCollBundle(b)
}

// peerHasCollections reports whether the peer DB carries the collection tables.
func peerHasCollections(peer *sql.DB) bool {
	ok, _ := peerHasTable(peer, "ix_collections")
	return ok
}

// mergeCollectionsFromDB merges the collections of an already-open peer DB.
// hasColl is false (and nothing happens) for a peer without collection tables.
func (s *Store) mergeCollectionsFromDB(peer *sql.DB) (st CollectionMergeStats, hasColl bool, err error) {
	if !peerHasCollections(peer) {
		return st, false, nil
	}
	b, err := loadCollBundle(peer)
	if err != nil {
		return st, true, fmt.Errorf("read peer collections: %w", err)
	}
	st, err = s.mergeCollBundle(b)
	return st, true, err
}

// previewCollectionsFromDB counts what a collections merge would add without
// writing. Matching mirrors mergeCollBundle (uid, then case-folded name); item
// counts are an upper bound because signature-level dedupe is not simulated.
func (s *Store) previewCollectionsFromDB(peer *sql.DB) (st CollectionMergeStats, hasColl bool, err error) {
	if !peerHasCollections(peer) {
		return st, false, nil
	}
	b, err := loadCollBundle(peer)
	if err != nil {
		return st, true, fmt.Errorf("read peer collections: %w", err)
	}
	local, err := loadCollBundle(s.db) // tolerates a project without collection tables
	if err != nil {
		return st, true, err
	}
	byUID, byName := map[string]bool{}, map[string]bool{}
	for _, c := range local.Collections {
		byUID[c.UID], byName[strings.ToLower(c.Name)] = true, true
	}
	isNew := map[string]bool{}
	for _, c := range b.Collections {
		if byUID[c.UID] || byName[strings.ToLower(c.Name)] {
			st.CollectionsSkipped++
		} else {
			st.CollectionsAdded++
			isNew[c.UID] = true
		}
	}
	localItems := map[string]bool{}
	for _, it := range local.Items {
		localItems[it.UID] = true
	}
	for _, it := range b.Items {
		if localItems[it.UID] {
			st.ItemsSkipped++
		} else {
			st.ItemsAdded++
		}
	}
	localEnv := map[string]bool{}
	for _, e := range local.Environments {
		localEnv[strings.ToLower(e.Name)+"\x00"+e.CollectionUID+"\x00"+e.Kind] = true
	}
	for _, e := range b.Environments {
		if !localEnv[strings.ToLower(e.Name)+"\x00"+e.CollectionUID+"\x00"+e.Kind] {
			st.EnvironmentsAdded++
		}
	}
	return st, true, nil
}

// itemSig identifies an item by its place and shape when uids do not match.
func itemSig(coll string, path []string, it *Item) string {
	return coll + "\x00" + strings.Join(path, "\x01") + "\x00" + it.Kind + "\x00" + it.Name + "\x00" + it.Method + "\x00" + string(it.URL)
}

// itemPaths maps uid -> ancestor name path for a set of items.
func itemPaths(items []Item) map[string][]string {
	byUID := make(map[string]*Item, len(items))
	for i := range items {
		byUID[items[i].UID] = &items[i]
	}
	paths := make(map[string][]string, len(items))
	var path func(uid string, depth int) []string
	path = func(uid string, depth int) []string {
		if p, ok := paths[uid]; ok {
			return p
		}
		it := byUID[uid]
		if it == nil || it.ParentUID == "" || depth > 64 {
			paths[uid] = nil
			return nil
		}
		parent := byUID[it.ParentUID]
		if parent == nil {
			paths[uid] = nil
			return nil
		}
		p := append(append([]string{}, path(parent.UID, depth+1)...), parent.Name)
		paths[uid] = p
		return p
	}
	for uid := range byUID {
		path(uid, 0)
	}
	return paths
}

func (s *Store) mergeCollBundle(b CollectionsBundle) (CollectionMergeStats, error) {
	var st CollectionMergeStats
	if len(b.Collections) == 0 && len(b.Environments) == 0 {
		return st, nil
	}
	if err := s.ensureCollections(); err != nil {
		return st, err
	}
	local, err := loadCollBundle(s.db)
	if err != nil {
		return st, err
	}
	// Incoming data is never trusted to be clean.
	scrubBundle(&b)

	localCollByUID := map[string]*Collection{}
	localCollByName := map[string]*Collection{}
	for i := range local.Collections {
		c := &local.Collections[i]
		localCollByUID[c.UID] = c
		localCollByName[strings.ToLower(c.Name)] = c
	}
	collMap := map[string]string{} // peer coll uid -> local coll uid
	for _, pc := range b.Collections {
		if lc, ok := localCollByUID[pc.UID]; ok {
			collMap[pc.UID] = lc.UID
			if pc.Rev > lc.Rev {
				upd := pc
				upd.UID, upd.Rev = lc.UID, 0
				upd.ScopePolicy, upd.Caps = lc.ScopePolicy, lc.Caps // policy/caps stay local
				if _, err := s.UpdateCollection(upd); err != nil {
					return st, err
				}
				st.CollectionsUpdated++
			} else {
				st.CollectionsSkipped++
			}
			continue
		}
		if lc, ok := localCollByName[strings.ToLower(pc.Name)]; ok {
			collMap[pc.UID] = lc.UID
			st.CollectionsSkipped++
			continue
		}
		nc := pc
		nc.Caps = nil // default-deny capabilities: never inherited from a peer
		if nc.ScopePolicy == ScopePolicyOff || !validScopePolicy(nc.ScopePolicy) {
			nc.ScopePolicy = ScopePolicyBlock
		}
		if strings.TrimSpace(nc.Name) == "" {
			nc.Name = "Imported collection"
		}
		if _, err := s.CreateCollection(nc); err != nil {
			return st, fmt.Errorf("merge collection %q: %w", pc.Name, err)
		}
		collMap[pc.UID] = nc.UID
		st.CollectionsAdded++
	}

	if err := s.mergeCollItems(b, local, collMap, &st); err != nil {
		return st, err
	}
	if err := s.mergeCollEnvs(b, local, collMap, &st); err != nil {
		return st, err
	}
	return st, nil
}

func (s *Store) mergeCollItems(b CollectionsBundle, local CollectionsBundle, collMap map[string]string, st *CollectionMergeStats) error {
	localByUID := map[string]*Item{}
	localPaths := itemPaths(local.Items)
	localSig := map[string]string{} // signature -> uid
	for i := range local.Items {
		it := &local.Items[i]
		localByUID[it.UID] = it
		localSig[itemSig(it.CollectionUID, localPaths[it.UID], it)] = it.UID
	}
	// Order peer items so parents come before children (stable by depth).
	peerPaths := itemPaths(b.Items)
	pending := append([]Item(nil), b.Items...)
	sort.SliceStable(pending, func(i, j int) bool { return len(peerPaths[pending[i].UID]) < len(peerPaths[pending[j].UID]) })
	itemMap := map[string]string{} // peer item uid -> local item uid
	for _, pi := range pending {
		lcoll, ok := collMap[pi.CollectionUID]
		if !ok {
			st.ItemsOrphaned++
			continue
		}
		parent := ""
		if pi.ParentUID != "" {
			mp, ok := itemMap[pi.ParentUID]
			if !ok {
				st.ItemsOrphaned++
				continue
			}
			parent = mp
		}
		if li, ok := localByUID[pi.UID]; ok && li.CollectionUID == lcoll {
			itemMap[pi.UID] = li.UID
			if pi.Rev > li.Rev {
				upd := pi
				upd.Rev, upd.ParentUID, upd.Rank = 0, parent, li.Rank
				if _, err := s.UpdateItem(upd, CollChange{Actor: "merge", Source: "merge"}); err != nil {
					return err
				}
				// Keep the peer's revision number so "higher rev wins" converges.
				if _, err := s.db.Exec(`UPDATE ix_items SET rev=? WHERE uid=?`, pi.Rev, pi.UID); err != nil {
					return err
				}
				st.ItemsUpdated++
			} else {
				st.ItemsSkipped++
			}
			continue
		}
		cand := pi
		cand.CollectionUID, cand.ParentUID = lcoll, parent
		path := localPathFor(local.Items, localPaths, parent, s)
		if uid, ok := localSig[itemSig(lcoll, path, &cand)]; ok {
			itemMap[pi.UID] = uid
			st.ItemsSkipped++
			continue
		}
		if _, taken := localByUID[pi.UID]; taken {
			cand.UID = "" // uid belongs to another collection locally
		}
		ni, err := s.CreateItem(cand)
		if err != nil {
			return fmt.Errorf("merge item %q: %w", pi.Name, err)
		}
		itemMap[pi.UID] = ni.UID
		ni.ParentUID = parent
		localByUID[ni.UID] = ni
		localSig[itemSig(lcoll, path, ni)] = ni.UID
		st.ItemsAdded++
	}
	// Variables owned by collections/items follow the uid maps; add-missing only.
	return s.mergeOwnedVars(b.Variables, func(kind, uid string) (string, bool) {
		switch kind {
		case VarOwnerCollection:
			u, ok := collMap[uid]
			return u, ok
		case VarOwnerFolder, VarOwnerRequest:
			u, ok := itemMap[uid]
			return u, ok
		}
		return "", false
	}, st)
}

// localPathFor returns the ancestor name path of a local parent item uid
// (including the parent itself), looking in loaded items and the live DB for
// rows created earlier in this merge.
func localPathFor(items []Item, paths map[string][]string, parent string, s *Store) []string {
	if parent == "" {
		return nil
	}
	var names []string
	cur := parent
	for depth := 0; cur != "" && depth < 64; depth++ {
		var name, up string
		if err := s.db.QueryRow(`SELECT name,parent_uid FROM ix_items WHERE uid=?`, cur).Scan(&name, &up); err != nil {
			break
		}
		names = append([]string{name}, names...)
		cur = up
	}
	return names
}

func (s *Store) mergeOwnedVars(vars []Variable, mapOwner func(kind, uid string) (string, bool), st *CollectionMergeStats) error {
	byOwner := map[[2]string][]Variable{}
	for _, v := range vars {
		if u, ok := mapOwner(v.OwnerKind, v.OwnerUID); ok {
			v.OwnerUID = u
			k := [2]string{v.OwnerKind, u}
			byOwner[k] = append(byOwner[k], v)
		}
	}
	for k, vs := range byOwner {
		cur, err := s.ListVariables(k[0], k[1])
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, c := range cur {
			have[c.Key] = true
		}
		merged := cur
		added := 0
		for _, v := range vs {
			if !have[v.Key] {
				merged = append(merged, v)
				added++
			}
		}
		if added > 0 {
			if err := s.SetVariables(k[0], k[1], merged); err != nil {
				return err
			}
			st.VariablesAdded += added
		}
	}
	return nil
}

func (s *Store) mergeCollEnvs(b CollectionsBundle, local CollectionsBundle, collMap map[string]string, st *CollectionMergeStats) error {
	type key struct{ name, coll, kind string }
	localEnv := map[key]string{}
	for _, e := range local.Environments {
		localEnv[key{strings.ToLower(e.Name), e.CollectionUID, e.Kind}] = e.UID
	}
	localUIDs := map[string]bool{}
	for _, e := range local.Environments {
		localUIDs[e.UID] = true
	}
	envMap := map[string]string{}
	for _, pe := range b.Environments {
		coll := ""
		if pe.CollectionUID != "" {
			mc, ok := collMap[pe.CollectionUID]
			if !ok {
				continue
			}
			coll = mc
		}
		k := key{strings.ToLower(pe.Name), coll, pe.Kind}
		if uid, ok := localEnv[k]; ok {
			envMap[pe.UID] = uid
			continue
		}
		ne := Environment{UID: pe.UID, Name: pe.Name, Kind: pe.Kind, CollectionUID: coll,
			BoundIdentity: pe.BoundIdentity, BaseTargetPin: pe.BaseTargetPin}
		if localUIDs[ne.UID] {
			ne.UID = ""
		}
		created, err := s.CreateEnvironment(ne)
		if err != nil {
			return fmt.Errorf("merge environment %q: %w", pe.Name, err)
		}
		envMap[pe.UID] = created.UID
		localEnv[k] = created.UID
		st.EnvironmentsAdded++
	}
	return s.mergeOwnedVars(b.Variables, func(kind, uid string) (string, bool) {
		if kind != VarOwnerEnvironment {
			return "", false
		}
		u, ok := envMap[uid]
		return u, ok
	}, st)
}
