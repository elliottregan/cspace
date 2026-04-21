#!/usr/bin/env python3
"""Render cluster-coords.tsv as a self-contained Plotly HTML page.

Corpus-agnostic clustering visualizer. Accepts either code or commit clusters.
Usage:
  plot-clusters.py --corpus=code [--in=path] [--out=path]
  plot-clusters.py --corpus=commits [--in=path] [--out=path]
"""
import argparse
import csv
import json
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(
        description="Render cluster coordinates as a Plotly HTML visualization"
    )
    parser.add_argument(
        "--corpus",
        default="code",
        choices=["code", "commits"],
        help="Corpus type: code or commits (default: code)",
    )
    parser.add_argument(
        "--in",
        dest="input_path",
        help="Input TSV path (default: .cspace/{corpus}-cluster-coords.tsv)",
    )
    parser.add_argument(
        "--out",
        dest="output_path",
        help="Output HTML path (default: .cspace/{corpus}-cluster-plot.html)",
    )
    args = parser.parse_args()

    # Derive input/output paths from corpus if not explicitly provided.
    if args.input_path is None:
        args.input_path = f".cspace/{args.corpus}-cluster-coords.tsv"
    if args.output_path is None:
        args.output_path = f".cspace/{args.corpus}-cluster-plot.html"

    src = Path(args.input_path)
    dst = Path(args.output_path)

    # Read TSV and detect schema.
    rows = list(csv.DictReader(src.open(), delimiter="\t"))
    if not rows:
        print(f"No data in {src}")
        return

    # Determine schema: check for "path" (code) vs "hash"+"subject" (commits).
    has_path = "path" in rows[0]
    has_hash = "hash" in rows[0]
    has_subject = "subject" in rows[0]

    by_label: dict[str, list[dict]] = {}
    for r in rows:
        by_label.setdefault(r["label"], []).append(r)

    def sort_key(label: str) -> tuple[int, int]:
        # noise (-1) last, otherwise by cluster size desc
        n = int(label)
        return (1 if n < 0 else 0, -len(by_label[label]))

    traces = []
    for label in sorted(by_label, key=sort_key):
        pts = by_label[label]
        is_noise = int(label) < 0

        # Build hover text based on schema.
        if has_path:
            # Code corpus: show path
            text = [f"<b>{p['path']}</b>" for p in pts]
        elif has_hash and has_subject:
            # Commit corpus: show subject, hash, date
            text = [
                f"<b>{p['subject']}</b><br>{p['hash'][:7]} · {p.get('date', 'unknown')}"
                for p in pts
            ]
        else:
            # Fallback: show id or path if available
            text = [f"<b>{p.get('path', p.get('id', 'unknown'))}</b>" for p in pts]

        traces.append(
            {
                "type": "scattergl",
                "mode": "markers",
                "name": "noise" if is_noise else f"cluster {label} (n={len(pts)})",
                "x": [float(p["x"]) for p in pts],
                "y": [float(p["y"]) for p in pts],
                "text": text,
                "hovertemplate": "%{text}<extra></extra>",
                "marker": {
                    "size": 6 if is_noise else 9,
                    "opacity": 0.35 if is_noise else 0.85,
                    "line": {"width": 0.5, "color": "#222"},
                },
            }
        )

    corpus_label = args.corpus
    title = f"cspace {corpus_label} clusters ({len(rows)} items, {sum(1 for l in by_label if int(l) >= 0)} clusters)"

    layout = {
        "title": title,
        "hovermode": "closest",
        "xaxis": {"title": "dim 1", "zeroline": False},
        "yaxis": {"title": "dim 2", "zeroline": False, "scaleanchor": "x"},
        "plot_bgcolor": "#fafafa",
        "legend": {"itemsizing": "constant"},
        "height": 800,
    }

    html = f"""<!doctype html>
<html><head><meta charset="utf-8"><title>cspace cluster map</title>
<script src="https://cdn.plot.ly/plotly-2.35.2.min.js"></script>
<style>body{{margin:0;font-family:system-ui,sans-serif}}</style>
</head><body>
<div id="plot"></div>
<script>
Plotly.newPlot("plot", {json.dumps(traces)}, {json.dumps(layout)},
  {{responsive: true, displaylogo: false}});
</script></body></html>
"""

    dst.parent.mkdir(parents=True, exist_ok=True)
    dst.write_text(html)
    print(
        f"Wrote {dst} ({dst.stat().st_size:,} bytes, {len(rows)} points, {len(by_label)} groups)"
    )


if __name__ == "__main__":
    main()
