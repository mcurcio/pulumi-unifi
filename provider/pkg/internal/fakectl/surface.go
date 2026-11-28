package fakectl

import (
	"encoding/json"
	"net/http"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/engine"
)

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }

// Thing and Group are synthetic kinds (no Ubiquiti names) that exercise every
// binding path: Thing has an item GET, an `_id`, nested objects with defaults
// and secrets; Group answers 405 on the item GET (list + filter), uses `id`,
// lists at another path and creates with a list body.
//
// Slice 2 adds (private-binding.md §S2.7, §S2.8): Rule6, a v2 variant kind
// (the G-Q4 variant exception: only rules with kind=V6, the official API
// serves V4 and L2) with a create default and output-only fields; Crew, a
// classic collection; and Tick, a classic settings singleton.
const (
	ThingToken = engine.PrivateTokenPrefix + "Thing"
	GroupToken = engine.PrivateTokenPrefix + "Group"
	Rule6Token = engine.PrivateTokenPrefix + "Rule6"
	CrewToken  = engine.PrivateTokenPrefix + "Crew"
	TickToken  = engine.PrivateTokenPrefix + "SettingTick"
	// TickID is the id of the one Tick document Start seeds.
	TickID = "0000000000000000000000ff"
)

// Surface is the synthetic private surface the tests run on.
func Surface() *engine.Surface {
	return &engine.Surface{
		Source: engine.SurfaceSource{NetworkVersion: "0.0.0-test"},
		Kinds: []*engine.Kind{
			{
				Token: ThingToken, Kind: "things", API: "v2",
				Collection: "things", ListPath: "things", ItemPath: "things/{id}", CreatePath: "things",
				ItemGet: true, ListGet: true, IDField: "_id", Envelope: "bare",
				NaturalKey: []string{"name"},
				Fields: map[string]*engine.Field{
					"name":       {Wire: "name", Property: "name", Type: "string", Required: true, Constraints: &engine.Constraints{NonEmpty: true, MaxLen: i64(16)}},
					"enabled":    {Wire: "enabled", Property: "enabled", Type: "bool", Default: json.RawMessage(`true`)},
					"rate_kbps":  {Wire: "rate_kbps", Property: "rateKbps", Type: "int", Constraints: &engine.Constraints{Min: f64(0), Max: f64(1000)}},
					"mode":       {Wire: "mode", Property: "mode", Type: "enum", Enum: []string{"FAST", "SLOW"}, Default: json.RawMessage(`"FAST"`)},
					"note":       {Wire: "note", Property: "note", Type: "string", Nullable: true},
					"code":       {Wire: "code", Property: "code", Type: "string", Pattern: "[A-Z]{3}", CreateOnly: true},
					"tags":       {Wire: "tags", Property: "tags", Type: "array", Items: &engine.Field{Type: "string"}, Default: json.RawMessage(`[]`)},
					"psk":        {Wire: "psk", Property: "psk", Type: "string", Secret: true, NotReturned: true},
					"created_at": {Wire: "created_at", Property: "createdAt", Type: "string", ReadOnly: true},
					"window": {Wire: "window", Property: "window", Type: "object", Fields: map[string]*engine.Field{
						"mode":  {Wire: "mode", Property: "mode", Type: "enum", Enum: []string{"ALWAYS", "CUSTOM"}, Required: true},
						"start": {Wire: "start", Property: "start", Type: "string", Nullable: true},
						"end":   {Wire: "end", Property: "end", Type: "string"},
					}},
				},
			},
			{
				Token: GroupToken, Kind: "groups", API: "v2",
				Collection: "groups", ListPath: "groups-all", ItemPath: "groups/{id}", CreatePath: "groups",
				ItemGet: false, ListGet: true, CreateList: true, IDField: "id", Envelope: "bare",
				NaturalKey: []string{"name"},
				Fields: map[string]*engine.Field{
					"name":    {Wire: "name", Property: "name", Type: "string", Required: true},
					"members": {Wire: "members", Property: "members", Type: "array", Items: &engine.Field{Type: "string"}, Constraints: &engine.Constraints{MinItems: i64(1), MaxItems: i64(3)}},
					"kind":    {Wire: "kind", Property: "kind", Type: "enum", Enum: []string{"A", "B"}, Required: true},
				},
			},
			{
				Token: Rule6Token, Kind: "rules", API: "v2",
				Collection: "rules", ListPath: "rules", ItemPath: "rules/{id}", CreatePath: "rules",
				ItemGet: false, ListGet: true, IDField: "_id", Envelope: "bare",
				NaturalKey:     []string{"name"},
				Variant:        &engine.Variant{Field: "kind", Value: "V6"},
				CreateDefaults: map[string]json.RawMessage{"slot": json.RawMessage(`0`)},
				OfficialTokens: []string{"unifi:sites/v1:RuleV4", "unifi:sites/v1:RuleL2"},
				Fields: map[string]*engine.Field{
					"name":   {Wire: "name", Property: "name", Type: "string", Required: true},
					"action": {Wire: "action", Property: "action", Type: "enum", Enum: []string{"ALLOW", "BLOCK"}, Required: true},
					"slot":   {Wire: "slot", Property: "slot", Type: "int", OutputOnly: true},
					"ext_id": {Wire: "ext_id", Property: "ruleId", Type: "uuid", OutputOnly: true},
				},
			},
			{
				Token: CrewToken, Kind: "Crew", API: "classic",
				Collection: "rest/crew", ListPath: "rest/crew", ItemPath: "rest/crew/{id}", CreatePath: "rest/crew",
				ItemGet: true, ListGet: true, IDField: "_id", Envelope: "meta-data",
				NaturalKey: []string{"name"},
				Fields: map[string]*engine.Field{
					"name":     {Wire: "name", Property: "name", Type: "string", Required: true},
					"rate_max": {Wire: "rate_max", Property: "rateMax", Type: "int", Default: json.RawMessage(`-1`)},
				},
			},
			{
				Token: TickToken, Kind: "Setting.tick", API: "classic",
				Collection: "rest/setting/tick", ListPath: "rest/setting/tick", ItemPath: "rest/setting/tick/{id}",
				CreatePath: "rest/setting/tick", ItemGet: false, ListGet: true, IDField: "_id", Envelope: "meta-data",
				Shape: engine.ShapeSingleton, SingletonKey: "tick",
				Fields: map[string]*engine.Field{
					"mode":   {Wire: "mode", Property: "mode", Type: "enum", Enum: []string{"auto", "manual"}, Default: json.RawMessage(`"auto"`)},
					"server": {Wire: "server", Property: "server", Type: "string"},
				},
			},
		},
	}
}

