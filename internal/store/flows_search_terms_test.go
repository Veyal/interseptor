package store

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func Test_SplitNegativeSearch_separates_excluded_terms_from_the_positive_query(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		positive string
		exclude  []string
	}{
		{"empty", "", "", nil},
		{"plain term", "jpg", "jpg", nil},
		{"single exclusion", "-.jpg", "", []string{".jpg"}},
		{"mixed", "api -.jpg -analytics", "api", []string{".jpg", "analytics"}},
		{"quoted phrase stays positive", `"content type"`, "content type", nil},
		{"quoted exclusion phrase", `-"content type"`, "", []string{"content type"}},
		{"lone dash is text", "-", "-", nil},
		{"exclusion is case folded", "-Analytics", "", []string{"analytics"}},
		{"duplicates collapse", "-a -a", "", []string{"a"}},
		{"whitespace collapses", "  api   users ", "api users", nil},
		{"hyphen inside a term stays text", "content-type", "content-type", nil},
		{"double dash excludes a dashed term", "--x-requested-with", "", []string{"-x-requested-with"}},
		{"unterminated quote keeps the rest", `"foo bar`, "foo bar", nil},
		{"exclusion without a positive term keeps order", "-b -a", "", []string{"b", "a"}},
		{"tabs and newlines separate terms", "api\t-.png\n-analytics", "api", []string{".png", "analytics"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			positive, exclude := SplitNegativeSearch(tc.input)
			if positive != tc.positive {
				t.Fatalf("positive = %q, want %q", positive, tc.positive)
			}
			if !slices.Equal(exclude, tc.exclude) {
				t.Fatalf("exclude = %v, want %v", exclude, tc.exclude)
			}
		})
	}
}

func Test_FlowFilter_excludes_stored_search_surfaces(t *testing.T) {
	// Given: one flow per stored surface, each carrying an excluded marker.
	s := openExtensionStore(t)
	seed := []*Flow{
		{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "cdn.example.com", Path: "/hero-needle.jpg", Status: 200},
		{TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "cdn.example.com", Path: "/report-needle.pdf", Status: 200},
		{TS: time.UnixMilli(3), Method: "GET", Scheme: "https", Host: "analytics-needle.example.com", Path: "/collect", Status: 200},
		{TS: time.UnixMilli(4), Method: "GET", Scheme: "https", Host: "app.example.com", Path: "/profile", Status: 200, Note: "needle-note"},
		{TS: time.UnixMilli(5), Method: "GET", Scheme: "https", Host: "app.example.com", Path: "/traced", Status: 200, ResHeaders: map[string][]string{"X-Trace": {"needle-header"}}},
		{TS: time.UnixMilli(6), Method: "GET", Scheme: "https", Host: "app.example.com", Path: "/keep", Status: 200},
	}
	// termForSeed names, per seeded flow, the term that must drop it.
	termForSeed := []string{"needle.jpg", "needle.pdf", "analytics-needle", "needle-note", "needle-header", "needle-tag"}
	byTerm := map[string]int64{}
	for i := range seed {
		id, err := s.InsertFlow(seed[i])
		if err != nil {
			t.Fatal(err)
		}
		byTerm[termForSeed[i]] = id
	}
	kept := byTerm["needle-tag"]
	if _, err := s.SetFlowTags(kept, []string{"needle-tag"}); err != nil {
		t.Fatal(err)
	}

	// When: each excluded term names exactly one flow the search must drop.
	terms := []string{"needle.jpg", "NEEDLE.PDF", "analytics-needle", "needle-note", "needle-header"}
	for _, term := range terms {
		flows, err := s.QueryFlowsListFilter(FlowFilter{Limit: 20, NotTerms: []string{term}})
		if err != nil {
			t.Fatalf("exclude %q: %v", term, err)
		}
		if len(flows) != len(seed)-1 {
			t.Fatalf("exclude %q left %d flows, want %d", term, len(flows), len(seed)-1)
		}
		dropped := byTerm[strings.ToLower(term)]
		for _, fl := range flows {
			if fl.ID == dropped {
				t.Fatalf("exclude %q kept flow %d", term, dropped)
			}
		}
		if dropped == kept {
			t.Fatalf("test setup: %q names the kept flow", term)
		}
	}

	// A tag is a stored surface too.
	flows, err := s.QueryFlowsListFilter(FlowFilter{Limit: 20, NotTerms: []string{"needle-tag"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != len(seed)-1 {
		t.Fatalf("exclude by tag left %d flows, want %d", len(flows), len(seed)-1)
	}

	// Multiple exclusions are ANDed, so each row is dropped by its own term.
	combined := append(append([]string{}, terms...), "needle-tag")
	if flows, err := s.QueryFlowsListFilter(FlowFilter{Limit: 20, NotTerms: combined}); err != nil || len(flows) != 0 {
		t.Fatalf("combined exclusions = %+v, err=%v", flows, err)
	}

	// An exclusion with a positive search narrows it instead of replacing it.
	if flows, err := s.QueryFlowsListFilter(FlowFilter{Limit: 20, Search: "cdn", NotTerms: []string{".pdf"}}); err != nil || len(flows) != 1 || flows[0].ID != byTerm["needle.jpg"] {
		t.Fatalf("search+exclude = %+v, err=%v", flows, err)
	}
	// The full-text path reads the "-" tokens out of the search string itself.
	if flows, err := s.QueryFlowsListFilter(FlowFilter{Limit: 20, Search: "cdn -.pdf"}); err != nil || len(flows) != 1 || flows[0].ID != byTerm["needle.jpg"] {
		t.Fatalf("full-text search+exclude = %+v, err=%v", flows, err)
	}
}

func openExtensionStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func Test_SearchFlowsAnywhere_drops_flows_named_by_an_excluded_term(t *testing.T) {
	// Given: three flows that share a positive term, one per noise class.
	s := openExtensionStore(t)
	jpg, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "cdn.example.com", Path: "/asset-needle.jpg", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	png, err := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "cdn.example.com", Path: "/asset-needle.png", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	analytics, err := s.InsertFlow(&Flow{TS: time.UnixMilli(3), Method: "GET", Scheme: "https", Host: "analytics.example.com", Path: "/asset-needle", Status: 200})
	if err != nil {
		t.Fatal(err)
	}

	// When / Then: each exclusion removes exactly the flow it names.
	cases := []struct {
		search string
		want   []int64
	}{
		{"asset-needle", []int64{analytics, png, jpg}},
		{"asset-needle -.jpg", []int64{analytics, png}},
		{"asset-needle -analytics -png", []int64{jpg}},
		// An exclusion works without a positive term: "hide these everywhere".
		{"-.jpg -.pdf", []int64{analytics, png}},
	}
	for _, tc := range cases {
		ids, _, searchErr := anywhereSearchIDs(s, FlowFilter{Search: tc.search}, 8000)
		if searchErr != nil {
			t.Fatalf("search %q: %v", tc.search, searchErr)
		}
		slices.Sort(ids)
		want := append([]int64(nil), tc.want...)
		slices.Sort(want)
		if !slices.Equal(ids, want) {
			t.Fatalf("search %q ids = %v, want %v", tc.search, ids, want)
		}
	}
}

func Test_SearchFlowsBody_excludes_body_text_and_ignores_stored_fields(t *testing.T) {
	// Given: two bodies with the same positive term, only one naming the noise.
	s := openExtensionStore(t)
	noisy, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "app.example.com", Path: "/noisy", Status: 200, ResBodyHash: writeAnywhereTestBody(t, s, `{"token":"needle-body","telemetry":"analytics-script"}`)})
	if err != nil {
		t.Fatal(err)
	}
	clean, err := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "POST", Scheme: "https", Host: "app.example.com", Path: "/analytics-in-path", Status: 200, ResBodyHash: writeAnywhereTestBody(t, s, `{"token":"needle-body"}`)})
	if err != nil {
		t.Fatal(err)
	}

	// When: body scope excludes on bodies, so a path hit is not enough to drop it,
	// and a body hit is.
	ids, _, _, err := s.FlowIDsBodySearch(FlowFilter{Search: "needle-body -analytics-script"}, 8000)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []int64{clean}) {
		t.Fatalf("body ids = %v, want [%d] (noisy=%d)", ids, clean, noisy)
	}

	// Then: an exclusion with no positive term has no body to match, so nothing.
	if ids, _, _, err := s.FlowIDsBodySearch(FlowFilter{Search: "-analytics-script"}, 8000); err != nil || len(ids) != 0 {
		t.Fatalf("exclusion-only body search = %v, err=%v", ids, err)
	}
}

