package uos

// A synthetic UniFi OS Server installer, built in memory for Tier-1 tests: an
// ELF-looking prefix, then a ZIP holding image.tar, whose layers hold an
// ace.jar that nests internal-dependencies.jar with the self-written fixture
// classes, field files and a redaction list. No Ubiquiti bytes anywhere.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const synthIntegration = `{"openapi":"3.1.0","info":{"title":"synthetic","version":"9.9.9"},"paths":{}}`

var synthFields = map[string]string{
	"api/fields/Thing.json": `{
  "name": ".{1,32}",
  "enabled": "true|false",
  "mode": "auto|manual|off",
  "x_passphrase": "",
  "port": "[0-9]{1,5}",
  "nested": {"inner": "a|b"},
  "list": ["^[a-f0-9]{24}$"]
}`,
	"api/fields/Setting.json": `{"mgmt": {"led_enabled": "true|false", "x_ssh_password": ".*", "hostname": ""}, "ntp": {"ntp_server_1": ""}}`,
	SensitiveMetaPath:         `{"sensitive_db_fields_by_collection": {"thing": ["name"], "setting": ["hostname"]}}`,
}

func zipBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(files[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tarBytes(t *testing.T, gz bool, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	var out io.Writer = &buf
	var gzw *gzip.Writer
	if gz {
		gzw = gzip.NewWriter(&buf)
		out = gzw
	}
	tw := tar.NewWriter(out)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(files[n])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(files[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if gzw != nil {
		if err := gzw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

// fixtureClassFiles returns the committed fixture classes as jar entries.
func fixtureClassFiles(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	root := filepath.Join("testdata", "classes")
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// writeSynthInstaller writes a synthetic installer and returns its path and
// SHA-256. The Network layer is the second of two layers that hold ace.jar
// (later layers win); a padding blob pushes one layer past the small-blob size.
func writeSynthInstaller(t *testing.T, dir string) (string, string) {
	t.Helper()
	deps := map[string][]byte{}
	for n, b := range fixtureClassFiles(t) {
		deps[n] = b
	}
	for n, s := range synthFields {
		deps[n] = []byte(s)
	}
	depsJar := zipBytes(t, deps)
	ace := zipBytes(t, map[string][]byte{InternalDepsPath: depsJar, "BOOT-INF/classes/app.properties": []byte("x=1\n")})
	oldAce := zipBytes(t, map[string][]byte{"BOOT-INF/lib/other.jar": []byte("not the one")})
	base := tarBytes(t, true, map[string][]byte{"etc/os-release": []byte("ID=synthetic\n"), AceJarPath: oldAce})
	big := make([]byte, 2<<20) // > 1 MiB: scanned as a big layer
	network := tarBytes(t, true, map[string][]byte{
		"./" + AceJarPath:         ace,
		IntegrationSpecPath:       []byte(synthIntegration),
		"usr/lib/unifi/padding":   big,
		"usr/lib/unifi/lib/a.jar": []byte("unrelated"),
	})
	cfg, _ := json.Marshal(map[string]any{"history": []map[string]string{
		{"created_by": "ARG UNIFI_VERSION=9.9.9-12345-1"},
		{"created_by": "ARG UOS_VERSION=7.7.7"},
		{"created_by": "RUN /bin/true"},
	}})
	manifest, _ := json.Marshal([]map[string]any{{
		"Config": "blobs/sha256/cfg",
		"Layers": []string{"blobs/sha256/base", "blobs/sha256/net"},
	}})
	image := tarBytes(t, false, map[string][]byte{
		"blobs/sha256/base": base, "blobs/sha256/net": network, "blobs/sha256/cfg": cfg, "manifest.json": manifest,
	})
	payload := zipBytes(t, map[string][]byte{"image.tar": image, "uosserver": []byte("#!/bin/false\n")})
	inst := append([]byte("\x7fELF synthetic prefix "+strings.Repeat("P", 4096)), payload...)
	p := filepath.Join(dir, "installer")
	if err := os.WriteFile(p, inst, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(inst)
	return p, hex.EncodeToString(sum[:])
}