// Server starts a fake controller for Surface's kinds. Things get a
// controller-side `created_at` and an unmodelled `extra` field on create.
//
// Rules: a create with kind=V6 that lacks `slot` answers 400, and the server
// mints `ext_id`. Crew refuses a rate_max of 0 or 1 like the live usergroup
// pattern. Tick holds one seeded document (TickID) and refuses creates.
func Start(site string) *Server {
	s := New(site,
		&Coll{Path: "things", IDField: "_id", ItemGet: true, OnCreate: func(o map[string]any) {
			o["created_at"] = "2026-01-01T00:00:00Z"
			o["extra"] = "controller-only"
			delete(o, "psk") // never returned
		}},
		&Coll{Path: "groups", ListPath: "groups-all", IDField: "id", CreateList: true},
		&Coll{Path: "rules", IDField: "_id", OnCreate: func(o map[string]any) {
			o["ext_id"] = "00000000-0000-4000-8000-0000000000aa"
		}, Refuse: func(o map[string]any) (int, map[string]any) {
			if _, ok := o["slot"]; !ok {
				return http.StatusBadRequest, map[string]any{"error": "slot is required"}
			}
			return 0, nil
		}},
		&Coll{Path: "rest/crew", IDField: "_id", ItemGet: true, Classic: true, Validate: func(o map[string]any) (string, string) {
			if v, ok := o["rate_max"].(float64); ok && (v == 0 || v == 1) {
				return "rate_max", "-1|[2-9]|[1-9][0-9]{1,4}"
			}
			return "", ""
		}},
		&Coll{Path: "rest/setting/tick", IDField: "_id", Classic: true, NoCreate: true, Validate: func(o map[string]any) (string, string) {
			if m, ok := o["mode"].(string); ok && m != "auto" && m != "manual" {
				return "mode", "auto|manual"
			}
			return "", ""
		}},
	)
	s.Put("rest/setting/tick", map[string]any{"_id": TickID, "key": "tick", "site_id": "site-1", "mode": "auto", "server": "time.example.test"})
	return s
}

// SurfaceJSON is Surface as surface.json bytes.
func SurfaceJSON() []byte {
	b, _ := json.Marshal(Surface())
	return b
}
