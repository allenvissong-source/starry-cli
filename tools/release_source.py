"""Fingerprint a local working tree for release provenance."""
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parent.parent


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args])


def revision(repo):
    return {
        "baseCommit": git(repo, "rev-parse", "HEAD").decode().strip(),
        "dirty": bool(git(repo, "status", "--porcelain")),
    }


def snapshot(repo):
    files = git(repo, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
    inventory = []
    for name in sorted(set(files.decode().split("\0")) - {""}):
        path = repo / name
        if path.is_file() and path.name != ".DS_Store" and not path.name.startswith("._"):
            inventory.append({"path": name, "sha256": hashlib.sha256(path.read_bytes()).hexdigest()})
    encoded = json.dumps(inventory, sort_keys=True, separators=(",", ":")).encode()
    return {**revision(repo), "sourceDigest": hashlib.sha256(encoded).hexdigest(), "files": inventory}
