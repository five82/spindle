#!/bin/bash
# Stage native CI dependencies for caching; never install into the host here.
set -euo pipefail
if [ "$#" -ne 1 ] || [ -z "$1" ]; then
    echo "Usage: $0 <staging-directory>" >&2
    exit 2
fi
mkdir -p "$1"
INSTALL_ROOT=$(cd "$1" && pwd -P)
if [ "$INSTALL_ROOT" = / ]; then
    echo "Refusing to install into the host; provide a staging directory." >&2
    exit 2
fi
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Upstream SVT-AV1 v4.2.0 and FFmpeg n8.1.3. Debian 13's SVT 2.3 lacks
# ac-bias and rejects small encode fixtures. Recipe changes invalidate the cache.
SVT_REVISION=9292ec8e32bce26f781f277ec8739b53426c4300
FFMPEG_REVISION=1041abdc962f4cc4f394aa8de9dc5236c0c3b9e7

echo "Phase 1/2 - Build SVT-AV1 (4.2.0)"
git init "$WORK/svt"
git -C "$WORK/svt" fetch --depth=1 https://gitlab.com/AOMediaCodec/SVT-AV1.git "$SVT_REVISION"
git -C "$WORK/svt" checkout --detach FETCH_HEAD
test "$(git -C "$WORK/svt" rev-parse HEAD)" = "$SVT_REVISION"
# Match the workstation's shared Clang/Release/LTO build, but keep cached
# binaries portable across CPUs of the same architecture. No CUDA or VSHIP.
cmake -S "$WORK/svt" -B "$WORK/svt-build" \
    -DCMAKE_INSTALL_PREFIX=/usr/local \
    -DCMAKE_INSTALL_LIBDIR=lib \
    -DCMAKE_C_COMPILER=clang \
    -DCMAKE_CXX_COMPILER=clang++ \
    -DBUILD_SHARED_LIBS=ON \
    -DCMAKE_BUILD_TYPE=Release \
    -DBUILD_APPS=OFF \
    -DSVT_AV1_LTO=ON \
    -DNATIVE=OFF \
    -DCMAKE_C_FLAGS=-O3 \
    -DCMAKE_CXX_FLAGS=-O3
cmake --build "$WORK/svt-build" --parallel 4
DESTDIR="$INSTALL_ROOT" cmake --install "$WORK/svt-build"

echo "Phase 2/2 - Build FFmpeg (8.1.3)"
git init "$WORK/ffmpeg"
git -C "$WORK/ffmpeg" fetch --depth=1 https://github.com/FFmpeg/FFmpeg.git "$FFMPEG_REVISION"
git -C "$WORK/ffmpeg" checkout --detach FETCH_HEAD
test "$(git -C "$WORK/ffmpeg" rev-parse HEAD)" = "$FFMPEG_REVISION"
cd "$WORK/ffmpeg"
# libav must not bring a second SVT ABI into Spindle. Reel links SVT directly;
# FFmpeg only supplies decoding/filtering/muxing, plus the CLI tools for tests.
./configure --prefix=/usr/local --enable-shared --disable-static \
    --disable-autodetect --enable-gpl --enable-libdav1d --disable-libsvtav1 \
    --disable-doc --disable-debug
make -j4
make DESTDIR="$INSTALL_ROOT" install
