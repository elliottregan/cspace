"""HDBSCAN clustering service for 2D points.

POST /cluster
  { "points": [[x, y], ...], "min_cluster_size": 3, "min_samples": 1 }
→ { "labels": [0, 1, -1, 2, ...] }

Expects points already reduced to 2D (by a separate reducer like reduce-api).
Runs HDBSCAN with Euclidean distance on the 2D plane.
"""

import numpy as np
import hdbscan
from flask import Flask, request, jsonify

app = Flask(__name__)


@app.get("/health")
def health():
    return {"status": "ok"}


@app.post("/cluster")
def cluster():
    payload = request.get_json(force=True)
    points = np.asarray(payload["points"], dtype=np.float32)
    min_cluster_size = int(payload.get("min_cluster_size", 3))
    min_samples = int(payload.get("min_samples", 1))

    clusterer = hdbscan.HDBSCAN(
        min_cluster_size=min_cluster_size,
        min_samples=min_samples,
        metric="euclidean",
    )
    labels = clusterer.fit_predict(points)
    return jsonify({"labels": [int(l) for l in labels]})


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=8090, threaded=False)
