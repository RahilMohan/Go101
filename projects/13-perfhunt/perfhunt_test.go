package perfhunt

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// approx reports whether a and b are equal within a small absolute
// tolerance (the handler serializes floats with 'g' formatting).
func approx(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// get issues a GET (or other method) against h and returns the recorder.
func doReq(t *testing.T, h http.HandlerFunc, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	if h == nil {
		t.Fatal("Handler() returned nil")
	}
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// ---------------------------------------------------------------------------
// 1. Aggregate: exact results on hand-built records
// ---------------------------------------------------------------------------

func TestAggregateKnownRecords(t *testing.T) {
	records := []Record{
		{ID: "a", Region: "eu", Value: 1.5},
		{ID: "b", Region: "us", Value: 2.25},
		{ID: "a", Region: "us", Value: -0.5}, // duplicate ID counts once in UniqueIDs
		{ID: "c", Region: "eu", Value: 4.0},
	}

	got := Aggregate(records)
	want := Summary{
		Count:     4,
		UniqueIDs: 3,
		Total:     7.25,
		Mean:      7.25 / 4,
		Min:       -0.5,
		Max:       4.0,
		ByRegion:  map[string]float64{"eu": 5.5, "us": 1.75},
	}

	if got.Count != want.Count || got.UniqueIDs != want.UniqueIDs {
		t.Fatalf("Count/UniqueIDs = %d/%d, want %d/%d", got.Count, got.UniqueIDs, want.Count, want.UniqueIDs)
	}
	for name, wv := range map[string]struct{ g, w float64 }{
		"Total": {got.Total, want.Total},
		"Mean":  {got.Mean, want.Mean},
		"Min":   {got.Min, want.Min},
		"Max":   {got.Max, want.Max},
	} {
		if !approx(wv.g, wv.w) {
			t.Errorf("%s = %v, want %v", name, wv.g, wv.w)
		}
	}
	if !reflect.DeepEqual(got.ByRegion, want.ByRegion) {
		t.Errorf("ByRegion = %#v, want %#v", got.ByRegion, want.ByRegion)
	}
}

func TestAggregateEmptyInput(t *testing.T) {
	got := Aggregate(nil)
	want := Summary{ByRegion: map[string]float64{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Aggregate(nil) = %#v, want zero Summary with empty ByRegion", got)
	}
	if got.Mean != 0 || got.Min != 0 || got.Max != 0 {
		t.Errorf("empty input must leave Mean/Min/Max at zero, got %v/%v/%v", got.Mean, got.Min, got.Max)
	}
}

// ---------------------------------------------------------------------------
// 2. TopK: ordering, ties, limits
// ---------------------------------------------------------------------------

func TestTopKOrderingTiesAndLimits(t *testing.T) {
	items := []string{
		"b", "b",
		"a", "a",
		"c", "c", "c",
		"d",
		"e", "e",
	}
	cases := []struct {
		name string
		k    int
		want []Count
	}{
		{"k=2 top two by count", 2, []Count{{Item: "c", Count: 3}, {Item: "a", Count: 2}}},
		{"ties broken alphabetically", 4, []Count{{Item: "c", Count: 3}, {Item: "a", Count: 2}, {Item: "b", Count: 2}, {Item: "e", Count: 2}}},
		{"k exceeds distinct items", 99, []Count{
			{Item: "c", Count: 3}, {Item: "a", Count: 2}, {Item: "b", Count: 2}, {Item: "e", Count: 2}, {Item: "d", Count: 1},
		}},
		{"k=1", 1, []Count{{Item: "c", Count: 3}}},
		{"k=0 empty", 0, []Count{}},
		{"negative k empty", -3, []Count{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TopK(items, tc.k)
			if len(tc.want) == 0 && len(got) != 0 {
				t.Fatalf("TopK(k=%d) = %v, want empty slice", tc.k, got)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("TopK(k=%d) = %v, want %v", tc.k, got, tc.want)
			}
		})
	}
}

func TestTopKEmptyInput(t *testing.T) {
	got := TopK(nil, 5)
	if len(got) != 0 {
		t.Fatalf("TopK(nil, 5) = %v, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// 3. Normalize: table-driven canonicalization
// ---------------------------------------------------------------------------

func TestNormalizeTable(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello", "hello"},
		{"  Hello,  World! ", "hello-world"},
		{"API-v2 Response", "api-v2-response"},
		{"multiple---dashes", "multiple-dashes"},
		{"!!!", ""},
		{"", ""},
		{"trailing dash -", "trailing-dash"},
		{"-leading dash", "leading-dash"},
		{"digits 123 ok", "digits-123-ok"},
	}
	for _, tc := range cases {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 4. Handler: routes, methods, payload shape
// ---------------------------------------------------------------------------

func TestHandlerStats(t *testing.T) {
	rec := doReq(t, Handler(), http.MethodGet, "/stats")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stats status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" {
		t.Error("Content-Type header missing on /stats")
	}

	var body struct {
		Count     int                `json:"count"`
		UniqueIDs int                `json:"unique_ids"`
		Total     float64            `json:"total"`
		Mean      float64            `json:"mean"`
		Min       float64            `json:"min"`
		Max       float64            `json:"max"`
		ByRegion  map[string]float64 `json:"by_region"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding /stats response: %v\nbody: %s", err, rec.Body.String())
	}

	recs, _ := dataset()
	want := Aggregate(recs)
	if body.Count != want.Count || body.UniqueIDs != want.UniqueIDs {
		t.Errorf("count/unique_ids = %d/%d, want %d/%d", body.Count, body.UniqueIDs, want.Count, want.UniqueIDs)
	}
	if !approx(body.Total, want.Total) || !approx(body.Mean, want.Mean) ||
		!approx(body.Min, want.Min) || !approx(body.Max, want.Max) {
		t.Errorf("numeric fields mismatch: got total=%v mean=%v min=%v max=%v, want %+v",
			body.Total, body.Mean, body.Min, body.Max, want)
	}
	if !reflect.DeepEqual(body.ByRegion, want.ByRegion) {
		t.Errorf("by_region = %#v, want %#v", body.ByRegion, want.ByRegion)
	}
}

func TestHandlerTagsDefaultAndParam(t *testing.T) {
	_, allTags := dataset()

	for _, tc := range []struct {
		target string
		k      int
	}{
		{"/tags", 5}, // default k
		{"/tags?k=2", 2},
	} {
		rec := doReq(t, Handler(), http.MethodGet, tc.target)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body: %s", tc.target, rec.Code, rec.Body.String())
		}
		var rows []struct {
			Item  string `json:"item"`
			Count int    `json:"count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatalf("decoding %s response: %v\nbody: %s", tc.target, err, rec.Body.String())
		}
		want := TopK(allTags, tc.k)
		if len(rows) != len(want) {
			t.Fatalf("%s returned %d rows, want %d", tc.target, len(rows), len(want))
		}
		for i, w := range want {
			if rows[i].Item != w.Item || rows[i].Count != w.Count {
				t.Errorf("%s row %d = {%s %d}, want {%s %d}", tc.target, i, rows[i].Item, rows[i].Count, w.Item, w.Count)
			}
		}
	}
}

func TestHandlerBadPathsAndMethods(t *testing.T) {
	h := Handler()
	if rec := doReq(t, h, http.MethodGet, "/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown path status = %d, want 404", rec.Code)
	}
	if rec := doReq(t, h, http.MethodPost, "/stats"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /stats status = %d, want 405", rec.Code)
	}
	if rec := doReq(t, h, http.MethodGet, "/tags?k=-1"); rec.Code != http.StatusOK {
		t.Errorf("GET /tags?k=-1 status = %d, want 200 (negative k -> empty)", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// 3b. Normalize: ASCII-only contract (review round 1, finding F1)
// ---------------------------------------------------------------------------

func TestNormalizeNonASCIIContract(t *testing.T) {
	// Contract: bytes >= 0x80 are separators. Unicode-aware lowering
	// must NOT smuggle non-ASCII runes into ASCII (e.g. U+212A KELVIN
	// SIGN lowers to 'k' under strings.ToLower but is out of spec here).
	cases := []struct{ in, want string }{
		{"\u212A", ""},       // Kelvin sign alone -> separator run -> trimmed
		{"A\u212AB", "a-b"},  // Kelvin sign splits A and B
		{"caf\u00E9", "caf"}, // é is a trailing separator
		{"\u00C5rge", "rge"}, // leading non-ASCII byte is a separator
	}
	for _, tc := range cases {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q (ASCII-only contract)", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 4b. Handler: k overflow falls back to default (review round 1, finding F2)
// ---------------------------------------------------------------------------

func TestHandlerTagsKOverflowDefault(t *testing.T) {
	for _, target := range []string{
		"/tags?k=99999999999999999999", // wraps int64 if unchecked
		"/tags?k=-99999999999999999999",
	} {
		rec := doReq(t, Handler(), http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body: %s", target, rec.Code, rec.Body.String())
		}
		var rows []struct {
			Item  string `json:"item"`
			Count int    `json:"count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatalf("decoding %s response: %v\nbody: %s", target, err, rec.Body.String())
		}
		_, allTags := dataset()
		want := TopK(allTags, 5) // overflow must keep default k=5
		if len(rows) != len(want) {
			t.Fatalf("%s returned %d rows, want %d (default k=5)", target, len(rows), len(want))
		}
		for i, w := range want {
			if rows[i].Item != w.Item || rows[i].Count != w.Count {
				t.Errorf("%s row %d = {%s %d}, want {%s %d}", target, i, rows[i].Item, rows[i].Count, w.Item, w.Count)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 5. Dataset sanity: the embedded data must be non-trivial and stable
// ---------------------------------------------------------------------------

func TestDatasetShape(t *testing.T) {
	recs, tags := dataset()
	const n = 5000
	if len(recs) != n {
		t.Fatalf("dataset size = %d, want %d", len(recs), n)
	}
	if len(tags) < n {
		t.Fatalf("tag stream size = %d, want >= %d", len(tags), n)
	}
	sum := Aggregate(recs).UniqueIDs
	if sum >= n {
		t.Errorf("UniqueIDs = %d, want collisions (deliberate duplicate IDs)", sum)
	}
	regions := make(map[string]int)
	for _, r := range recs {
		regions[r.Region]++
	}
	if len(regions) < 2 {
		t.Errorf("expected multiple regions, got %d", len(regions))
	}
}
