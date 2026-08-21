"""
HTTP wrapper around ocrmypdf. Two endpoints:

    GET  /api/isalive  -> 200 {"ok": true}       (healthcheck)
    POST /ocr          -> multipart file=<pdf>   (returns OCR'd PDF)

The /ocr endpoint runs ocrmypdf on the uploaded PDF and streams
back a text-layered searchable PDF. Hive calls this before GROBID
whenever a PDF's initial GROBID pass produces empty body content,
indicating the PDF is image-only (a scanned paper).

Flags used:
    --skip-text  Preserve any real text layer that's already there;
                 only add OCR for pages that lack one. Safer than
                 --force-ocr, which would erase existing text.
    --optimize 0 Skip the post-OCR PDF optimization pass (jbig2enc,
                 pngquant). Trades slightly larger output for a
                 substantial speedup on our synchronous pipeline.
    --quiet      Silence progress bars in the container logs.

The container listens on 0.0.0.0:8080; docker-compose binds it to
loopback only on the host side.
"""

import os
import subprocess
import tempfile

from fastapi import FastAPI, UploadFile, HTTPException
from fastapi.responses import Response, JSONResponse

app = FastAPI(title="hive-ocr", version="0.1")


@app.get("/api/isalive")
def isalive() -> JSONResponse:
    return JSONResponse({"ok": True})


@app.post("/ocr")
async def ocr(file: UploadFile) -> Response:
    if file.content_type not in ("application/pdf", None, "", "application/octet-stream"):
        raise HTTPException(415, detail=f"unsupported content-type {file.content_type!r}")
    with tempfile.TemporaryDirectory(prefix="hive-ocr-") as td:
        in_path = os.path.join(td, "in.pdf")
        out_path = os.path.join(td, "out.pdf")
        with open(in_path, "wb") as f:
            while chunk := await file.read(1 << 20):
                f.write(chunk)
        try:
            subprocess.run(
                [
                    "ocrmypdf",
                    "--skip-text",
                    "--optimize", "0",
                    "--quiet",
                    in_path,
                    out_path,
                ],
                check=True,
                capture_output=True,
                timeout=600,
            )
        except subprocess.CalledProcessError as e:
            # ocrmypdf writes diagnostics on stderr; trim to a
            # sensible size for the HTTP error body.
            detail = (e.stderr or b"").decode("utf-8", "replace")[:1024]
            raise HTTPException(500, detail=f"ocrmypdf failed: {detail}")
        except subprocess.TimeoutExpired:
            raise HTTPException(504, detail="ocrmypdf exceeded 10-minute timeout")
        with open(out_path, "rb") as f:
            data = f.read()
    return Response(content=data, media_type="application/pdf")
