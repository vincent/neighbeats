#!/bin/sh
# Downloads the ONNX Runtime CPU libraries that the Debian packages bundle into
# packaging/ort/<amd64|arm64>/. Run by goreleaser before the build.
set -eu
V=1.29.0
cd "$(dirname "$0")"
fetch() { # goarch ortarch sha256
  out="ort/$1"
  [ -f "$out/libonnxruntime.so" ] && return 0
  tmp=$(mktemp -d)
  curl -fsSL --retry 8 --retry-delay 3 --retry-all-errors -o "$tmp/ort.tgz" \
    "https://github.com/microsoft/onnxruntime/releases/download/v$V/onnxruntime-linux-$2-$V.tgz"
  echo "$3  $tmp/ort.tgz" | sha256sum -c -
  tar -xzf "$tmp/ort.tgz" -C "$tmp" --strip-components=1
  mkdir -p "$out"
  cp -L "$tmp/lib/libonnxruntime.so" "$out/libonnxruntime.so"
  cp -L "$tmp/lib/libonnxruntime_providers_shared.so" "$out/libonnxruntime_providers_shared.so"
  cp "$tmp/LICENSE" "$out/LICENSE"
  cp "$tmp/ThirdPartyNotices.txt" "$out/ThirdPartyNotices.txt"
  rm -rf "$tmp"
}
fetch amd64 x64     c3fddc4f139a045b0c4902c57410f0694f1c2fdf9b6939fbe38b1aeae7cd14ba
fetch arm64 aarch64 e1799098ebc054b370f6176a450f158720f297818c613e5dc99b92e2ec82346f
