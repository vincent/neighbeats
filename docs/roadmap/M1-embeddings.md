# M1: Embeddings and throughput

Goal: turn each track into a vector that says how it sounds, and learn what the
full library will cost on this hardware. Iterate on a **~500 track dev slice**, then embed
the **whole library (25,125 tracks) in one resumable run**: there is no need for a
separate large pilot.

## Step 0: smoke tests (do these before anything else)

- [x] ONNX Runtime runs on the VPS (Python `onnxruntime`; CPU provider also
      verified here with the Go binding).
- [ ] ONNX Runtime with the CUDA provider runs the model on the GTX 1060
      inside Docker (Debian, NVIDIA container toolkit already works). Needs the
      `onnxruntime-gpu` build of `libonnxruntime.so` (the pip/CPU one has no CUDA
      provider); `provider: auto` logs the fallback and uses the CPU.
- [ ] The same binary starts on the i7-3770K (AVX, no AVX2).
- [x] Choose the Go binding and record the exact versions here (see Decisions).

## Tasks

- [x] Dockerfile (multi-stage; gcc only in the build stage, see the cgo decision
      below). Still to verify: an image build and a run on the NAS.
- [x] Compose file (`compose.yaml`, config template `config.example.yaml`): GPU
      reservation, music share mounted read-only at `/music`, runs as your uid.
      Still to verify on the NAS.
- [x] Pick the embedding model. Candidates: Essentia's Discogs-EffNet-style
      music embedding models (ONNX/TF; license CC BY-NC-SA, fine for personal
      use). Read the model card for the exact input: sample rate, FFT size, hop,
      number of mel bands, normalisation, patch length. **Do not guess these.**
- [x] Decode: one clip per track via ffmpeg (start with 30 s from the middle),
      mono, model sample rate. Compare against a symphonia/pure-Go decode only if
      process spawning shows up in the profile.
- [x] Mel-spectrogram in Go matching the model's reference preprocessing.
- [x] Dev-time check (script allowed here, never shipped): reference
      implementation vs Go output on ~10 tracks; cosine similarity of the
      resulting embeddings should be ~1.0.
- [x] `analyze` command: resumable, parallel decode, batched GPU inference, results
      stored in a new `embeddings` table (track id, model id+version, vector as
      float32 blob, clip params). Re-run skips tracks already done for the same
      model version.
- [ ] Also store duration and a cheap tempo/energy estimate if the model does not
      give them (used later for transition scoring); decide after listening tests.
- [x] Timing report (CLI prints it): seconds per track split into decode / preprocess / inference
      / DB write, measured on the dev slice and then confirmed on the full run.

## Exit criteria

- Whole library embedded; the timing table is in this file.
- The bottleneck is named, with the worker count that was best.
- Embedding quality is plausible: nearest neighbours of 10 hand-picked tracks
  are not nonsense (formal check comes in M2).

## Decisions to record here when made

Model chosen, clip length and position, vector size, ONNX Runtime version.

### Recorded so far (dev slice, 2026-10-11)

- **Model:** Essentia `discogs-effnet-bsdynamic-1.onnx` (input `melspectrogram`
  `[batch,128,96]`, outputs `activations` 400 and `embeddings` 1280; 16 kHz).
  Track vector = mean of the per-patch embeddings, stored as float32 LE.
- **Preprocessing (matches Essentia `TensorflowInputMusiCNN`, golden test in
  `internal/embed/mel_test.go`):** Hann 512 / hop 256, power spectrum, 96 Slaney
  mel bands 0-8 kHz with unit-area triangles (Essentia's "unit_tri"),
  `log10(10000x+1)`, patches of 128 frames every 62 frames (29 per 30 s clip).
- **Clip:** 30 s from the middle (ffprobe duration + `ffmpeg -ss`), mono, 16 kHz.
- **Go binding:** `github.com/yalue/onnxruntime_go` v1.36.0 (headers for ORT 1.29;
  run against `libonnxruntime` 1.31.0 from the Python wheel, CPU provider). cgo is
  now required to build.
- **cgo (decided 2026-10-11): keep it, confine it to Docker.** The ONNX binding is
  the only cgo dependency (SQLite stays pure Go, ffmpeg is spawned). The
  multi-stage Dockerfile builds with gcc on the same Ubuntu 22.04 as the CUDA
  runtime image, so the final image carries only the binary, `libonnxruntime`
  and ffmpeg. Cost: a C compiler on dev machines and no easy cross-compile (build
  natively per arch). Rejected: a purego wrapper over the ORT C API (about 15
  calls, but ORT struct-offset drift between versions is ours to maintain; revisit
  only if `CGO_ENABLED=0` becomes a real need), a pure-Go ONNX runtime (missing
  ops, slow, no GPU), a Python/C++ sidecar (second runtime, same GPU problem).
- **Reference check (10 tracks):** Go vs Python (Essentia mel + ORT) on the same
  ffmpeg samples: cosine 1.0000. ONNX vs the Essentia `.pb` on the same samples:
  0.9995. Against the `.pb` fed by Essentia's own decoder: 0.95, all of it decoder
  and alignment difference (the `.pb` alone gives 0.95 between the two decoders).
- **Neighbours (500-track dev slice):** top-1 neighbour shares the artist for
  82% of tracks; hand-checked neighbours are genre-coherent.

| Machine | Provider | Workers | Tracks/s | Per track, summed over workers (decode / mel / inference / write) |
|---|---|---|---|---|
| i7-13620H laptop, 500 tracks | CPU | 2 | 8.9 | |
| | CPU | 4 | 14.2 | |
| | CPU | 8 | 16.7 | |
| | CPU | 15 | 19.4 | 388 ms / 32 ms / 51 ms / 0.6 ms |

Bottleneck on this machine: ffmpeg decode (CPU). Still to do: the same table on
the NAS (GTX 1060, i7-3770K) and for the full 25,125 tracks.
