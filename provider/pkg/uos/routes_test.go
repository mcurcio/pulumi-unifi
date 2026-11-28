package uos

import (
	"strings"
	"testing"
)

func fixtureExtractor(t *testing.T) *Extractor {
	t.Helper()
	dc, err := NewDirClasses("testdata/classes")
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewExtractor(NewIndex(dc))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestRoutesFromFixtures(t *testing.T) {
	var got []string
	for _, r := range fixtureExtractor(t).Routes() {
		got = append(got, r.Verb+" "+r.Path+" "+strings.TrimPrefix(r.Class, "com.ubnt.fixture.")+"."+r.Method)
	}
	want := []string{
		// path= mapping; prefix via the meta-annotation @SiteApi
		"DELETE /api/site/{siteName}/gadgets/{id} GadgetController.delete",
		"DELETE /api/site/{siteName}/things/{thingId} ThingController.delete",
		"DELETE /api/site/{siteName}/widgets/{id} WidgetController.delete",
		"GET /api/site/{siteName}/gadgets GadgetController.list",
		"GET /api/site/{siteName}/members/{id} MemberController.get",
		"GET /api/site/{siteName}/things ThingController.list",
		"GET /api/site/{siteName}/things/{thingId}/status ThingController.status",
		// a bare @GetMapping on a class-level @RequestMapping
		"GET /api/site/{siteName}/widgets WidgetController.list",
		// a handler with a throws clause (the javap-text prototype skipped these)
		"GET /api/site/{siteName}/widgets/{id} WidgetController.get",
		"POST /api/site/{siteName}/gadgets GadgetController.create",
		"POST /api/site/{siteName}/members MemberController.create",
		"POST /api/site/{siteName}/things/create ThingController.create",
		"POST /api/site/{siteName}/widgets WidgetController.create",
		// @RequestMapping(method = {POST, PUT})
		"POST /api/site/{siteName}/widgets/{id}/ping WidgetController.ping",
		"PUT /api/site/{siteName}/gadgets/{id} GadgetController.update",
		"PUT /api/site/{siteName}/members/{id} MemberController.update",
		"PUT /api/site/{siteName}/widgets/{id} WidgetController.update",
		"PUT /api/site/{siteName}/widgets/{id}/ping WidgetController.ping",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
