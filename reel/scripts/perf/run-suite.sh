#!/usr/bin/env bash
#
# run-suite.sh -- run reel over the standard perf matrix, capturing environment
# metadata, wall time, output size, GPU utilization/VRAM, and the per-encode
# perf.json / target-quality.json artifacts into a timestamped run directory.
#
# The repo holds this harness and the clip manifest (scripts/perf/clips.tsv);
# the clip bytes and run outputs live under $REEL_TESTING_DIR (default
# ~/testing). See scripts/perf/README.md for the harness and corpus, and
# docs/PERFORMANCE_TESTING.md for prior decisions.
#
# Runs are strictly sequential so another GPU workload cannot contaminate wall,
# utilization, thermal, or VRAM comparisons.
#
# Usage:
#   scripts/perf/run-suite.sh [options] [clip ...]
#
# Options:
#   --mode crf|tq      Encode mode (default: tq).
#   --crf N            CRF for crf mode (default: 28).
#   --label NAME       Label folded into the run directory name (default: run).
#   --matrix NAME      Clip matrix when no explicit clips are provided:
#                      default, coverage, encoder, or long (default: default).
#   --reel PATH        reel binary to use (default: ./reel in repo if present,
#                      otherwise `reel` on PATH).
#   --out DIR          Run-output root (default: $REEL_TESTING_DIR/perf-runs).
#   --keep-workdirs    Keep full .reel-* workdirs (needed for fullvalidate);
#                      by default they are deleted after the small JSON
#                      artifacts are harvested, to reclaim disk.
#   --gpu-interval S   nvidia-smi sample interval in seconds (default: 2).
#   -- ARGS...         Everything after -- is passed through to `reel encode`.
#
# Clips are resolved under $REEL_TESTING_DIR by prefix (e.g. `sully-5m` ->
# sully-5m-4k-hdr.mkv) or given as a direct path to an .mkv. With no clips the
# selected matrix is used. The default matrix is the historical A/B anchor:
# air-5m im-5m bts-5m sully-5m kbv1-5m sullyhv-15m. sullyhv is a derived local
# stress asset, not a single-row clips.tsv cut.

set -euo pipefail

REEL_TESTING_DIR="${REEL_TESTING_DIR:-$HOME/testing}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

MODE="tq"
CRF="28"
LABEL="run"
MATRIX="default"
REEL_BIN=""
OUT_ROOT=""
KEEP_WORKDIRS=0
GPU_INTERVAL="2"
PASSTHROUGH=()
CLIPS=()

DEFAULT_MATRIX=(air-5m im-5m bts-5m sully-5m kbv1-5m sullyhv-15m)
COVERAGE_MATRIX=(air-5m bts-5m im-5m soms-5m io-5m sully-5m kbv1-5m ko-5m sullyhv-15m)
ENCODER_MATRIX=(sully-5m kbv1-5m ko-5m sullyhv-15m)
LONG_MATRIX=(air-20m bts-20m sully-20m ko-20m)

die() { echo "run-suite: $*" >&2; exit 1; }
need() { [[ $# -ge 2 ]] || die "$1 requires a value"; }

# Reap the background GPU sampler on any exit, including Ctrl-C / SIGTERM, so an
# interrupted run never leaves an nvidia-smi loop polling the GPU and skewing a
# later run (the single GPU must host one reel process at a time).
sampler_pid=""
host_sampler_pid=""
cleanup() {
	[[ -n "${sampler_pid:-}" ]] && kill "$sampler_pid" 2>/dev/null || true
	[[ -n "${host_sampler_pid:-}" ]] && kill "$host_sampler_pid" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

while [[ $# -gt 0 ]]; do
	case "$1" in
		--mode) need "$@"; MODE="$2"; shift 2 ;;
		--crf) need "$@"; CRF="$2"; shift 2 ;;
		--label) need "$@"; LABEL="$2"; shift 2 ;;
		--matrix) need "$@"; MATRIX="$2"; shift 2 ;;
		--reel) need "$@"; REEL_BIN="$2"; shift 2 ;;
		--out) need "$@"; OUT_ROOT="$2"; shift 2 ;;
		--keep-workdirs) KEEP_WORKDIRS=1; shift ;;
		--gpu-interval) need "$@"; GPU_INTERVAL="$2"; shift 2 ;;
		--) shift; PASSTHROUGH=("$@"); break ;;
		-h|--help) sed -n '2,40p' "$0"; exit 0 ;;
		-*) die "unknown option: $1" ;;
		*) CLIPS+=("$1"); shift ;;
	esac
