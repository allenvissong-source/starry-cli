"""Validate release contents, checksums and independently installable Skills."""
import hashlib
import json
from pathlib import Path
import re
import sys
import tarfile

root = Path(sys.argv[1]).resolve()
skills = ("starry-community", "starry-character", "starry-worldbook")
checksums = {}
for line in (root / "SHA256SUMS").read_text().splitlines():
    digest, name = line.split()
    if not re.fullmatch(r"[0-9a-f]{64}", digest) or Path(name).name != name:
        raise ValueError("invalid checksum entry")
    checksums[name] = digest
expected = {f"starry-cli-{os}-{arch}.tar.gz" for os in ("darwin", "linux", "windows") for arch in ("amd64", "arm64")}
if set(checksums) != expected:
    raise ValueError("missing or extra target")
for name, digest in checksums.items():
    package = root / name
    if hashlib.sha256(package.read_bytes()).hexdigest() != digest:
        raise ValueError("checksum mismatch")
    prefix = name.removesuffix(".tar.gz")
    with tarfile.open(package) as archive:
        members = archive.getmembers()
        names = {member.name.rstrip("/") for member in members}
        if len(names) != len(members):
            raise ValueError("duplicate archive path")
        for member in members:
            path = Path(member.name)
            if path.is_absolute() or ".." in path.parts or path.parts[0] != prefix or member.issym() or member.islnk():
                raise ValueError("unsafe archive member")
            if path.name == ".DS_Store" or path.name.startswith("._"):
                raise ValueError("system metadata in release")
        binary = "starry-cli.exe" if "-windows-" in name else "starry-cli"
        if prefix + "/" + binary not in names:
            raise ValueError("missing CLI")
        capabilities = json.loads(archive.extractfile(prefix + "/file-capabilities.json").read())
        if capabilities["fileImportExport"] and prefix + "/" + capabilities["host"] not in names:
            raise ValueError("capability host missing")
        for skill in skills:
            path = prefix + "/" + skill + "/SKILL.md"
            text = archive.extractfile(path).read().decode()
            header = re.match(r"^---\nname: ([a-z0-9-]+)\ndescription: ([^\n]+)\n---\n", text)
            if not header or header[1] != skill or ": " in header[2]:
                raise ValueError("invalid Skill frontmatter")
            if len(text.splitlines()) >= 500:
                raise ValueError("Skill too long")
            for target in re.findall(r"\[[^\]]+\]\(([^)]+)\)", text):
                if "://" not in target and prefix + "/" + skill + "/" + target not in names:
                    raise ValueError("missing Skill reference")
        print(json.dumps({"package": name, "skills": list(skills), "fileImportExport": capabilities["fileImportExport"], "sha256": digest}))
print("Six release packages verified.")
