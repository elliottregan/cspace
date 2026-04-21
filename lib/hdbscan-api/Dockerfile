FROM python:3.12-slim

# HDBSCAN is a C extension; wheels aren't available for every numpy/py combo,
# so build from source. Build tools are removed after install to keep image small.
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential \
 && pip install --no-cache-dir \
      "numpy<2" \
      "scikit-learn>=1.3,<1.6" \
      "cython<3.1" \
      hdbscan==0.8.40 \
      flask==3.1.0 \
 && apt-get purge -y --auto-remove build-essential \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY server.py /app/server.py

EXPOSE 8090
CMD ["python", "/app/server.py"]
