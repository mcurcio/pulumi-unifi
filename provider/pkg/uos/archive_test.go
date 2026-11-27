package uos

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractSyntheticInstaller(t *testing.T) {
	dir := t.TempDir()
	inst, sum := writeSynthInstaller(t, dir)
	imgOut := filepath.Join(dir, "cache", "image.tar")
	ex, err := ExtractInstaller(inst, ExtractOptions{WantSHA256: strings.ToUpper(sum), ImageOut: imgOut})
	if err != nil {
		t.Fatalf("ExtractInstaller: %v", err)
	}
	if ex.InstallerSHA256 != sum {
		t.Errorf("InstallerSHA256 = %s, want %s", ex.InstallerSHA256, sum)
	}
	if got := ex.NetworkVersion(); got != "9.9.9" {
		t.Errorf("NetworkVersion = %q, want 9.9.9", got)
	}
	if ex.ImageArgs["UOS_VERSION"] != "7.7.7" {
		t.Errorf("ImageArgs = %v", ex.ImageArgs)
	}
	if ex.NetworkLayer != "blobs/sha256/net" {
		t.Errorf("NetworkLayer = %q, want the later layer holding ace.jar", ex.NetworkLayer)
	}
	if string(ex.IntegrationJSON) != synthIntegration {
		t.Errorf("IntegrationJSON = %q", ex.IntegrationJSON)
	}
	if ex.IntegrationSHA256() == "" {
		t.Error("IntegrationSHA256 empty")
	}
	names, files, err := FieldFiles(ex.Deps)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "Setting,Thing" || len(files) != 2 {
		t.Errorf("FieldFiles = %v", names)
	}
	// The image copy is the exact image.tar from the installer ZIP.
	img, err := os.ReadFile(imgOut)
	if err != nil {
		t.Fatalf("image copy: %v", err)
	}
	if !bytes.HasPrefix(img[257:], []byte("ustar")) {
		t.Error("image copy is not a tar")
	}
	left, _ := filepath.Glob(filepath.Join(dir, "cache", ".image.tar.*"))
	if len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestExtractRejectsWrongChecksum(t *testing.T) {
	dir := t.TempDir()
	inst, _ := writeSynthInstaller(t, dir)
	_, err := ExtractInstaller(inst, ExtractOptions{WantSHA256: strings.Repeat("0", 64)})
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("want a checksum mismatch, got %v", err)
	}
}

func TestOpenAppendedZip(t *testing.T) {
	z := zipBytes(t, map[string][]byte{"a.txt": []byte("hello")})
	for _, prefix := range []string{"", "\x7fELF", strings.Repeat("x", 70000)} {
		b := append([]byte(prefix), z...)
		zr, err := OpenAppendedZip(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			t.Fatalf("prefix %d bytes: %v", len(prefix), err)
		}
		got, err := readZipEntry(zr, "a.txt")
		if err != nil || string(got) != "hello" {
			t.Fatalf("prefix %d bytes: read %q, %v", len(prefix), got, err)
		}
	}
	if _, err := OpenAppendedZip(bytes.NewReader([]byte("no zip here at all, just bytes")), 30); err == nil {
		t.Error("want an error for data with no ZIP")
	}
}

func TestExtractRejectsInstallerWithoutImage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "installer")
	if err := os.WriteFile(p, append([]byte("\x7fELF"), zipBytes(t, map[string][]byte{"x": []byte("y")})...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractInstaller(p, ExtractOptions{}); err == nil || !strings.Contains(err.Error(), "image.tar") {
		t.Fatalf("want a missing image.tar error, got %v", err)
	}
}
