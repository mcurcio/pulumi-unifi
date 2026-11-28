package uos

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// keyInArgv matches an X-API-Key header whose value is expanded from a shell
// variable on the command line (curl -H "X-API-KEY: $KEY"). Such a key sits
// in the process list (ps, /proc/<pid>/cmdline) for as long as the command
// runs. Feed the header on stdin instead (printf ... | curl -H @-).
var keyInArgv = regexp.MustCompile(`(?i)x-api-key:\s*\$`)

// TestNoKeyOnCommandLine fails if a tracked shell script or Makefile passes the
// API key in a command's arguments. The live harness reads the throwaway seed
// key at run time and keeps it in the environment or on stdin only
// (docs/design/private-binding.md §S2.10).
func TestNoKeyOnCommandLine(t *testing.T) {
	pin, err := FindRepoFile("uos/pin.env")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(pin))
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v (this gate needs git)", err)
	}
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		base := filepath.Base(f)
		if !strings.HasSuffix(f, ".sh") && base != "Makefile" && !strings.HasSuffix(f, ".mk") {
			continue
		}
		fh, err := os.Open(filepath.Join(root, f))
		if err != nil {
			continue // deleted in the worktree
		}
		sc := bufio.NewScanner(fh)
		for n := 1; sc.Scan(); n++ {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "#") {
				continue
			}
			if keyInArgv.MatchString(line) {
				t.Errorf("%s:%d passes the API key on a command line; feed the header on stdin (curl -H @-)", f, n)
			}
		}
		fh.Close()
	}
}
