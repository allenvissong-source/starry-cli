"""Write public build provenance and, on request, a separate local inventory."""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import sys

sys.dont_write_bytecode = True
from release_source import ROOT, snapshot

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("output", type=Path)
parser.add_argument("--internal-output", type=Path, help="write private inventory outside the public output directory")
args = parser.parse_args()
if args.internal_output and args.internal_output.resolve().parent == args.output.resolve().parent:
    parser.error("internal inventory must use a different output directory")

source = snapshot(ROOT)
version = (ROOT / "VERSION").read_text().strip()
if not re.fullmatch(r"[1-9][0-9]*\.[0-9]+\.[0-9]+", version):
    parser.error("VERSION must declare a stable semantic version")
now = datetime.now(timezone.utc)
manifest = {
    "version": version, "releaseId": "v" + version,
    "builtAt": now.isoformat(),
    "source": {key: source[key] for key in ("baseCommit", "dirty", "sourceDigest")},
    "defaultEndpoints": {"backendUrl": "https://api.dustarry.com",
                         "publicWebUrl": "https://dustarry.com",
                         "casDirectUploadOrigin": "https://upload.dustarry.com"},
    "skills": ["starry-community", "starry-character", "starry-worldbook", "starry-assets"],
}

if args.internal_output:
    inventory = {
        **manifest,
        "source": source,
    }
    with open(args.internal_output, "x", opener=lambda path, flags: os.open(path, flags, 0o600)) as stream:
        json.dump(inventory, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
args.output.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
