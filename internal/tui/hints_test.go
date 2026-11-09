package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// No hint on the screen names a key that moved: "X again", "t moves",
// "v plays it", "(A)" each pointed at a key that does something else now
// (or only says where it went).
func TestHintsNameTodaysKeys(t *testing.T) {
	var old []string
	for _, b := range narKeys.Binds {
		if b.Moved != "" && len([]rune(b.Seq)) == 1 {
			old = append(old, regexp.QuoteMeta(b.Seq))
		}
	}
	old = append(old, "v", "A") // v is VISUAL now; A asks Claude to write
	keys := `(?:` + strings.Join(old, "|") + `)`
	verbs := `(?:again|moves|shows|plays|opens|sends|saves|marks|mutes|blocks|for more|edits)\b`
	hint := regexp.MustCompile(`(?:"|· |\()` + keys + `(?: ` + verbs + `|\)"| · )|· ` + keys + `"`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "keymap.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "case ") || strings.Contains(line, "old(") || strings.Contains(line, "whatsapp.net") {
				continue // the old letters as the implementation's names; phone numbers
			}
			if loc := hint.FindString(line); loc != "" {
				t.Errorf("%s:%d names a moved key in a hint (%s): %s", f, i+1, loc, strings.TrimSpace(line))
			}
		}
	}
}
