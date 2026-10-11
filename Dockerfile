# syntax=docker/dockerfile:1

# neighbeats: CPU + CUDA 12 image. One image runs everywhere: with the NVIDIA
# container toolkit and `--gpus all` the CUDA provider is used, otherwise
# `provider: auto` logs "CUDA unavailable" and falls back to the CPU.
#
#   docker build -t neighbeats .
#   docker run --rm --gpus all \
#     -e NEIGHBEATS_CONFIG=/config/config.yaml \
#     -v ./config.yaml:/config/config.yaml:ro \
#     -v ./data:/data -v ./models:/models:ro -v /path/to/music:/music:ro \
#     neighbeats analyze
#
# Host requirement for the GPU: NVIDIA driver >= 525 and the NVIDIA container
# toolkit (CUDA 12 runs on older drivers through minor-version compatibility).
#
# The model is not baked in (CC BY-NC-SA): mount it at /models. config.example.yaml
# already uses these container paths; compose.yaml sets up all the mounts.

ARG ORT_VERSION=1.29.0
ARG CUDA_IMAGE=nvidia/cuda:12.8.1-cudnn-runtime-ubuntu22.04

# --- ONNX Runtime GPU build ---------------------------------------------------
# 1.29 matches the C API headers of the Go binding (yalue/onnxruntime_go).
# The cuda12 build is required for the GTX 1060 (Pascal): the cuda13 build
# dropped it. Its CPU kernels pick AVX/AVX2 at run time, so a CPU without
# AVX2 (i7-3770K) still works.
FROM debian:bookworm-slim AS ort
ARG ORT_VERSION
ARG ORT_SHA256=4ca594a0da83927befbd73fe020d7f569be151d70bb4fe9741ad405f4882e2ad
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL --retry 8 --retry-delay 3 --retry-all-errors -C - -o /ort.tgz \
      "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-x64-gpu_cuda12-${ORT_VERSION}.tgz" \
 && echo "${ORT_SHA256}  /ort.tgz" | sha256sum -c - \
 && mkdir /ort && tar -xzf /ort.tgz -C /ort --strip-components=1 \
 && rm /ort/lib/libonnxruntime_providers_tensorrt.so

# --- build --------------------------------------------------------------------
# Built on the same Ubuntu as the runtime image so the cgo binary links
# against a compatible glibc.
FROM ubuntu:22.04 AS build
COPY --from=golang:1.26-bookworm /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH CGO_ENABLED=1 GOFLAGS=-trimpath
RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev ca-certificates git \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN go build -ldflags "-s -w -X main.version=${VERSION}" -o /neighbeats ./cmd/neighbeats

# --- runtime ------------------------------------------------------------------
FROM ${CUDA_IMAGE}
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg ca-certificates \
 && rm -rf /var/lib/apt/lists/*
# The base image ships CUDA forward-compat libraries (cuda-compat-12-8, driver
# 570). The container toolkit switches them on when the host driver is older,
# but forward compatibility only works on datacenter GPUs: on a GeForce card
# it fails with "CUDA failure 804: forward compatibility was attempted on non
# supported HW". Remove them so the host driver (>= 525) is used directly, and
# drop the image's "cuda>=12.8" start constraint, which only the compat libs
# could satisfy on an older driver.
RUN apt-get purge -y cuda-compat-12-8 && apt-get autoremove -y \
 && rm -rf /usr/local/cuda/compat /var/lib/apt/lists/*
COPY --from=ort /ort/lib/ /opt/onnxruntime/lib/
COPY --from=build /neighbeats /usr/local/bin/neighbeats
# ONNX Runtime finds libonnxruntime_providers_cuda.so next to the main library
# or through the loader path.
ENV LD_LIBRARY_PATH=/opt/onnxruntime/lib \
    NVIDIA_DISABLE_REQUIRE=true \
    NVIDIA_VISIBLE_DEVICES=all \
    NVIDIA_DRIVER_CAPABILITIES=compute,utility
VOLUME ["/data"]
ENTRYPOINT ["neighbeats"]
CMD ["help"]
