package lyrics

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincent/neighbeats/internal/store"
	"github.com/vincent/neighbeats/internal/textembed"
)

// fakeText embeds a text as a one-hot of its first letter.
type fakeText struct{ calls int }

func (f *fakeText) Tokenize(prefix, text string) []int64 {
	ids := []int64{0}
	for _, r := range prefix + text {
		ids = append(ids, int64(r))
	}
	return append(ids, 2)
}

func (f *fakeText) EmbedTokens(ids [][]int64) ([][]float32, error) {
	f.calls++
	out := make([][]float32, len(ids))
	for i, x := range ids {
		v := make([]float32, textembed.Dim)
		v[int(x[len(textembed.PassagePrefix)+1])%textembed.Dim] = 1
		out[i] = v
	}
	return out, nil
}

func TestEmbedAllIsResumable(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	music := t.TempDir()
	run, _ := st.StartRun(ctx, []string{music})
	var ts []store.Track
	for _, n := range []string{"a", "b", "c"} {
		p := filepath.Join(music, n+".mp3")
		os.WriteFile(p, []byte("x"), 0o644)
		os.WriteFile(filepath.Join(music, n+".lrc"), []byte(strings.Repeat(n+" la\n", 3)), 0o644)
		ts = append(ts, store.Track{Path: p, Size: 1, MtimeNs: 1, Format: "mp3"})
	}
	if err := st.UpsertTracks(ctx, run, ts); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, st); err != nil {
		t.Fatal(err)
	}

	f := &fakeText{}
	res, err := EmbedAll(ctx, st, f, EmbedOptions{Limit: 2, Chunk: 1})
	if err != nil || res.Pending != 3 || res.Done != 2 || f.calls != 2 {
		t.Fatalf("first run: %+v calls %d err %v", res, f.calls, err)
	}
	res, err = EmbedAll(ctx, st, f, EmbedOptions{})
	if err != nil || res.Pending != 1 || res.Done != 1 {
		t.Fatalf("second run: %+v err %v", res, err)
	}
	if res, _ = EmbedAll(ctx, st, f, EmbedOptions{}); res.Pending != 0 {
		t.Fatalf("third run: %+v", res)
	}
	if n, _ := st.LyricEmbeddingCount(ctx, textembed.ModelID); n != 3 {
		t.Fatalf("stored %d", n)
	}
}
