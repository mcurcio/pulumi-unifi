package fakectl

import (
	"encoding/json"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/engine"
)

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }

// Thing and Group are synthetic kinds (no Ubiquiti names) that exercise every
// binding path: Thing has an item GET, an `_id`, nested objects with defaults
// and secrets; Group answers 405 on the item GET (list + filter), uses `id`,
// lists at another path and creates with a list body.
const (
	ThingToken = engine.TokenPrefix + "Thing"
	GroupToken = engine.TokenPrefix + "Group"
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
		},
	}
}

// Server starts a fake controller for Surface's kinds. Things get a
// controller-side `created_at` and an unmodelled `extra` field on create.
func Start(site string) *Server {
	return New(site,
		&Coll{Path: "things", IDField: "_id", ItemGet: true, OnCreate: func(o map[string]any) {
			o["created_at"] = "2026-01-01T00:00:00Z"
			o["extra"] = "controller-only"
			delete(o, "psk") // never returned
		}},
		&Coll{Path: "groups", ListPath: "groups-all", IDField: "id", CreateList: true},
	)
}

// SurfaceJSON is Surface as private.json bytes.
func SurfaceJSON() []byte {
	b, _ := json.Marshal(Surface())
	return b
}
