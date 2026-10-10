# M1: Embeddings and throughput

Goal: turn each track into a vector that says how it sounds, and learn what the
full library will cost on this hardware. Iterate on a **~500 track dev slice**, then embed
the **whole library (25,125 tracks) in one resumable run**: there is no need for a
separate large pilot.

## Step 0: smoke tests (do these before anything else)

- [ ] ONNX Runtime with the CUDA provider runs a trivial model on the GTX 1060
      inside Docker (Debian, NVIDIA container toolkit already works).
- [ ] The same binary starts on the i7-3770K (AVX, no AVX2).
- [ ] Choose the Go binding and record the exact versions here.

## Tasks

- [ ] Dockerfile (multi-stage) and compose file with GPU access and the music
      share mounted read-only at the path in `music_roots`.
- [ ] Pick the embedding model. Candidates: Essentia's Discogs-EffNet-style
      music embedding models (ONNX/TF; license CC BY-NC-SA, fine for personal
      use). Read the model card for the exact input: sample rate, FFT size, hop,
      number of mel bands, normalisation, patch length. **Do not guess these.**
- [ ] Decode: one clip per track via ffmpeg (start with 30 s from the middle),
      mono, model sample rate. Compare against a symphonia/pure-Go decode only if
      process spawning shows up in the profile.
- [ ] Mel-spectrogram in Go matching the model's reference preprocessing.
- [ ] Dev-time check (script allowed here, never shipped): reference
      implementation vs Go output on ~10 tracks; cosine similarity of the
      resulting embeddings should be ~1.0.
- [ ] `analyze` command: resumable, parallel decode, batched GPU inference, results
      stored in a new `embeddings` table (track id, model id+version, vector as
      float32 blob, clip params). Re-run skips tracks already done for the same
      model version.
- [ ] Also store duration and a cheap tempo/energy estimate if the model does not
      give them (used later for transition scoring); decide after listening tests.
- [ ] Timing report: seconds per track split into decode / preprocess / inference
      / DB write, measured on the dev slice and then confirmed on the full run.

## Exit criteria

- Whole library embedded; the timing table is in this file.
- The bottleneck is named, with the worker count that was best.
- Embedding quality is plausible: nearest neighbours of 10 hand-picked tracks
  are not nonsense (formal check comes in M2).

## Decisions to record here when made

Model chosen, clip length and position, vector size, ONNX Runtime version.
