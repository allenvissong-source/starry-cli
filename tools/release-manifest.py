"""Write public build provenance and, on request, a separate local inventory."""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import sys

sys.dont_write_bytecode = True
from release_source import ROOT, revision, snapshot

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("output", type=Path)
parser.add_argument("--internal-output", type=Path, help="write private inventory outside the public output directory")
args = parser.parse_args()
if args.internal_output and args.internal_output.resolve().parent == args.output.resolve().parent:
    parser.error("internal inventory must use a different output directory")

source = snapshot(ROOT)
now = datetime.now(timezone.utc)
manifest = {
    "version": "1.0.0", "releaseId": "v1-" + now.strftime("%Y%m%dT%H%M%SZ"),
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
        "formatHostSources": {
            name: snapshot(ROOT.parent / name)
            for name in ("starry", "starry_character_card_codec", "starry_domain_entities", "starry_prompt_compiler")
        },
        "sdkConsumer": revision(ROOT.parent / "starry-mcp"),
        "contractSource": revision(ROOT.parent / "starry-go-backend"),
    }
    with open(args.internal_output, "x", opener=lambda path, flags: os.open(path, flags, 0o600)) as stream:
        json.dump(inventory, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
args.output.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
