package pathmap

import "testing"

func TestTranslate(t *testing.T) {
	m, err := New([]Mapping{
		{Local: "/music", Remote: "/data/music"},
		{Local: "/music/archive", Remote: "/data/old"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		in, want string
		ok       bool
		remote   bool
	}{
		{"/music/a/b.mp3", "/data/music/a/b.mp3", true, true},
		{"/music/archive/x.mp3", "/data/old/x.mp3", true, true}, // longest prefix wins
		{"/music2/x.mp3", "/music2/x.mp3", false, true},         // boundary respected
		{"/music", "/data/music", true, true},
		{"/data/music/a/b.mp3", "/music/a/b.mp3", true, false},
		{"/data/old/x.mp3", "/music/archive/x.mp3", true, false},
		{"/elsewhere/x.mp3", "/elsewhere/x.mp3", false, false},
	}
	for _, c := range cases {
		var got string
		var ok bool
		if c.remote {
			got, ok = m.ToRemote(c.in)
		} else {
			got, ok = m.ToLocal(c.in)
		}
		if got != c.want || ok != c.ok {
			t.Errorf("%q (remote=%v) = %q,%v want %q,%v", c.in, c.remote, got, ok, c.want, c.ok)
		}
	}
}

func TestRootMapping(t *testing.T) {
	m, err := New([]Mapping{{Local: "/music", Remote: "/"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := m.ToRemote("/music/a.mp3"); got != "/a.mp3" {
		t.Errorf("ToRemote = %q", got)
	}
	if got, _ := m.ToLocal("/a.mp3"); got != "/music/a.mp3" {
		t.Errorf("ToLocal = %q", got)
	}
}

func TestValidation(t *testing.T) {
	if _, err := New([]Mapping{{Local: "music", Remote: "/x"}}); err == nil {
		t.Error("relative local accepted")
	}
	if m, err := New(nil); err != nil {
		t.Fatal(err)
	} else if got, ok := m.ToRemote("/a"); got != "/a" || ok {
		t.Errorf("identity mapper = %q,%v", got, ok)
	}
}
