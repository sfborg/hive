# Minimal OCR sidecar for hive. Wraps ocrmypdf (which itself wraps
# tesseract, unpaper, ghostscript, jbig2enc) in a tiny FastAPI
# HTTP shim so hive can POST a PDF and get back a searchable
# text-layered PDF.
#
# Debian bookworm base — Alpine's musl doesn't have prebuilt
# tesseract wheels for the training-data languages hive might want
# to add later. Slim variant keeps the image ~500 MB (ocrmypdf's
# native deps are the bulk of that; the FastAPI shim is trivial).
FROM python:3.12-slim-bookworm

# ocrmypdf has a lot of native deps. tesseract-ocr-eng is the
# English trained data pack; add other -osd / -*** packs here for
# additional language support (deu, fra, spa, ...).
RUN apt-get update && apt-get install -y --no-install-recommends \
      ocrmypdf \
      tesseract-ocr-eng \
      curl \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir \
      fastapi==0.115.0 \
      "uvicorn[standard]==0.32.0" \
      python-multipart==0.0.12

WORKDIR /app
COPY server.py /app/server.py

EXPOSE 8080

# Uvicorn workers=1 keeps memory predictable; the ocrmypdf CPU
# work is heavy enough that concurrent requests would fight for
# CPU. Scale horizontally with more containers if needed.
CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8080", "--workers", "1"]
