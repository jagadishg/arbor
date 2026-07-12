package skill

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The repository skill is the canonical source. The release binary embeds a
// copy because go:embed cannot include files outside this package; keep that
// copy byte-for-byte identical so source users and installed users get the
// same instructions.
func TestEmbeddedSkillMatchesCanonicalSkill(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(current), "../.."))
	canonical := filepath.Join(repo, "skills", Name)
	embedded := filepath.Join(repo, "internal", "skill", "assets", Name)

	err := filepath.WalkDir(canonical, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(canonical, path)
		if err != nil {
			return err
		}
		want, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(embedded, rel))
		if err != nil {
			return err
		}
		if string(got) != string(want) {
			t.Errorf("embedded skill differs from canonical file %s", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
