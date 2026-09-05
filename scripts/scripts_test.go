package scripts_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGit is a helper for the fixtures: every git invocation runs in the temp
// repo with a throwaway identity so the host's git config cannot interfere.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(
		os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return strings.TrimSpace(string(out))
}

// fixtureFiles writes the minimal file set the guard script reads.
func fixtureFiles(t *testing.T, dir, goMod, goSum, flake string) {
	t.Helper()

	for name, content := range map[string]string{
		"go.mod":    goMod,
		"go.sum":    goSum,
		"flake.nix": flake,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

const (
	baseGoMod = "module example.com/m\n\ngo 1.26.7\n"
	baseGoSum = "example.com/dep v1.0.0 h1:abc=\nexample.com/dep v1.0.0/go.mod h1:def=\n"
	baseFlake = "vendorHash = \"sha256-base\";\n"
)

// setupRepo creates a temp git repo with a base commit and returns its path.
func setupRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	fixtureFiles(t, dir, baseGoMod, baseGoSum, baseFlake)
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "base")

	return dir
}

// scriptPath is the absolute path of the guard script: the test binary runs
// with the scripts package directory as its working directory.
func scriptPath(t *testing.T) string {
	t.Helper()

	abs, err := filepath.Abs("check-vendor-hash.sh")
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}

	return abs
}

func TestCheckVendorHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string

		// mutations applied to the working tree after the base commit;
		// nil entries are skipped.
		goMod    *string
		goSum    *string
		flake    *string
		wantCode int
		wantOut  []string
	}{
		{
			name:     "no changes passes",
			wantCode: 0,
			wantOut:  []string{"OK"},
		},
		{
			// The go directive is excluded from the snapshot, so a
			// compiler-pin bump alone is not drift.
			name:     "toolchain-only go.mod bump passes",
			goMod:    new("module example.com/m\n\ngo 1.26.9\n"),
			wantCode: 0,
			wantOut:  []string{"OK"},
		},
		{
			name:     "dependency change without vendorHash fails",
			goSum:    new("example.com/dep v1.1.0 h1:abc=\nexample.com/dep v1.1.0/go.mod h1:def=\n"),
			wantCode: 1,
			wantOut:  []string{"DRIFT DETECTED"},
		},
		{
			name:     "vendorHash-only change warns but passes",
			flake:    new("vendorHash = \"sha256-rotated\";\n"),
			wantCode: 0,
			wantOut:  []string{"OK", "note — vendorHash changed"},
		},
		{
			name:     "dependency and vendorHash changed together passes",
			goSum:    new("example.com/dep v1.1.0 h1:abc=\nexample.com/dep v1.1.0/go.mod h1:def=\n"),
			flake:    new("vendorHash = \"sha256-rotated\";\n"),
			wantCode: 0,
			wantOut:  []string{"OK"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := setupRepo(t)

			if tt.goMod != nil {
				fixtureFiles(t, dir, *tt.goMod, baseGoSum, baseFlake)
			}
			if tt.goSum != nil {
				fixtureFiles(t, dir, baseGoMod, *tt.goSum, baseFlake)
			}
			if tt.flake != nil {
				fixtureFiles(t, dir, baseGoMod, baseGoSum, *tt.flake)
			}

			cmd := exec.CommandContext(t.Context(), scriptPath(t), "HEAD")
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()

			code := 0
			if exitErr := (*exec.ExitError)(nil); err != nil && errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			} else if err != nil {
				t.Fatalf("run script: %v\n%s", err, out)
			}

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\n%s", code, tt.wantCode, out)
			}

			for _, want := range tt.wantOut {
				if !strings.Contains(string(out), want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestCheckVendorHashBadBaseRev(t *testing.T) {
	t.Parallel()

	dir := setupRepo(t)

	cmd := exec.CommandContext(t.Context(), scriptPath(t), "does-not-exist")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure, got success\n%s", out)
	}

	if !strings.Contains(string(out), "not found") {
		t.Errorf("output missing \"not found\":\n%s", out)
	}
}
