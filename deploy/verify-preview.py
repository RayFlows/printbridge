#!/usr/bin/env python3
"""Explicit deployment check: upload synthetic fixtures and preview, never submit jobs."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import struct
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--server", default="http://127.0.0.1:8765")
    parser.add_argument("--env-file", type=Path, default=Path.home() / ".config/printbridge/service.env")
    parser.add_argument("--fixtures", type=Path, required=True)
    parser.add_argument("--state", type=Path, default=Path.home() / ".cache/printbridge/preview-check.json")
    parser.add_argument("--resume", action="store_true", help="Check the saved session and preview after restarting the service")
    office = parser.add_mutually_exclusive_group()
    office.add_argument("--office", action="store_true")
    office.add_argument("--expect-office-unavailable", action="store_true")
    args = parser.parse_args()
    os.umask(0o077)
    origin = args.server.rstrip("/")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    token = None

    def request(method, path, payload=None, content_type="application/json", status=200, authorized=True):
        # This tool cannot call create, continue, cancel or any other print mutation.
        if method == "POST" and path not in {"/api/v1/sessions", "/api/v1/documents", "/api/v1/plans"}:
            raise RuntimeError("Refusing a print mutation")
        headers = {}
        if authorized and token:
            headers["Authorization"] = "Bearer " + token
        if isinstance(payload, dict):
            payload = json.dumps(payload).encode()
        if payload is not None:
            headers["Content-Type"] = content_type
        req = urllib.request.Request(origin + path, data=payload, method=method, headers=headers)
        try:
            response = opener.open(req, timeout=180)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            body = response.read()
            if response.status != status:
                raise RuntimeError(f"{method} {path}: HTTP {response.status}, expected {status}")
            if "application/json" in response.headers.get("Content-Type", ""):
                return json.loads(body)
            return body

    def upload(name, status=201):
        boundary = "printbridge-" + uuid.uuid4().hex
        body = (
            f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{name}"\r\n'
            "Content-Type: application/octet-stream\r\n\r\n"
        ).encode() + (args.fixtures / name).read_bytes() + f"\r\n--{boundary}--\r\n".encode()
        return request("POST", "/api/v1/documents", body, "multipart/form-data; boundary=" + boundary, status)

    def preview(path, name):
        image = request("GET", path)
        if image[:8] != b"\x89PNG\r\n\x1a\n":
            raise RuntimeError("Preview is not a PNG")
        width, height = struct.unpack(">II", image[16:24])
        if not (1 <= width <= 1600 and 1 <= height <= 1600):
            raise RuntimeError("Invalid preview dimensions")
        output = args.state.parent / (name + ".png")
        output.write_bytes(image)
        return hashlib.sha256(image).hexdigest()

    assert request("GET", "/healthz")["status"] == "ok"
    print("Version:", request("GET", "/api/v1/server")["version"])
    assert request("GET", "/api/v1/printers", status=401, authorized=False)["error"]["code"] == "session_expired"
    args.state.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if args.resume:
        state = json.loads(args.state.read_text())
        if state["server"] != origin:
            raise RuntimeError("Saved check belongs to a different server")
        token = state["token"]
        assert request("GET", "/api/v1/documents/" + state["documentId"])["pageCount"] == 3
        assert request("GET", "/api/v1/plans/" + state["planId"])["sheets"] == state["sheets"]
        digest = preview("/api/v1/plans/" + state["planId"] + "/pages/1", "resumed-preview")
        assert digest == state["previewHash"], "Preview changed across restart"
        assert request("GET", "/api/v1/jobs")["jobs"] == []
        print("PASS persisted session, document, plan and identical preview; session has zero jobs")
        return

    values = dict(line.split("=", 1) for line in args.env_file.read_text().splitlines() if "=" in line and not line.startswith("#"))
    token = request("POST", "/api/v1/sessions", {"pairingCode": values["PRINTBRIDGE_PAIRING_CODE"]}, status=201)["token"]
    printers = request("GET", "/api/v1/printers")["printers"]
    assert printers, "No CUPS queues reported"
    printer = printers[0]
    print("Queue:", printer["id"], "; state:", printer["state"])
    settings = {
        "printerId": printer["id"], "media": printer["capabilities"]["defaultMedia"],
        "colorMode": printer["capabilities"]["defaultColorMode"],
        "copies": 1, "pages": "", "subset": "all", "reverse": False, "outputOrder": "reverse",
        "orientation": "portrait", "pagesPerSheet": 1, "scalePercent": 100,
        "sides": "one-sided", "backOrder": "same", "backRotation": 0, "calibrated": False,
        "scaling": "fit",
    }
    fixtures = [("three-pages.pdf", 3), ("sample.jpg", 1), ("sample.png", 1), ("sample.webp", 1),
                ("sample.bmp", 1), ("sample.tiff", 2), ("sample.gif", 1), ("orientation.jpg", 1)]
    if args.office:
        fixtures += [(f"sample.{ext}", pages) for ext, pages in (
            ("docx", 2), ("xlsx", 1), ("pptx", 2), ("doc", 2), ("xls", 1), ("ppt", 2),
            ("odt", 2), ("ods", 1), ("odp", 2), ("rtf", 1), ("txt", 1), ("csv", 1))]
    saved = None
    for name, pages in fixtures:
        start = time.monotonic()
        doc = upload(name)
        assert doc["pageCount"] == pages, name
        preview(f"/api/v1/documents/{doc['id']}/pages/1", name + "-source")
        if pages > 1:
            preview(f"/api/v1/documents/{doc['id']}/pages/{pages}", name + "-last")
        plan = request("POST", "/api/v1/plans", {"documentId": doc["id"], "settings": settings}, status=201)
        digest = preview(f"/api/v1/plans/{plan['id']}/pages/1", name + "-layout")
        assert plan["sheets"][0]["front"] == [pages]
        if name == "three-pages.pdf":
            # This is a preparation-only fixture, not a claim of physical calibration.
            manual = {**settings, "sides": "manual-long-edge", "calibrated": True}
            plan = request("POST", "/api/v1/plans", {
                "documentId": doc["id"], "settings": manual, "replacesPlanId": plan["id"],
            }, status=201)
            assert plan["sheets"] == [{"front": [3], "back": [0]}, {"front": [1], "back": [2]}]
            digest = preview(f"/api/v1/plans/{plan['id']}/pages/1", "manual-reverse")
            saved = {"server": origin, "token": token, "documentId": doc["id"], "planId": plan["id"],
                     "sheets": plan["sheets"], "previewHash": digest}
        print(f"PASS {name}: {pages} pages, source/layout previews, {time.monotonic() - start:.2f}s")
    if args.expect_office_unavailable:
        assert upload("sample.docx", status=503)["error"]["code"] == "office_unavailable"
        print("PASS missing Office converter returns explicit office_unavailable")
    elif not args.office:
        print("SKIP Office conversion: use --office after installing dependencies")
    assert request("GET", "/api/v1/jobs")["jobs"] == []
    args.state.write_text(json.dumps(saved))
    args.state.chmod(0o600)
    print("PASS zero jobs; private restart-check state saved")


if __name__ == "__main__":
    main()
