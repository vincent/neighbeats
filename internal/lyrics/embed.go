package lyrics

import (
	"context"
	"fmt"

	"github.com/vincent/neighbeats/internal/store"
	"github.com/vincent/neighbeats/internal/textembed"
)

// TextEmbedder is what EmbedAll needs from the text model.
type TextEmbedder interface {
	Tokenize(prefix, text string) []int64
	EmbedTokens(ids [][]int64) ([][]float32, error)
}

// EmbedOptions tunes EmbedAll.
type EmbedOptions struct {
	// Limit stops after this many lyrics (0 = all).
	Limit int
	// Chunk is how many lyrics are embedded and saved together (default 256).
	Chunk int
	// Logf receives progress lines.
	Logf func(format string, args ...any)
	// Progress receives the running totals after each chunk; nil means Logf.
	Progress func(format string, args ...any)
}

// EmbedResult counts what EmbedAll did.
type EmbedResult struct {
	Pending   int
	Done      int
	Truncated int // lyrics longer than the model's input limit
}

// EmbedAll embeds every lyric that has no up-to-date vector for
// textembed.ModelID. It is resumable: each chunk is saved as it completes.
func EmbedAll(ctx context.Context, st *store.Store, m TextEmbedder, opt EmbedOptions) (EmbedResult, error) {
	var res EmbedResult
	if opt.Chunk <= 0 {
		opt.Chunk = 256
	}
	pending, err := st.PendingLyricEmbeddings(ctx, textembed.ModelID)
	if err != nil {
		return res, err
	}
	res.Pending = len(pending)
	if opt.Limit > 0 && len(pending) > opt.Limit {
		pending = pending[:opt.Limit]
	}
	for start := 0; start < len(pending); start += opt.Chunk {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		part := pending[start:min(start+opt.Chunk, len(pending))]
		ids := make([][]int64, len(part))
		for i, p := range part {
			ids[i] = m.Tokenize(textembed.PassagePrefix, textembed.Prepare(p.Text))
			if len(ids[i]) >= textembed.MaxTokens {
				res.Truncated++
			}
		}
		vecs, err := m.EmbedTokens(ids)
		if err != nil {
			return res, fmt.Errorf("embed lyrics from track %d: %w", part[0].TrackID, err)
		}
		es := make([]store.LyricEmbedding, len(part))
		for i, p := range part {
			es[i] = store.LyricEmbedding{TrackID: p.TrackID, Vector: vecs[i], Tokens: len(ids[i]), Size: p.Size, MtimeNs: p.MtimeNs}
		}
		if err := st.SaveLyricEmbeddings(ctx, textembed.ModelID, es); err != nil {
			return res, err
		}
		res.Done += len(part)
		report := opt.Progress
		if report == nil && opt.Logf != nil {
			report = opt.Logf
		}
		if report != nil {
			report("embedded %d of %d lyrics", res.Done, len(pending))
		}
	}
	return res, nil
}
