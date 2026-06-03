package provider

import (
	"errors"
	"testing"
)

// rows builds a slice of n opaque page items labeled by index, mimicking the
// shape json.Unmarshal into interface{} produces (each row a map).
func rows(start, end int) []interface{} {
	out := make([]interface{}, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, map[string]interface{}{"id": float64(i)})
	}
	return out
}

// TestAggregatePagesSinglePage: when the first page already holds totalCount
// rows, no follow-up GET is issued and the envelope is returned reconciled.
func TestAggregatePagesSinglePage(t *testing.T) {
	first := map[string]interface{}{
		"data":       rows(0, 2),
		"totalCount": float64(2),
		"count":      float64(2),
		"offset":     float64(0),
		"limit":      float64(25),
	}
	fetched := false
	fetch := func(offset, limit int) (map[string]interface{}, error) {
		fetched = true
		return nil, errors.New("should not fetch when first page is complete")
	}

	out, err := aggregatePages(first, fetch)
	if err != nil {
		t.Fatalf("aggregatePages: %v", err)
	}
	if fetched {
		t.Error("fetched a second page when the first was already complete")
	}
	if got := len(toSlice(out["data"])); got != 2 {
		t.Errorf("data len = %d, want 2", got)
	}
	if n, _ := toInt(out["count"]); n != 2 {
		t.Errorf("count = %v, want 2", out["count"])
	}
}

// TestAggregatePagesMultiPage: rows arrive across three windows. Each follow-up
// GET must request offset=len(all-so-far) and limit=listPageLimit, and the rows
// concatenate in order until totalCount is reached.
func TestAggregatePagesMultiPage(t *testing.T) {
	first := map[string]interface{}{
		"data":       rows(0, 2),
		"totalCount": float64(5),
	}
	pages := []map[string]interface{}{
		{"data": rows(2, 4)},
		{"data": rows(4, 5)},
	}
	var gotOffsets, gotLimits []int
	idx := 0
	fetch := func(offset, limit int) (map[string]interface{}, error) {
		gotOffsets = append(gotOffsets, offset)
		gotLimits = append(gotLimits, limit)
		p := pages[idx]
		idx++
		return p, nil
	}

	out, err := aggregatePages(first, fetch)
	if err != nil {
		t.Fatalf("aggregatePages: %v", err)
	}

	data := toSlice(out["data"])
	if len(data) != 5 {
		t.Fatalf("data len = %d, want 5", len(data))
	}
	for i, item := range data {
		if id, _ := toInt(item.(map[string]interface{})["id"]); id != i {
			t.Errorf("data[%d].id = %d, want %d (order not preserved)", i, id, i)
		}
	}
	if want := []int{2, 4}; !equalInts(gotOffsets, want) {
		t.Errorf("fetch offsets = %v, want %v", gotOffsets, want)
	}
	if want := []int{listPageLimit, listPageLimit}; !equalInts(gotLimits, want) {
		t.Errorf("fetch limits = %v, want %v", gotLimits, want)
	}
	// Envelope reconciled to the full set.
	if n, _ := toInt(out["count"]); n != 5 {
		t.Errorf("count = %v, want 5", out["count"])
	}
	if n, _ := toInt(out["offset"]); n != 0 {
		t.Errorf("offset = %v, want 0", out["offset"])
	}
	if n, _ := toInt(out["limit"]); n != 5 {
		t.Errorf("limit = %v, want 5", out["limit"])
	}
}

// TestAggregatePagesEmptyPageTerminates: a server that reports a totalCount it
// never fills must not loop forever — an empty page ends aggregation.
func TestAggregatePagesEmptyPageTerminates(t *testing.T) {
	first := map[string]interface{}{
		"data":       rows(0, 2),
		"totalCount": float64(10), // lies; server returns no more rows
	}
	calls := 0
	fetch := func(offset, limit int) (map[string]interface{}, error) {
		calls++
		if calls > 3 {
			t.Fatalf("aggregatePages did not terminate on empty page (%d calls)", calls)
		}
		return map[string]interface{}{"data": []interface{}{}}, nil
	}

	out, err := aggregatePages(first, fetch)
	if err != nil {
		t.Fatalf("aggregatePages: %v", err)
	}
	if got := len(toSlice(out["data"])); got != 2 {
		t.Errorf("data len = %d, want 2 (only the first page survived)", got)
	}
}

// TestAggregatePagesFetchError: a fetch failure aborts and propagates.
func TestAggregatePagesFetchError(t *testing.T) {
	first := map[string]interface{}{
		"data":       rows(0, 2),
		"totalCount": float64(10),
	}
	fetch := func(offset, limit int) (map[string]interface{}, error) {
		return nil, errors.New("boom")
	}

	if _, err := aggregatePages(first, fetch); err == nil {
		t.Error("aggregatePages = nil error, want propagated fetch failure")
	}
}

// TestAggregatePagesMissingTotalCount: with no totalCount, aggregation runs to
// the empty-page terminator and backfills totalCount from the assembled set.
func TestAggregatePagesMissingTotalCount(t *testing.T) {
	first := map[string]interface{}{
		"data": rows(0, 2),
	}
	// No totalCount key, so toInt(first["totalCount"]) is (0,false) -> loop.
	pages := []map[string]interface{}{
		{"data": rows(2, 3)},
		{"data": []interface{}{}},
	}
	idx := 0
	fetch := func(offset, limit int) (map[string]interface{}, error) {
		p := pages[idx]
		idx++
		return p, nil
	}

	out, err := aggregatePages(first, fetch)
	if err != nil {
		t.Fatalf("aggregatePages: %v", err)
	}
	if got := len(toSlice(out["data"])); got != 3 {
		t.Errorf("data len = %d, want 3", got)
	}
	if n, _ := toInt(out["totalCount"]); n != 3 {
		t.Errorf("totalCount = %v, want backfilled 3", out["totalCount"])
	}
}

// TestToInt covers the JSON-number cases: decoded bodies carry float64, but an
// int set by our own reconciliation must also read back.
func TestToInt(t *testing.T) {
	if n, ok := toInt(float64(7)); !ok || n != 7 {
		t.Errorf("toInt(float64(7)) = (%d,%v), want (7,true)", n, ok)
	}
	if n, ok := toInt(3); !ok || n != 3 {
		t.Errorf("toInt(int(3)) = (%d,%v), want (3,true)", n, ok)
	}
	if _, ok := toInt("nope"); ok {
		t.Error("toInt(string) = ok, want not-ok")
	}
	if _, ok := toInt(nil); ok {
		t.Error("toInt(nil) = ok, want not-ok")
	}
}

// TestToSlice covers array vs non-array inputs.
func TestToSlice(t *testing.T) {
	if got := toSlice([]interface{}{1, 2}); len(got) != 2 {
		t.Errorf("toSlice(array) len = %d, want 2", len(got))
	}
	if got := toSlice(map[string]interface{}{}); got != nil {
		t.Errorf("toSlice(map) = %v, want nil", got)
	}
	if got := toSlice(nil); got != nil {
		t.Errorf("toSlice(nil) = %v, want nil", got)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
