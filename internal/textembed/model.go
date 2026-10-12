package textembed

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/vincentlark/neighbeats/internal/embed"
)

const (
	// ModelID names the model and the text preparation (Prepare, the
	// "passage: " prefix, MaxTokens). It is stored with every lyric embedding;
	// bump it when any of them changes.
	ModelID = "multilingual-e5-small-passage-512"
	// Dim is the length of one embedding vector.
	Dim = 384
	// MaxTokens is the model's input limit, special tokens included.
	MaxTokens = 512

	// PassagePrefix goes before a lyric and QueryPrefix before a free-text
	// theme, as the e5 models expect ("query: " for the short side of an
	// asymmetric search).
	PassagePrefix = "passage: "
	QueryPrefix   = "query: "

	// maxBatchTokens bounds batch size * longest sequence, which sets the
	// memory of one inference. Small enough for a 6 GB GPU.
	maxBatchTokens = 8192
)

// Options configures Open.
type Options struct {
	// Dir holds model.onnx and tokenizer.json.
	Dir         string
	LibraryPath string
	Provider    embed.Provider
	Logf        func(format string, args ...any)
}

// Model embeds text. It is safe for concurrent use.
type Model struct {
	tok      *Tokenizer
	sess     *ort.DynamicAdvancedSession
	provider string
	mu       sync.Mutex
}

// Open loads the tokenizer and the ONNX model from opt.Dir.
func Open(opt Options) (*Model, error) {
	tok, err := LoadTokenizer(filepath.Join(opt.Dir, "tokenizer.json"))
	if err != nil {
		return nil, err
	}
	sess, provider, err := embed.OpenSession(embed.ORTOptions{
		ModelPath: filepath.Join(opt.Dir, "model.onnx"), LibraryPath: opt.LibraryPath,
		Provider: opt.Provider, Logf: opt.Logf,
	}, []string{"input_ids", "attention_mask", "token_type_ids"}, []string{"last_hidden_state"})
	if err != nil {
		return nil, err
	}
	return &Model{tok: tok, sess: sess, provider: provider}, nil
}

// Provider names the execution provider in use.
func (m *Model) Provider() string { return m.provider }

// Close releases the session.
func (m *Model) Close() error { return m.sess.Destroy() }

// Tokenize returns the ids Embed would feed the model for prefix+text.
func (m *Model) Tokenize(prefix, text string) []int64 { return m.tok.Encode(prefix+text, MaxTokens) }

// Embed returns one unit-length vector per text, in order. Every text gets the
// prefix first. Texts are batched by length so padding stays small.
func (m *Model) Embed(prefix string, texts []string) ([][]float32, error) {
	ids := make([][]int64, len(texts))
	for i, t := range texts {
		ids[i] = m.Tokenize(prefix, t)
	}
	return m.EmbedTokens(ids)
}

// EmbedTokens is Embed for texts that are already tokenised (<s> ... </s>).
func (m *Model) EmbedTokens(ids [][]int64) ([][]float32, error) {
	order := make([]int, len(ids))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return len(ids[order[a]]) > len(ids[order[b]]) })
	out := make([][]float32, len(ids))
	for start := 0; start < len(order); {
		longest := len(ids[order[start]])
		n := max(1, maxBatchTokens/longest)
		end := min(start+n, len(order))
		batch := order[start:end]
		vecs, err := m.run(ids, batch, longest)
		if err != nil {
			return nil, err
		}
		for i, v := range vecs {
			out[batch[i]] = v
		}
		start = end
	}
	return out, nil
}

func (m *Model) run(ids [][]int64, batch []int, width int) ([][]float32, error) {
	n := len(batch)
	input := make([]int64, n*width)
	mask := make([]int64, n*width)
	for r, idx := range batch {
		row := input[r*width : (r+1)*width]
		for i := range row {
			row[i] = PAD
		}
		copy(row, ids[idx])
		for i := range ids[idx] {
			mask[r*width+i] = 1
		}
	}
	shape := ort.NewShape(int64(n), int64(width))
	tIDs, err := ort.NewTensor(shape, input)
	if err != nil {
		return nil, err
	}
	defer tIDs.Destroy()
	tMask, err := ort.NewTensor(shape, mask)
	if err != nil {
		return nil, err
	}
	defer tMask.Destroy()
	tTypes, err := ort.NewTensor(shape, make([]int64, n*width))
	if err != nil {
		return nil, err
	}
	defer tTypes.Destroy()
	out, err := ort.NewEmptyTensor[float32](ort.NewShape(int64(n), int64(width), Dim))
	if err != nil {
		return nil, err
	}
	defer out.Destroy()

	m.mu.Lock()
	err = m.sess.Run([]ort.Value{tIDs, tMask, tTypes}, []ort.Value{out})
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	hidden := out.GetData()
	vecs := make([][]float32, n)
	for r, idx := range batch {
		v := make([]float32, Dim)
		for i := 0; i < len(ids[idx]); i++ { // mean over the real tokens
			for d, x := range hidden[(r*width+i)*Dim : (r*width+i+1)*Dim] {
				v[d] += x
			}
		}
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
			return nil, fmt.Errorf("text %d: degenerate embedding", idx)
		}
		inv := float32(1 / math.Sqrt(norm))
		for d := range v {
			v[d] *= inv
		}
		vecs[r] = v
	}
	return vecs, nil
}

// Prepare turns lyric text into what is embedded: each distinct line once, in
// order, so a repeated chorus does not use up the token budget.
func Prepare(lyrics string) string {
	seen := map[string]bool{}
	var keep []string
	for _, line := range strings.Split(lyrics, "\n") {
		line = strings.TrimSpace(line)
		key := strings.ToLower(line)
		if line == "" || seen[key] {
			continue
		}
		seen[key] = true
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}