done

[[ "$MODE" == "crf" || "$MODE" == "tq" ]] || die "--mode must be crf or tq, got '$MODE'"
[[ "$GPU_INTERVAL" =~ ^[0-9]+([.][0-9]+)?$ ]] || die "--gpu-interval must be numeric, got '$GPU_INTERVAL'"
if [[ ${#CLIPS[@]} -eq 0 ]]; then
	case "$MATRIX" in
		default) CLIPS=("${DEFAULT_MATRIX[@]}") ;;
		coverage) CLIPS=("${COVERAGE_MATRIX[@]}") ;;
		encoder) CLIPS=("${ENCODER_MATRIX[@]}") ;;
		long) CLIPS=("${LONG_MATRIX[@]}") ;;
		*) die "--matrix must be default, coverage, encoder, or long, got '$MATRIX'" ;;
	esac
else
	MATRIX="custom"
fi

if [[ -z "$REEL_BIN" ]]; then
	if [[ -x "$REPO_ROOT/reel" ]]; then
		REEL_BIN="$REPO_ROOT/reel"
	else
		REEL_BIN="reel"
	fi
fi
command -v "$REEL_BIN" >/dev/null 2>&1 || [[ -x "$REEL_BIN" ]] || die "reel binary not found: $REEL_BIN"

[[ -n "$OUT_ROOT" ]] || OUT_ROOT="$REEL_TESTING_DIR/perf-runs"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
RUN_DIR="$OUT_ROOT/${TIMESTAMP}-${LABEL}"
mkdir -p "$RUN_DIR"

RESULTS_TSV="$RUN_DIR/results.tsv"
printf 'clip\tmode\trc\telapsed_s\tout_bytes\tsha256\tgpu_mean\tgpu_p90\tvram_peak_mib\n' > "$RESULTS_TSV"

