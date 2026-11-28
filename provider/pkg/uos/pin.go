package uos

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Pin is uos/pin.env: the pinned UniFi OS Server installer.
type Pin struct {
	Version        string // UOS_VERSION
	Platform       string // UOS_PLATFORM
	URL            string // UOS_URL
	Size           string // UOS_SIZE
	SHA256         string // UOS_SHA256
	NetworkVersion string // UOS_NETWORK_VERSION
}

// ParseEnv reads a shell-sourceable KEY=VALUE file (comments and blank lines
// ignored; values may be single- or double-quoted).
func ParseEnv(b []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}

// ReadPin reads and checks uos/pin.env.
func ReadPin(path string) (*Pin, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	e := ParseEnv(b)
	p := &Pin{
		Version: e["UOS_VERSION"], Platform: e["UOS_PLATFORM"], URL: e["UOS_URL"],
		Size: e["UOS_SIZE"], SHA256: e["UOS_SHA256"], NetworkVersion: e["UOS_NETWORK_VERSION"],
	}
	for k, v := range map[string]string{"UOS_VERSION": p.Version, "UOS_PLATFORM": p.Platform, "UOS_URL": p.URL,
		"UOS_SIZE": p.Size, "UOS_SHA256": p.SHA256, "UOS_NETWORK_VERSION": p.NetworkVersion} {
		if v == "" {
			return nil, fmt.Errorf("%s: %s is not set", path, k)
		}
	}
	return p, nil
}

// FindRepoFile walks up from the working directory to find a repo-relative
// file (tests run from the package directory).
func FindRepoFile(rel string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		c := filepath.Join(dir, rel)
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%s not found walking up from the working directory", rel)
		}
		dir = parent
	}
}