func Test_SearchFlowsAnywhere_exclusions_match_stored_fields_only(t *testing.T) {
	// Given: a flow whose URL carries the positive term and whose body carries the
	// excluded one.
	s := openExtensionStore(t)
	bodyHit, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "app.example.com", Path: "/asset-needle", Status: 200, ResBodyHash: writeAnywhereTestBody(t, s, "analytics-script")})
	if err != nil {
		t.Fatal(err)
	}

	// When: the default (anywhere) scope reads bodies for the positive term only.
	ids, _, err := anywhereSearchIDs(s, FlowFilter{Search: "asset-needle -analytics-script"}, 8000)
	if err != nil {
		t.Fatal(err)
	}

	// Then: the stored-field match stands; the documented contract is that an
	// anywhere exclusion matches stored fields, not body text.
	if !slices.Equal(ids, []int64{bodyHit}) {
		t.Fatalf("anywhere ids = %v, want [%d]", ids, bodyHit)
	}
}

func Test_QueryFlowsFilter_id_lookup_still_resolves_with_an_exclusion(t *testing.T) {
	s := openExtensionStore(t)
	id, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "app.example.com", Path: "/profile", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	flows, err := s.QueryFlowsFilter(FlowFilter{Limit: 10, Search: "#" + strconv.FormatInt(id, 10) + " -nothing-matches"})
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 || flows[0].ID != id {
		t.Fatalf("id lookup with exclusion = %+v", flows)
	}
	// An exclusion the flow does name still drops it.
	if flows, err := s.QueryFlowsFilter(FlowFilter{Limit: 10, Search: "#" + strconv.FormatInt(id, 10) + " -profile"}); err != nil || len(flows) != 0 {
		t.Fatalf("id lookup with matching exclusion = %+v, err=%v", flows, err)
	}
}