# Resolve a clip token to a source .mkv path.
resolve_clip() {
	local token="$1"
	if [[ "$token" == *.mkv && -f "$token" ]]; then echo "$token"; return 0; fi
	if [[ "$token" == */* && -f "$token" ]]; then echo "$token"; return 0; fi
	if [[ -f "$REEL_TESTING_DIR/$token.mkv" ]]; then echo "$REEL_TESTING_DIR/$token.mkv"; return 0; fi
	local matches=()
	shopt -s nullglob
	matches=( "$REEL_TESTING_DIR/$token"*.mkv )
	shopt -u nullglob
	if [[ ${#matches[@]} -eq 1 ]]; then echo "${matches[0]}"; return 0; fi
	if [[ ${#matches[@]} -gt 1 ]]; then
		echo "run-suite: clip '$token' is ambiguous under $REEL_TESTING_DIR:" >&2
		printf '  %s\n' "${matches[@]}" >&2
		return 1
	fi
	echo "run-suite: clip '$token' not found under $REEL_TESTING_DIR (see scripts/perf/README.md and clips.tsv)" >&2
	return 1
}

# Summarize a nvidia-smi "util,mem" log into "mean p90 peak_mib".
gpu_summary() {
	local f="$1"
	if [[ ! -s "$f" ]]; then echo "na na na"; return; fi
	local mean peak p90
	mean=$(awk -F',' '{s+=$1; n++} END{if(n)printf "%.1f", s/n; else printf "na"}' "$f")
	peak=$(awk -F',' '{m=$2+0; if(m>pk)pk=m} END{printf "%d", pk}' "$f")
	p90=$(awk -F',' '{print $1+0}' "$f" | sort -n | \
		awk '{a[NR]=$1} END{if(NR==0){print "na"; exit} i=int(0.9*NR); if(i<1)i=1; print a[i]}')
	echo "$mean $p90 $peak"
}

git_commit() { git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown; }
git_dirty() { [[ -n "$(git -C "$REPO_ROOT" status --porcelain 2>/dev/null)" ]] && echo true || echo false; }

reel_sha() {
	local p
	p="$(command -v "$REEL_BIN" 2>/dev/null || echo "$REEL_BIN")"
	sha256sum "$p" 2>/dev/null | cut -d' ' -f1 || echo unknown
}

gpu_name() { nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | head -1 || echo na; }
gpu_driver() { nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -1 || echo na; }

libvship_path() {
	for p in /usr/local/lib/libvship.so /usr/lib/libvship.so /usr/lib64/libvship.so; do
		[[ -e "$p" ]] && { echo "$p"; return; }
	done
	echo na
}

# Written once at start (so an interrupted run still has env), and rewritten at
# the end with the SVT-AV1 version grepped from the first encode log.
write_run_meta() {
	local svt="$1"
	REEL_TESTING_DIR="$REEL_TESTING_DIR" \
	RUN_DIR="$RUN_DIR" TIMESTAMP="$TIMESTAMP" LABEL="$LABEL" MODE="$MODE" CRF="$CRF" \
	MATRIX="$MATRIX" CLIP_TOKENS="$(printf '%s\n' "${CLIPS[@]}")" \
	REEL_BIN_RESOLVED="$(command -v "$REEL_BIN" 2>/dev/null || echo "$REEL_BIN")" \
	REEL_VERSION="$("$REEL_BIN" version 2>/dev/null | head -1 || echo unknown)" \
	REEL_SHA="$(reel_sha)" \
	GIT_COMMIT="$(git_commit)" GIT_DIRTY="$(git_dirty)" \
	SVT_VERSION="$svt" \
	HOSTNAME_VAL="$(hostname 2>/dev/null || echo unknown)" \
	GPU_NAME="$(gpu_name)" GPU_DRIVER="$(gpu_driver)" \
	LIBVSHIP_PATH="$(libvship_path)" \
	PASSTHROUGH="${PASSTHROUGH[*]:-}" \
	python3 - <<'PY' > "$RUN_DIR/run-meta.json"
import json, os
meta = {
    "timestamp": os.environ["TIMESTAMP"],
    "label": os.environ["LABEL"],
    "hostname": os.environ["HOSTNAME_VAL"],
    "mode": os.environ["MODE"],
    "matrix": os.environ["MATRIX"],
    "clips": os.environ["CLIP_TOKENS"].splitlines(),
    "crf": float(os.environ["CRF"]) if os.environ["MODE"] == "crf" else None,
    "reel_binary": os.environ["REEL_BIN_RESOLVED"],
    "reel_version": os.environ["REEL_VERSION"],
    "reel_sha256": os.environ["REEL_SHA"],
    "git_commit": os.environ["GIT_COMMIT"],
    "git_dirty": os.environ["GIT_DIRTY"] == "true",
    "svtav1_version": os.environ["SVT_VERSION"] or None,
    "gpu_name": os.environ["GPU_NAME"],
    "gpu_driver": os.environ["GPU_DRIVER"],
    "libvship_path": os.environ["LIBVSHIP_PATH"],
    "extra_reel_args": os.environ["PASSTHROUGH"],
    "testing_dir": os.environ["REEL_TESTING_DIR"],
}
print(json.dumps(meta, indent=2))
PY
}

echo "run-suite: mode=$MODE clips=${#CLIPS[@]} -> $RUN_DIR"
write_run_meta ""
SVT_VERSION=""

for token in "${CLIPS[@]}"; do
	src="$(resolve_clip "$token")" || { echo "run-suite: skipping '$token'"; continue; }
	stem="$(basename "$src" .mkv)"
	clip_dir="$RUN_DIR/$stem"
	mkdir -p "$clip_dir"
	log="$clip_dir/$stem.log"
	gpulog="$clip_dir/$stem.gpu"

	# Defeat resume traps: clear any stale workdir for this clip.
	rm -rf "$clip_dir/.reel-${stem}-"* 2>/dev/null || true

	encode_args=(encode -i "$src" -o "$clip_dir" --keep-workdir --no-log -v)
	if [[ "$MODE" == "crf" ]]; then
		encode_args+=(--quality-mode crf --crf "$CRF")
	fi
	[[ ${#PASSTHROUGH[@]} -gt 0 ]] && encode_args+=("${PASSTHROUGH[@]}")

	# Start GPU sampler (best-effort; absent nvidia-smi just yields an empty log).
	: > "$gpulog"
	sampler_pid=""
	if command -v nvidia-smi >/dev/null 2>&1; then
		( while true; do
			nvidia-smi --query-gpu=utilization.gpu,memory.used --format=csv,noheader,nounits >> "$gpulog" 2>/dev/null || true
			sleep "$GPU_INTERVAL"
		done ) &
		sampler_pid=$!
	fi

	# Host sampler: "cpu_busy_pct, mem_available_kib, disk_read_bytes, disk_write_bytes"
	# per interval (deltas over whole nvme disks, partitions excluded). Answers
	# whether an encode phase is CPU-saturated, memory-pressured, or IO-bound.
	hostlog="$clip_dir/$stem.host"
	: > "$hostlog"
	host_sampler_pid=""
	( prev_total=0; prev_idle=0; prev_rd=0; prev_wr=0
	  while true; do
		read -r _ user nice system idle iowait irq softirq steal _ < /proc/stat
		total=$((user+nice+system+idle+iowait+irq+softirq+steal))
		idle_all=$((idle+iowait))
		mem_avail_kb=$(awk '/^MemAvailable:/{print $2}' /proc/meminfo)
		rd=0; wr=0
		while read -r _ _ dev _ _ rsec _ _ _ wsec _; do
			case "$dev" in nvme[0-9]n1) rd=$((rd+rsec)); wr=$((wr+wsec));; esac
		done < /proc/diskstats
		if [[ $prev_total -gt 0 ]]; then
			dt=$((total-prev_total)); di=$((idle_all-prev_idle))
			cpu=$(awk -v dt="$dt" -v di="$di" 'BEGIN{if(dt>0)printf "%.1f",100*(dt-di)/dt; else print "na"}')
			echo "$cpu, $mem_avail_kb, $(( (rd-prev_rd)*512 )), $(( (wr-prev_wr)*512 ))" >> "$hostlog"
		fi
		prev_total=$total; prev_idle=$idle_all; prev_rd=$rd; prev_wr=$wr
		sleep "$GPU_INTERVAL"
	  done ) &
	host_sampler_pid=$!

	echo "run-suite: [$stem] encoding..."
	start=$(date +%s)
	set +e
	"$REEL_BIN" "${encode_args[@]}" > "$log" 2>&1
	rc=$?
	set -e
	elapsed=$(( $(date +%s) - start ))
	[[ -n "$sampler_pid" ]] && kill "$sampler_pid" 2>/dev/null || true
	sampler_pid=""
	[[ -n "$host_sampler_pid" ]] && kill "$host_sampler_pid" 2>/dev/null || true
	host_sampler_pid=""

	# Harvest the small JSON artifacts out of the workdir.
	workdir="$(find "$clip_dir" -maxdepth 1 -type d -name ".reel-${stem}-*" | head -1)"
	if [[ -n "$workdir" ]]; then
		[[ -f "$workdir/perf.json" ]] && cp "$workdir/perf.json" "$clip_dir/perf.json"
		[[ -f "$workdir/target-quality.json" ]] && cp "$workdir/target-quality.json" "$clip_dir/target-quality.json"
		[[ "$KEEP_WORKDIRS" -eq 0 ]] && rm -rf "$workdir"
	fi

	out_mkv="$clip_dir/$stem.mkv"
	out_bytes=0; sha="na"
	if [[ -f "$out_mkv" ]]; then
		out_bytes=$(stat -c %s "$out_mkv")
		sha=$(sha256sum "$out_mkv" | cut -d' ' -f1)
	fi
	read -r g_mean g_p90 g_peak <<< "$(gpu_summary "$gpulog")"

	printf '%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\t%s\n' \
		"$stem" "$MODE" "$rc" "$elapsed" "$out_bytes" "$sha" "$g_mean" "$g_p90" "$g_peak" >> "$RESULTS_TSV"

	if [[ -z "$SVT_VERSION" && -f "$log" ]]; then
		SVT_VERSION="$(grep -aoiE 'SVT version:[[:space:]]*[0-9a-f.]+' "$log" | head -1 | awk '{print $NF}' || true)"
	fi
	echo "run-suite: [$stem] rc=$rc elapsed=${elapsed}s size=${out_bytes}B gpu_mean=${g_mean}% vram_peak=${g_peak}MiB"
done

write_run_meta "$SVT_VERSION"
echo "run-suite: done -> $RUN_DIR"
echo "run-suite: analyze with: scripts/perf/analyze.py $RUN_DIR"
