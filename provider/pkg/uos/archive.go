package uos

// Unpacking the UniFi OS Server installer WITHOUT executing it. The installer
// is an ELF binary with a ZIP archive appended. The ZIP holds image.tar (an OCI
// image in docker-archive layout: manifest.json, a config blob, gzip layers).
// The Network app layer is found by content (the layer that holds
// usr/lib/unifi/lib/ace.jar, a Spring Boot fat jar), and the model sources sit
// in BOOT-INF/lib/internal-dependencies.jar nested inside it.
//
// Everything is streamed and held in memory; nothing raw is written to disk
// except, on request, a copy of image.tar for the e2e controller.

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Paths inside the image and the jars.
const (
	AceJarPath          = "usr/lib/unifi/lib/ace.jar"
	IntegrationSpecPath = "usr/lib/unifi/webapps/ROOT/api-docs/integration.json"
	InternalDepsPath    = "BOOT-INF/lib/internal-dependencies.jar"
	FieldsDir           = "api/fields/"
	SensitiveMetaPath   = "sensitive_metadata.json"
)

// Extracted is what the extractor pulls out of one installer.
type Extracted struct {
	InstallerSHA256 string
	// ImageArgs are the image build ARGs from the config history
	// (UNIFI_VERSION=10.4.57-34628-1, ...).
	ImageArgs map[string]string
	// NetworkLayer is the image layer (manifest path) that holds ace.jar.
	NetworkLayer    string
	IntegrationJSON []byte
	// Deps is BOOT-INF/lib/internal-dependencies.jar, opened in memory.
	Deps *zip.Reader
}

// NetworkVersion is the Network app version from the UNIFI_VERSION build arg,
// without its build suffix ("10.4.57-34628-1" -> "10.4.57").
func (e *Extracted) NetworkVersion() string {
	v := e.ImageArgs["UNIFI_VERSION"]
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	return v
}

// IntegrationSHA256 is the hex SHA-256 of the image's integration.json, or
// "" when the Network layer has none.
func (e *Extracted) IntegrationSHA256() string {
	if e.IntegrationJSON == nil {
		return ""
	}
	sum := sha256.Sum256(e.IntegrationJSON)
	return hex.EncodeToString(sum[:])
}

// ExtractOptions tune ExtractInstaller.
type ExtractOptions struct {
	// WantSHA256, when set, must equal the installer's SHA-256.
	WantSHA256 string
	// ImageOut, when set, receives a copy of image.tar (written via a temp
	// file and renamed, so a partial copy never stands).
	ImageOut string
}

// ExtractInstaller hashes the installer, opens its appended ZIP, and streams
// image.tar to find the Network layer's model sources.
func ExtractInstaller(installer string, opt ExtractOptions) (*Extracted, error) {
	f, err := os.Open(installer)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, fmt.Errorf("hashing installer: %w", err)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if opt.WantSHA256 != "" && !strings.EqualFold(sum, opt.WantSHA256) {
		return nil, fmt.Errorf("installer sha256 mismatch: want %s, got %s", opt.WantSHA256, sum)
	}
	zr, err := OpenAppendedZip(f, st.Size())
	if err != nil {
		return nil, err
	}
	var img *zip.File
	for _, zf := range zr.File {
		if zf.Name == "image.tar" {
			img = zf
		}
	}
	if img == nil {
		return nil, errors.New("installer ZIP holds no image.tar")
	}
	rc, err := img.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var src io.Reader = rc
	var tmp *os.File
	if opt.ImageOut != "" {
		if err := os.MkdirAll(filepath.Dir(opt.ImageOut), 0o755); err != nil {
			return nil, err
		}
		tmp, err = os.CreateTemp(filepath.Dir(opt.ImageOut), ".image.tar.*")
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmp.Name()) // no-op after the rename
		src = io.TeeReader(rc, tmp)
	}
	ex, err := scanImage(src)
	if err != nil {
		if tmp != nil {
			tmp.Close()
		}
		return nil, err
	}
	if tmp != nil {
		// Drain whatever the tar reader did not consume, then publish the copy.
		if _, err := io.Copy(io.Discard, src); err != nil {
			tmp.Close()
			return nil, err
		}
		if err := tmp.Close(); err != nil {
			return nil, err
		}
		if err := os.Rename(tmp.Name(), opt.ImageOut); err != nil {
			return nil, err
		}
	}
	ex.InstallerSHA256 = sum
	return ex, nil
}

// OpenAppendedZip opens a ZIP archive appended to other data (here, an ELF)
// by locating the end-of-central-directory record and reading through a
// section reader that starts where the archive starts.
func OpenAppendedZip(r io.ReaderAt, size int64) (*zip.Reader, error) {
	const eocdLen = 22
	tail := int64(eocdLen + 65535)
	if tail > size {
		tail = size
	}
	buf := make([]byte, tail)
	if _, err := r.ReadAt(buf, size-tail); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	for i := len(buf) - eocdLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(buf[i:]) != 0x06054b50 {
			continue
		}
		cdSize := int64(binary.LittleEndian.Uint32(buf[i+12:]))
		cdOff := int64(binary.LittleEndian.Uint32(buf[i+16:]))
		commentLen := int(binary.LittleEndian.Uint16(buf[i+20:]))
		if i+eocdLen+commentLen != len(buf) {
			continue // a signature inside the comment or data, not the record
		}
		if cdOff == 0xFFFFFFFF || cdSize == 0xFFFFFFFF {
			// ZIP64: archive/zip resolves the zip64 records and the prefix itself.
			return zip.NewReader(r, size)
		}
		eocdPos := size - tail + int64(i)
		start := eocdPos - cdSize - cdOff
		if start < 0 {
			return nil, errors.New("appended ZIP: central directory lies before the file start")
		}
		return zip.NewReader(io.NewSectionReader(r, start, size-start), size-start)
	}
	return nil, errors.New("no ZIP end-of-central-directory record found")
}

