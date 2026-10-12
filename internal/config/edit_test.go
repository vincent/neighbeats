package config

import (
	"strings"
	"testing"
)

const sample = `# top comment
db_path: /data/x.sqlite
path_map:
  - local: /music
    remote: /data/music   # as Jellyfin sees it
provider: auto
other:
  remote: /nope
`

func TestSetFirstRemote(t *testing.T) {
	got, ok := SetFirstRemote(sample, "/mnt/media")
	if !ok || !strings.Contains(got, "    remote: /mnt/media\n") || !strings.Contains(got, "/nope") || !strings.Contains(got, "# top comment") {
		t.Fatalf("ok=%v\n%s", ok, got)
	}
	if _, ok := SetFirstRemote("db_path: x\nother:\n  remote: /a\n", "/b"); ok {
		t.Fatal("edited a remote outside path_map")
	}
}

func TestSetProvider(t *testing.T) {
	if got := SetProvider(sample, "cpu"); !strings.Contains(got, "\nprovider: cpu\n") || strings.Contains(got, "provider: auto") {
		t.Fatal(got)
	}
	if got := SetProvider("a: 1\n", "cpu"); got != "a: 1\nprovider: cpu\n" {
		t.Fatalf("%q", got)
	}
}

func TestValidRemote(t *testing.T) {
	for _, ok := range []string{"/data/music", `D:\Music`} {
		if err := ValidRemote(ok); err != nil {
			t.Error(ok, err)
		}
	}
	for _, bad := range []string{"music", "", "/a\nb", "/a#b"} {
		if ValidRemote(bad) == nil {
			t.Error("accepted", bad)
		}
	}
}
