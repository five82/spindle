#!/usr/bin/env python3
"""
Compare two target-quality runs and show per-chunk differences.

Usage:
    python3 scripts/compare-tq.py <old_workdir> <new_workdir> [options]

Options:
    --threshold T    Only show differences where |old_score - new_score| > T
    --show-same      Also show chunks that are identical
    --csv            Output as CSV

Example:
    python3 scripts/compare-tq.py \
        ~/testing/.reel-soms-e0c9b46af397 \
        ~/testing/.reel-soms-e0c9b46af397
"""

import argparse
import csv
import json
import os
import sys


def load_log(path: str) -> dict:
    p = os.path.join(path, "target-quality.json")
    if not os.path.exists(p):
        print(f"Error: {p} not found", file=sys.stderr)
        sys.exit(1)
    with open(p, "r") as f:
        return json.load(f)


def main():
    parser = argparse.ArgumentParser(description="Compare two TQ runs")
    parser.add_argument("old_dir", help="Older work directory")
    parser.add_argument("new_dir", help="Newer work directory")
    parser.add_argument("--threshold", type=float, default=0.0,
                        help="Only show chunks with score delta > threshold")
    parser.add_argument("--show-same", action="store_true",
                        help="Show unchanged chunks too")
    parser.add_argument("--csv", action="store_true",
                        help="Output CSV")
    parser.add_argument("--sort", choices=["idx", "delta"],
                        default="idx", help="Sort output")
    args = parser.parse_args()

    old_log = load_log(args.old_dir)
    new_log = load_log(args.new_dir)

    old_chunks = {c["chunk_idx"]: c for c in old_log.get("chunks", [])}
    new_chunks = {c["chunk_idx"]: c for c in new_log.get("chunks", [])}

    rows = []
    all_keys = sorted(set(old_chunks.keys()) | set(new_chunks.keys()))

    for idx in all_keys:
        old_c = old_chunks.get(idx)
        new_c = new_chunks.get(idx)

        if old_c is None or new_c is None:
            rows.append({
                "idx": idx,
                "old_crf": old_c["final_crf"] if old_c else None,
                "new_crf": new_c["final_crf"] if new_c else None,
                "old_score": old_c["final_score"] if old_c else None,
                "new_score": new_c["final_score"] if new_c else None,
                "old_probes": len(old_c["probes"]) if old_c else None,
                "new_probes": len(new_c["probes"]) if new_c else None,
                "crf_delta": None,
                "score_delta": None,
                "probe_delta": None,
                "status": "missing",
            })
            continue

        old_crf = old_c["final_crf"]
        new_crf = new_c["final_crf"]
        old_score = old_c["final_score"]
        new_score = new_c["final_score"]
        old_probes = len(old_c["probes"])
        new_probes = len(new_c["probes"])

        crf_delta = abs(new_crf - old_crf)
        score_delta = abs(new_score - old_score)
        probe_delta = new_probes - old_probes

        changed = (crf_delta > 0.001 or probe_delta != 0 or score_delta > 0.001)
        status = "changed" if changed else "same"

        rows.append({
            "idx": idx,
            "old_crf": old_crf,
            "new_crf": new_crf,
            "old_score": old_score,
            "new_score": new_score,
            "old_probes": old_probes,
            "new_probes": new_probes,
            "crf_delta": crf_delta,
            "score_delta": score_delta,
            "probe_delta": probe_delta,
            "status": status,
        })

    # Filter
    if not args.show_same:
        rows = [r for r in rows if r["status"] != "same"]

    if args.threshold > 0:
        rows = [r for r in rows if r["score_delta"] is not None and r["score_delta"] > args.threshold]

    # Sort
    if args.sort == "delta":
        rows.sort(key=lambda r: abs(r["score_delta"] or 0), reverse=True)

    # Output
    if args.csv:
        writer = csv.DictWriter(sys.stdout, fieldnames=rows[0].keys() if rows else [])
        writer.writeheader()
        for r in rows:
            writer.writerow(r)
        return

    if not rows:
        print("No differences found.")
        return

    print(f"{'idx':>4} {'old_crf':>7} {'new_crf':>7} {'old_probes':>10} {'new_probes':>10} "
          f"{'old_score':>9} {'new_score':>9} {'score_delta':>11} {'status':>8}")
    print("-" * 90)

    for r in rows:
        if r["status"] == "missing":
            print(f"{r['idx']:04d} {str(r['old_crf']):>7} {str(r['new_crf']):>7} "
                  f"{str(r['old_probes']):>10} {str(r['new_probes']):>10} "
                  f"{'-':>9} {'-':>9} {'-':>11} {'missing':>8}")
            continue
        print(f"{r['idx']:04d} {r['old_crf']:7.2f} {r['new_crf']:7.2f} "
              f"{r['old_probes']:10d} {r['new_probes']:10d} "
              f"{r['old_score']:9.4f} {r['new_score']:9.4f} {r['score_delta']:11.4f} "
              f"{r['status']:>8}")

    # Summary
    changed = [r for r in rows if r["status"] == "changed"]
    same = [r for r in rows if r["status"] == "same"]
    missing = [r for r in rows if r["status"] == "missing"]
    print(f"\nSummary: {len(changed)} changed, {len(same)} same, {len(missing)} missing")

    if changed:
        score_deltas = [r["score_delta"] for r in changed]
        probe_deltas = [r["probe_delta"] for r in changed]
        print(f"Score delta mean: {sum(score_deltas)/len(score_deltas):.4f}")
        print(f"Score delta max:  {max(score_deltas):.4f}")
        print(f"Probe delta sum:  {sum(probe_deltas):+d}")


if __name__ == "__main__":
    main()
