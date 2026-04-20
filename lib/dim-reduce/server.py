"""Dim-reduce + HDBSCAN service.

POST /cluster
  { "vectors": [[...], ...], "min_cluster_size": 3, "min_samples": 1,
    "n_neighbors": 15, "min_dist": 0.0 }
→ { "labels": [0, 1, -1, 2, ...], "coords": [[x, y], ...] }

Reduces high-dim vectors to 2D via UMAP, then clusters in 2D via HDBSCAN.
The 2D coords are returned so the caller can plot/debug.
"""

import numpy as np
import umap
import hdbscan
from flask import Flask, request, jsonify

app = Flask(__name__)


@app.get("/health")
def health():
    return {"status": "ok"}


@app.post("/cluster")
def cluster():
    payload = request.get_json(force=True)
    vectors = np.asarray(payload["vectors"], dtype=np.float32)

    n_neighbors = int(payload.get("n_neighbors", 15))
    min_dist = float(payload.get("min_dist", 0.0))
    min_cluster_size = int(payload.get("min_cluster_size", 3))
    min_samples = int(payload.get("min_samples", 1))

    # UMAP to 2D using cosine metric (embeddings are L2-normalized-ish).
    reducer = umap.UMAP(
        n_components=2,
        n_neighbors=min(n_neighbors, max(2, len(vectors) - 1)),
        min_dist=min_dist,
        metric="cosine",
        random_state=42,
    )
    coords = reducer.fit_transform(vectors)

    # HDBSCAN on the 2D projection.
    clusterer = hdbscan.HDBSCAN(
        min_cluster_size=min_cluster_size,
        min_samples=min_samples,
        metric="euclidean",
    )
    labels = clusterer.fit_predict(coords)

    return jsonify({
        "labels": [int(l) for l in labels],
        "coords": [[float(x), float(y)] for x, y in coords],
    })


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=8090, threaded=False)