type layerHit struct {
	ace, spec []byte
}

// scanImage reads a docker-archive image tar as a stream. Every layer is
// scanned for the Network app files; the manifest and small blobs (the
// config) are kept so the choice can be made once the stream ends.
func scanImage(r io.Reader) (*Extracted, error) {
	tr := tar.NewReader(r)
	var manifest []byte
	small := map[string][]byte{}
	hits := map[string]*layerHit{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading image.tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(hdr.Name)
		switch {
		case name == "manifest.json":
			if manifest, err = io.ReadAll(tr); err != nil {
				return nil, err
			}
		case hdr.Size <= 1<<20:
			// Small blobs: the config, or a small layer. Keep the bytes and
			// scan them as a layer too.
			b, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			small[name] = b
			if hit, err := scanLayer(bytes.NewReader(b)); err == nil && hit != nil {
				hits[name] = hit
			}
		default:
			hit, err := scanLayer(tr)
			if err != nil {
				return nil, fmt.Errorf("layer %s: %w", name, err)
			}
			if hit != nil {
				hits[name] = hit
			}
		}
	}
	if manifest == nil {
		return nil, errors.New("image.tar has no manifest.json")
	}
	var mf []struct {
		Config string
		Layers []string
	}
	if err := json.Unmarshal(manifest, &mf); err != nil || len(mf) == 0 {
		return nil, fmt.Errorf("image.tar manifest.json: unreadable or empty (%v)", err)
	}
	ex := &Extracted{ImageArgs: map[string]string{}}
	// Later layers win (overlay order): take the last layer holding ace.jar.
	for _, l := range mf[0].Layers {
		if h := hits[path.Clean(l)]; h != nil && h.ace != nil {
			ex.NetworkLayer = l
		}
	}
	if ex.NetworkLayer == "" {
		return nil, fmt.Errorf("no image layer holds %s", AceJarPath)
	}
	hit := hits[path.Clean(ex.NetworkLayer)]
	ex.IntegrationJSON = hit.spec
	if cfg := small[path.Clean(mf[0].Config)]; cfg != nil {
		var c struct {
			History []struct {
				CreatedBy string `json:"created_by"`
			} `json:"history"`
		}
		if err := json.Unmarshal(cfg, &c); err != nil {
			return nil, fmt.Errorf("image config: %w", err)
		}
		for _, h := range c.History {
			if a, ok := strings.CutPrefix(h.CreatedBy, "ARG "); ok {
				if k, v, ok := strings.Cut(a, "="); ok {
					ex.ImageArgs[k] = v
				}
			}
		}
	}
	ace, err := zip.NewReader(bytes.NewReader(hit.ace), int64(len(hit.ace)))
	if err != nil {
		return nil, fmt.Errorf("opening ace.jar: %w", err)
	}
	deps, err := readZipEntry(ace, InternalDepsPath)
	if err != nil {
		return nil, fmt.Errorf("ace.jar: %w", err)
	}
	if ex.Deps, err = zip.NewReader(bytes.NewReader(deps), int64(len(deps))); err != nil {
		return nil, fmt.Errorf("opening %s: %w", InternalDepsPath, err)
	}
	return ex, nil
}

// scanLayer streams one layer (gzip or plain tar) and returns the Network
// app files it holds, or nil.
func scanLayer(r io.Reader) (*layerHit, error) {
	br := bufio.NewReaderSize(r, 1<<16)
	magic, _ := br.Peek(2)
	var lr io.Reader = br
	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		lr = gz
	}
	tr := tar.NewReader(lr)
	var hit *layerHit
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return hit, nil
		}
		if err != nil {
			if hit == nil {
				return nil, nil // not a tar layer (some other blob)
			}
			return nil, err
		}
		name := strings.TrimPrefix(path.Clean("/"+hdr.Name), "/")
		if hdr.Typeflag != tar.TypeReg || (name != AceJarPath && name != IntegrationSpecPath) {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		if hit == nil {
			hit = &layerHit{}
		}
		if name == AceJarPath {
			hit.ace = b
		} else {
			hit.spec = b
		}
	}
}

func readZipEntry(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("no entry %s", name)
}

// FieldFiles returns api/fields/*.json from the dependencies jar, by base
// name without ".json", in sorted order.
func FieldFiles(deps *zip.Reader) (names []string, files map[string][]byte, err error) {
	files = map[string][]byte{}
	for _, f := range deps.File {
		if !strings.HasPrefix(f.Name, FieldsDir) || !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		base := strings.TrimSuffix(strings.TrimPrefix(f.Name, FieldsDir), ".json")
		if strings.Contains(base, "/") {
			continue
		}
		b, err := readZipEntry(deps, f.Name)
		if err != nil {
			return nil, nil, err
		}
		files[base] = b
		names = append(names, base)
	}
	sort.Strings(names)
	return names, files, nil
}
