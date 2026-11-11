package store

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// r3ReferenceMutateTags is the per-flow implementation MutateFlowTagsBulk
// replaces, kept here as the oracle: one statement per flow and tag.
func r3ReferenceMutateTags(s *Store, ids []int64, add, remove []string) error {
	add, remove = NormalizeTags(add), NormalizeTags(remove)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if err := requireFlowForTags(tx, id); err != nil {
			return err
		}
		for _, tag := range add {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO flow_tags (flow_id, tag) VALUES (?,?)`, id, tag); err != nil {
				return err
			}
		}
		for _, tag := range remove {
			if _, err := tx.Exec(`DELETE FROM flow_tags WHERE flow_id=? AND tag=?`, id, tag); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func r3TagState(t *testing.T, s *Store) map[int64]string {
	t.Helper()
	rows, err := s.db.Query(`SELECT flow_id, group_concat(tag, ',') FROM (SELECT flow_id, tag FROM flow_tags ORDER BY flow_id, tag) GROUP BY flow_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var (
			id   int64
			tags string
		)
		if err := rows.Scan(&id, &tags); err != nil {
			t.Fatal(err)
		}
		out[id] = tags
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Random add/remove commands over random selections (with duplicates, several
// IN-list chunks long) leave the same tags as the per-flow implementation, and
// the returned set is exactly the flows whose tags differ afterwards.
func TestR3BulkTagsMatchPerFlowImplementationOnRandomData(t *testing.T) {
	const n = 3*tagBulkChunk + 77
	bulk, ref := seedReviewDB(t, n), seedReviewDB(t, n)
	rng := rand.New(rand.NewSource(20261111))
	pool := []string{"auth", "idor-candidate", "Reviewed", "todo", "x y", "p1", "", "##"}
	pick := func() []string {
		var out []string
		for _, tag := range pool {
			if rng.Intn(3) == 0 {
				out = append(out, tag)
			}
		}
		return out
	}
	for round := 0; round < 40; round++ {
		size := 1 + rng.Intn(n)
		if round%4 == 0 {
			size = 1 + rng.Intn(5)
		}
		ids := make([]int64, 0, size+3)
		for i := 0; i < size; i++ {
			ids = append(ids, int64(1+rng.Intn(n)))
		}
		ids = append(ids, ids[0], ids[len(ids)/2]) // duplicates
		add, remove := pick(), pick()
		before := r3TagState(t, bulk)

		changed, err := bulk.MutateFlowTagsBulk(ids, add, remove)
		if err != nil {
			t.Fatalf("round %d: bulk: %v", round, err)
		}
		if err := r3ReferenceMutateTags(ref, ids, add, remove); err != nil {
			t.Fatalf("round %d: reference: %v", round, err)
		}
		after, want := r3TagState(t, bulk), r3TagState(t, ref)
		if !reflect.DeepEqual(after, want) {
			t.Fatalf("round %d (add %v remove %v, %d ids): tags differ from the per-flow implementation", round, add, remove, len(ids))
		}
		var wantChanged []int64
		seen := map[int64]bool{}
		for _, id := range ids {
			if !seen[id] && before[id] != after[id] {
				wantChanged = append(wantChanged, id)
			}
			seen[id] = true
		}
		got := append([]int64(nil), changed...)
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
		sort.Slice(wantChanged, func(i, j int) bool { return wantChanged[i] < wantChanged[j] })
		if !reflect.DeepEqual(got, wantChanged) {
			t.Fatalf("round %d: changed = %d flows, want %d (%v vs %v)", round, len(got), len(wantChanged), r3Head(got), r3Head(wantChanged))
		}
	}
	// The tag index answers the same as the table.
	var viaIndex, viaTable int
	if err := bulk.db.QueryRow(`SELECT COUNT(*) FROM flow_tags INDEXED BY idx_flow_tags_tag WHERE tag = 'auth'`).Scan(&viaIndex); err != nil {
		t.Fatal(err)
	}
	if err := bulk.db.QueryRow(`SELECT COUNT(*) FROM flow_tags NOT INDEXED WHERE tag = 'auth'`).Scan(&viaTable); err != nil {
		t.Fatal(err)
	}
	if viaIndex != viaTable {
		t.Fatalf("tag index counts %d, table %d", viaIndex, viaTable)
	}
	var integrity string
	if err := bulk.db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check = %q, %v", integrity, err)
	}
}

func r3Head(ids []int64) []int64 {
	if len(ids) > 8 {
		return ids[:8]
	}
	return ids
}

// A selection naming a flow that does not exist is rejected as a whole, also
// when the missing id sits in a later chunk than flows already tagged.
func TestR3BulkTagsRejectAMissingFlowWithoutPartialTags(t *testing.T) {
	const n = 2*tagBulkChunk + 10
	s := seedReviewDB(t, n)
	before := r3TagState(t, s)
	ids := make([]int64, 0, n+1)
	for i := 1; i <= n; i++ {
		ids = append(ids, int64(i))
	}
	ids = append(ids, 999_999_999)
	changed, err := s.MutateFlowTagsBulk(ids, []string{"phantom"}, []string{"auth"})
	if err == nil {
		t.Fatalf("bulk tag of a missing flow succeeded (changed %d)", len(changed))
	}
	if !reflect.DeepEqual(r3TagState(t, s), before) {
		t.Fatal("a rejected bulk tag left tags behind")
	}
}

// One failing row in a late chunk rolls back the rows of earlier chunks.
func TestR3BulkTagsAreAllOrNothingAcrossChunks(t *testing.T) {
	const n = 2*tagBulkChunk + 10
	s := seedReviewDB(t, n)
	before := r3TagState(t, s)
	if _, err := s.db.Exec(fmt.Sprintf(`CREATE TRIGGER r3_reject BEFORE INSERT ON flow_tags
		WHEN NEW.flow_id = %d AND NEW.tag = 'batch' BEGIN SELECT RAISE(ABORT, 'r3 rejected'); END`, n-1)); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, n)
	for i := 1; i <= n; i++ {
		ids = append(ids, int64(i))
	}
	if changed, err := s.MutateFlowTagsBulk(ids, []string{"batch"}, nil); err == nil {
		t.Fatalf("bulk tag succeeded (changed %d) although a row was rejected", len(changed))
	}
	if !reflect.DeepEqual(r3TagState(t, s), before) {
		t.Fatal("a failed bulk tag left an early prefix committed")
	}
}

// Tagging again reports no changed flow; only the flows that lacked the tag
// are reported when part of the selection already has it.
func TestR3BulkTagsReportOnlyFlowsThatChanged(t *testing.T) {
	s := seedReviewDB(t, 200)
	ids := []int64{1, 2, 3, 4, 5}
	changed, err := s.MutateFlowTagsBulk(ids, []string{"fresh"}, nil)
	if err != nil || len(changed) != 5 {
		t.Fatalf("first tag: changed %v, err %v", changed, err)
	}
	changed, err = s.MutateFlowTagsBulk([]int64{4, 5, 6, 7}, []string{"fresh"}, nil)
	if err != nil || !reflect.DeepEqual(changed, []int64{6, 7}) {
		t.Fatalf("second tag: changed %v, err %v; want [6 7]", changed, err)
	}
	changed, err = s.MutateFlowTagsBulk(ids, []string{"fresh"}, nil)
	if err != nil || len(changed) != 0 {
		t.Fatalf("repeat tag: changed %v, err %v; want none", changed, err)
	}
	// Remove wins over add for the same tag; a flow without it is unchanged.
	changed, err = s.MutateFlowTagsBulk([]int64{1, 150}, []string{"fresh"}, []string{"FRESH"})
	if err != nil || !reflect.DeepEqual(changed, []int64{1}) {
		t.Fatalf("add+remove: changed %v, err %v; want [1]", changed, err)
	}
	if tags, _ := s.FlowTags(150); len(tags) != 0 {
		t.Fatalf("flow 150 has tags %v after add+remove of the same tag", tags)
	}
}
