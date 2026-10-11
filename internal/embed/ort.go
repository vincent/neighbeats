package embed

import (
	"fmt"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// Provider selects the ONNX Runtime execution provider.
type Provider string

const (
	ProviderAuto Provider = "auto" // CUDA if it initialises, else CPU
	ProviderCUDA Provider = "cuda" // CUDA or fail
	ProviderCPU  Provider = "cpu"
)

// ORTOptions configures NewORT.
type ORTOptions struct {
	ModelPath string
	// LibraryPath is the libonnxruntime shared library; empty means
	// "libonnxruntime.so" via the system loader.
	LibraryPath string
	Provider    Provider
	// IntraOpThreads limits CPU threads per inference (0 = ONNX default).
	IntraOpThreads int
	// Logf receives one line when auto mode falls back to the CPU.
	Logf func(format string, args ...any)
}

var (
	envOnce sync.Once
	envErr  error
)

type ortEmbedder struct {
	sess     *ort.DynamicAdvancedSession
	provider string
	mu       sync.Mutex // ORT sessions are thread-safe, but one lock keeps GPU memory use predictable
}

// NewORT loads the model. In auto mode a CUDA failure is logged and the
// session is rebuilt on the CPU.
func NewORT(o ORTOptions) (Embedder, error) {
	if o.Provider == "" {
		o.Provider = ProviderAuto
	}
	envOnce.Do(func() {
		// The binding's own default is "onnxruntime.so", which no package
		// ships; let the system loader find the usual name instead.
		lib := o.LibraryPath
		if lib == "" {
			lib = "libonnxruntime.so"
		}
		ort.SetSharedLibraryPath(lib)
		envErr = ort.InitializeEnvironment()
	})
	if envErr != nil {
		return nil, fmt.Errorf("init onnxruntime (library %q): %w", o.LibraryPath, envErr)
	}

	switch o.Provider {
	case ProviderCPU:
		return newSession(o, false)
	case ProviderCUDA:
		return newSession(o, true)
	case ProviderAuto:
		e, err := newSession(o, true)
		if err == nil {
			return e, nil
		}
		if o.Logf != nil {
			o.Logf("CUDA unavailable (%v); using the CPU", err)
		}
		return newSession(o, false)
	}
	return nil, fmt.Errorf("unknown provider %q (want auto, cuda or cpu)", o.Provider)
}

func newSession(o ORTOptions, cuda bool) (Embedder, error) {
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer opts.Destroy()
	if o.IntraOpThreads > 0 {
		if err := opts.SetIntraOpNumThreads(o.IntraOpThreads); err != nil {
			return nil, err
		}
	}
	name := "cpu"
	if cuda {
		co, err := ort.NewCUDAProviderOptions()
		if err != nil {
			return nil, fmt.Errorf("cuda options: %w", err)
		}
		defer co.Destroy()
		if err := opts.AppendExecutionProviderCUDA(co); err != nil {
			return nil, fmt.Errorf("cuda provider: %w", err)
		}
		name = "cuda"
	}
	sess, err := ort.NewDynamicAdvancedSession(o.ModelPath,
		[]string{"melspectrogram"}, []string{"embeddings"}, opts)
	if err != nil {
		return nil, fmt.Errorf("load %s on %s: %w", o.ModelPath, name, err)
	}
	return &ortEmbedder{sess: sess, provider: name}, nil
}

func (e *ortEmbedder) Embed(patches []float32, n int) ([]float32, error) {
	in, err := ort.NewTensor(ort.NewShape(int64(n), PatchFrames, NumBands), patches)
	if err != nil {
		return nil, err
	}
	defer in.Destroy()
	out, err := ort.NewEmptyTensor[float32](ort.NewShape(int64(n), Dim))
	if err != nil {
		return nil, err
	}
	defer out.Destroy()

	e.mu.Lock()
	err = e.sess.Run([]ort.Value{in}, []ort.Value{out})
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return append([]float32(nil), out.GetData()...), nil
}

func (e *ortEmbedder) Provider() string { return e.provider }
func (e *ortEmbedder) Close() error     { return e.sess.Destroy() }
