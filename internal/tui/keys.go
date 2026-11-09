package tui

// Key is one row of the key sheet (?), made from the keymap (keymap.go):
// the same table feeds the key sheet, the README's key tables and
// scripts/keybindings (Vayu's keybinding search reads that file; a test
// keeps both in sync).
type Key struct {
	Group string
	Keys  string
	Desc  string
}

// Keys is the key sheet: every view's keys, the Space groups each under
// its own heading.
var Keys = keySheet()

func keySheet() []Key {
	var out []Key
	for _, sec := range narKeys.Sections() {
		group := sec.Title
		for _, r := range sec.Rows {
			if r.Head {
				group = r.Desc
				if r.Keys != "" {
					group = r.Keys + " · " + r.Desc
				}
				continue
			}
			out = append(out, Key{Group: group, Keys: r.Keys, Desc: r.Desc})
		}
	}
	return out
}

// KeybindLines is scripts/keybindings: Vayu's `# keybind:` annotations,
// scoped "narada <view>:<mode>".
func KeybindLines() string {
	return narKeys.KeybindingsFile("Tmux", "go test ./internal/tui -update")
}

// KeyTables is the README's key tables, between the markers.
func KeyTables() string { return narKeys.Markdown() }
