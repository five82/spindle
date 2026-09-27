#!/usr/bin/env python3
"""
Compare two perf run directories produced by run-suite.sh, clip by clip.

This is a run-level A/B (wall, size, probes, JOD, encode-vs-metric seconds, key
phase timings). For a per-chunk diff of a single clip's target-quality run, use
scripts/compare-tq.py instead.

Usage:
    scripts/perf/compare-runs.py <run_dir_A> <run_dir_B>

A is the baseline; deltas are B - A (so negative wall/size is B faster/smaller).
"""

import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import analyze  # noqa: E402


def index_by_clip(summaries):
    return {s["clip"]: s for s in summaries}


def pct(a, b):
    if isinstance(a, (int, float)) and isinstance(b, (int, float)) and a:
        return 100.0 * (b - a) / a
    return None


def fmt(v, spec):
    if isinstance(v, (int, float)):
        return format(v, spec)
    m = re.match(r"[+\-]?0?(\d+)", spec)
    width = int(m.group(1)) if m else 2
    return format("na", f">{width}")


def main():
    if len(sys.argv) != 3:
        print(__doc__)
        sys.exit(2)
    dir_a, dir_b = sys.argv[1], sys.argv[2]
    for d in (dir_a, dir_b):
        if not os.path.isdir(d):
            print(f"compare-runs: not a directory: {d}", file=sys.stderr)
            sys.exit(1)

    a = index_by_clip(analyze.summarize_run(dir_a))
    b = index_by_clip(analyze.summarize_run(dir_b))
    clips = sorted(set(a) | set(b))

    print(f"A = {dir_a}")
    print(f"B = {dir_b}")
    print("deltas are B - A (wall%/size% relative to A)\n")
    hdr = ["clip", "wallA", "wallB", "wall%", "sizeA_MB", "sizeB_MB", "size%",
           "p/chA", "p/chB", "jodA", "jodB", "maeA", "maeB"]
    print(f"{hdr[0]:<26} {hdr[1]:>6} {hdr[2]:>6} {hdr[3]:>6} {hdr[4]:>8} {hdr[5]:>8} "
          f"{hdr[6]:>6} {hdr[7]:>6} {hdr[8]:>6} {hdr[9]:>7} {hdr[10]:>7} {hdr[11]:>6} {hdr[12]:>6}")
    print("-" * 122)

    for clip in clips:
        sa = a.get(clip, {})
        sb = b.get(clip, {})
        if clip not in a:
            print(f"{clip:<26} {'(only in B)':>60}")
            continue
        if clip not in b:
            print(f"{clip:<26} {'(only in A)':>60}")
            continue
        mba = (sa.get("out_bytes") or 0) / 1e6
        mbb = (sb.get("out_bytes") or 0) / 1e6
        print(f"{clip:<26} "
              f"{fmt(sa.get('wall_s'), '6.0f')} "
              f"{fmt(sb.get('wall_s'), '6.0f')} "
              f"{fmt(pct(sa.get('wall_s'), sb.get('wall_s')), '+6.1f')} "
              f"{mba:8.1f} {mbb:8.1f} "
              f"{fmt(pct(mba, mbb), '+6.1f')} "
              f"{fmt(sa.get('probes_per_chunk'), '6.2f')} "
              f"{fmt(sb.get('probes_per_chunk'), '6.2f')} "
              f"{fmt(sa.get('jod_mean'), '7.4f')} "
              f"{fmt(sb.get('jod_mean'), '7.4f')} "
              f"{fmt(sa.get('jod_mae'), '6.4f')} "
              f"{fmt(sb.get('jod_mae'), '6.4f')}")

    # Aggregate wall/size across clips present in both.
    both = [c for c in clips if c in a and c in b]
    wa = sum(a[c].get("wall_s") or 0 for c in both)
    wb = sum(b[c].get("wall_s") or 0 for c in both)
    za = sum(a[c].get("out_bytes") or 0 for c in both)
    zb = sum(b[c].get("out_bytes") or 0 for c in both)
    print("-" * 122)
    print(f"{'TOTAL ('+str(len(both))+' clips)':<26} "
          f"{wa:6.0f} {wb:6.0f} {fmt(pct(wa, wb), '+6.1f')} "
          f"{za/1e6:8.1f} {zb/1e6:8.1f} {fmt(pct(za, zb), '+6.1f')}")


if __name__ == "__main__":
    main()
