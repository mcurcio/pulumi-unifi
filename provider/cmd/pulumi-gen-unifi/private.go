package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private"
	privategen "github.com/mcurcio/pulumi-unifi/provider/pkg/private/gen"
)

// privateGen implements `pulumi-gen-unifi private`: model + overrides +
// golden schema -> schema.merged.json + private.json (design §5). Without
// -model the merged schema is the golden byte for byte and private.json holds
// no kinds (PRIVATE=0).
func privateGen(args []string) error {
	fs := flag.NewFlagSet("private", flag.ContinueOnError)
	modelPath := fs.String("model", "", "the extracted model (build/uos/model.json); empty: no private kinds")
	overridesPath := fs.String("overrides", "", "kinds.yaml (default: the embedded provider/pkg/private/kinds.yaml)")
	goldenPath := fs.String("golden", filepath.Join("provider", "cmd", "pulumi-resource-unifi", "schema.json"), "the official golden schema")
	outDir := fs.String("out", filepath.Join("provider", "cmd", "pulumi-resource-unifi"), "where to write schema.merged.json and private.json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	golden, err := os.ReadFile(*goldenPath)
	if err != nil {
		return err
	}
	var o *private.Overrides
	if *overridesPath == "" {
		o, err = private.Load()
	} else {
		var b []byte
		if b, err = os.ReadFile(*overridesPath); err == nil {
			o, err = private.Parse(b)
		}
	}
	if err != nil {
		return err
	}
	var m *kind.Model
	if *modelPath != "" {
		b, err := os.ReadFile(*modelPath)
		if err != nil {
			return err
		}
		if m, err = kind.Parse(b); err != nil {
			return err
		}
	}
	out, err := privategen.Generate(m, o, golden)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(*outDir, "schema.merged.json"), out.Schema); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(*outDir, "private.json"), out.Surface); err != nil {
		return err
	}
	if m == nil {
		fmt.Fprintln(os.Stderr, "private: no model (PRIVATE=0): schema.merged.json = schema.json, no private kinds")
	} else {
		fmt.Fprintf(os.Stderr, "private: Network %s: %d private resources %v\n", m.Source.NetworkVersion, len(out.Tokens), out.Tokens)
	}
	return nil
}
