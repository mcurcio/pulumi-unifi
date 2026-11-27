package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/uos"
)

// uosExtract implements `pulumi-gen-unifi uos-extract`: pinned installer ->
// canonical build/uos/model.json. It checks the pin ties and fails loudly on
// any mismatch (design §4).
func uosExtract(args []string) error {
	fs := flag.NewFlagSet("uos-extract", flag.ContinueOnError)
	installer := fs.String("installer", "", "path to the pinned UniFi OS Server installer (uos/fetch.sh prints it)")
	pinPath := fs.String("pin", filepath.Join("uos", "pin.env"), "the installer pin")
	specPin := fs.String("spec-pin", filepath.Join("openapi", "pin.env"), "the official spec pin to tie to (\"\" skips the tie)")
	out := fs.String("out", filepath.Join("build", "uos", "model.json"), "where to write the canonical model")
	imageOut := fs.String("image-out", "", "also write the installer's image.tar here (for the e2e controller)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *installer == "" {
		return errors.New("-installer is required")
	}
	pin, err := uos.ReadPin(*pinPath)
	if err != nil {
		return err
	}
	ex, err := uos.ExtractInstaller(*installer, uos.ExtractOptions{WantSHA256: pin.SHA256, ImageOut: *imageOut})
	if err != nil {
		return err
	}
	if got := ex.NetworkVersion(); got != pin.NetworkVersion {
		return fmt.Errorf("installer carries Network %q, but %s pins UOS_NETWORK_VERSION=%q", got, *pinPath, pin.NetworkVersion)
	}
	if *specPin != "" {
		b, err := os.ReadFile(*specPin)
		if err != nil {
			return err
		}
		sp := uos.ParseEnv(b)
		if sp["SPEC_VERSION"] != pin.NetworkVersion {
			return fmt.Errorf("pin tie: UOS_NETWORK_VERSION=%q but SPEC_VERSION=%q", pin.NetworkVersion, sp["SPEC_VERSION"])
		}
		if got := ex.IntegrationSHA256(); got != sp["SPEC_SHA256"] {
			return fmt.Errorf("pin tie: the installer's integration.json has SHA-256 %q, SPEC_SHA256 is %q", got, sp["SPEC_SHA256"])
		}
	}
	m, err := uos.BuildModel(ex, pin.Version)
	if err != nil {
		return err
	}
	b, err := kind.Canonical(m)
	if err != nil {
		return err
	}
	if err := writeAtomic(*out, b); err != nil {
		return err
	}
	d, _ := kind.Digest(m)
	v2, classic := 0, 0
	for _, k := range m.Kinds {
		if k.API == kind.APIV2 {
			v2++
		} else {
			classic++
		}
	}
	fmt.Fprintf(os.Stderr, "uos-extract: UniFi OS Server %s (Network %s): %d v2 + %d classic kinds -> %s (sha256 %s)\n",
		pin.Version, m.Source.NetworkVersion, v2, classic, *out, d)
	return nil
}

func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
