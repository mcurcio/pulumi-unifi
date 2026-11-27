package uos

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ClassSource provides class-file bytes by binary name (dotted).
type ClassSource interface {
	// Names lists every class's binary name, sorted.
	Names() []string
	// Bytes returns the class file, or an error wrapping fs.ErrNotExist.
	Bytes(name string) ([]byte, error)
}

// ZipClasses serves classes from a jar held in memory.
type ZipClasses struct {
	files map[string]*zip.File
	names []string
}

// NewZipClasses indexes every .class entry of a jar.
func NewZipClasses(zr *zip.Reader) *ZipClasses {
	z := &ZipClasses{files: map[string]*zip.File{}}
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".class") || f.FileInfo().IsDir() {
			continue
		}
		n := strings.ReplaceAll(strings.TrimSuffix(f.Name, ".class"), "/", ".")
		if strings.HasPrefix(n, "META-INF.") || n == "module-info" {
			continue
		}
		if _, dup := z.files[n]; !dup {
			z.names = append(z.names, n)
		}
		z.files[n] = f
	}
	sort.Strings(z.names)
	return z
}

func (z *ZipClasses) Names() []string { return z.names }

func (z *ZipClasses) Bytes(name string) ([]byte, error) {
	f, ok := z.files[name]
	if !ok {
		return nil, fmt.Errorf("class %s: %w", name, fs.ErrNotExist)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// DirClasses serves classes from a directory tree (tests, and the research
// prototype's classes/ output for the parity oracle).
type DirClasses struct {
	root  string
	names []string
}

// NewDirClasses indexes every .class under root.
func NewDirClasses(root string) (*DirClasses, error) {
	d := &DirClasses{root: root}
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() || !strings.HasSuffix(p, ".class") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		d.names = append(d.names, strings.ReplaceAll(filepath.ToSlash(strings.TrimSuffix(rel, ".class")), "/", "."))
		return nil
	})
	sort.Strings(d.names)
	return d, err
}

func (d *DirClasses) Names() []string { return d.names }

func (d *DirClasses) Bytes(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(d.root, filepath.FromSlash(strings.ReplaceAll(name, ".", "/"))+".class"))
}

// Index parses classes on demand and caches them.
type Index struct {
	src   ClassSource
	cache map[string]*ClassFile
}

// NewIndex wraps a class source.
func NewIndex(src ClassSource) *Index {
	return &Index{src: src, cache: map[string]*ClassFile{}}
}

// Class returns the parsed class, or nil when it is absent or unreadable
// (library classes such as java.* and Spring are not in the jar).
func (x *Index) Class(name string) *ClassFile {
	if c, ok := x.cache[name]; ok {
		return c
	}
	var c *ClassFile
	if b, err := x.src.Bytes(name); err == nil {
		c, _ = ParseClass(b)
	}
	x.cache[name] = c
	return c
}

// Scan returns the names of classes whose raw bytes contain any of needles,
// in sorted order. It reads every class once without parsing it.
func (x *Index) Scan(needles ...[]byte) ([]string, error) {
	var out []string
	for _, n := range x.src.Names() {
		b, err := x.src.Bytes(n)
		if err != nil {
			return nil, err
		}
		for _, nd := range needles {
			if bytes.Contains(b, nd) {
				out = append(out, n)
				break
			}
		}
	}
	return out, nil
}
