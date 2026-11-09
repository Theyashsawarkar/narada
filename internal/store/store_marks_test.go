package store

import "testing"

// Marks (m{a-z}) last across runs: set, read back, set again.
func TestMarks(t *testing.T) {
	s, err := Open(t.TempDir() + "/n.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, _, ok, err := s.Mark("a"); ok || err != nil {
		t.Fatalf("unset mark: ok=%v err=%v", ok, err)
	}
	if err := s.SetMark("a", 2, 1, "whatsapp/123@s.whatsapp.net"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMark("a", 0, 3, "me@x/t1"); err != nil {
		t.Fatal(err)
	}
	p, sec, item, ok, err := s.Mark("a")
	if !ok || err != nil || p != 0 || sec != 3 || item != "me@x/t1" {
		t.Fatalf("got %d %d %q %v %v", p, sec, item, ok, err)
	}
}
