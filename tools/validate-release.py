"""Verify archives, provenance, capabilities and all standalone Skill references."""
import hashlib
import json
from pathlib import Path, PurePosixPath
import posixpath
import re
import sys
import tarfile
import zipfile

sys.dont_write_bytecode = True
from release_source import ROOT, snapshot

root = Path(sys.argv[1]).resolve()
source = ROOT
skills = ("starry-community", "starry-character", "starry-worldbook", "starry-assets")
manifest = json.loads((root / "release-manifest.json").read_text())
if set(manifest) != {"version", "releaseId", "builtAt", "source", "defaultEndpoints", "skills"}:
    raise ValueError("unexpected fields in public release manifest")
source_fields = {"baseCommit", "dirty", "sourceDigest"}
if set(manifest["source"]) != source_fields:
    raise ValueError("unexpected fields in public source provenance")
if manifest.get("defaultEndpoints") != {"backendUrl": "https://api.dustarry.com",
                                      "publicWebUrl": "https://dustarry.com",
                                      "casDirectUploadOrigin": "https://upload.dustarry.com"}:
    raise ValueError("release does not declare production endpoints")
checkout = snapshot(source)
if manifest["source"] != {key: checkout[key] for key in source_fields}:
    raise ValueError("source revision or content changed after packaging")
checksums = {}
for line in (root / "SHA256SUMS").read_text().splitlines():
    digest, name = line.split()
    if not re.fullmatch(r"[0-9a-f]{64}", digest) or Path(name).name != name or name in checksums:
        raise ValueError("invalid checksum entry")
    checksums[name] = digest
expected = {f"starry-cli-{os}-{arch}.tar.gz" for os in ("darwin", "linux", "windows") for arch in ("amd64", "arm64")}
if set(checksums) != expected | {"starry-skills-v1.zip"}:
    raise ValueError("missing or extra release artifact")
def validate_skills(files, prefix=""):
    for skill in skills:
        base = prefix + skill + "/"
        text = files[base + "SKILL.md"].decode()
        header = re.match(r"^---\nname: ([a-z0-9-]+)\ndescription: ([^\n]+)\n---\n", text)
        if not header or header[1] != skill or ": " in header[2] or len(text.splitlines()) >= 500:
            raise ValueError("invalid Skill frontmatter or length")
        for name, data in files.items():
            if not name.startswith(base):
                continue
            original = source / "skills" / name.removeprefix(prefix)
            if not original.is_file() or original.read_bytes() != data:
                raise ValueError("packaged Skill differs from source: " + name)
            if name.endswith(".md"):
                for target in re.findall(r"\[[^\]]+\]\(([^)]+)\)", data.decode()):
                    if "://" not in target:
                        resolved = posixpath.normpath(posixpath.join(posixpath.dirname(name), target.split("#")[0]))
                        if not resolved.startswith(base) or resolved not in files:
                            raise ValueError("Skill reference is not standalone: " + name + " -> " + target)
        actual = {name.removeprefix(base) for name in files if name.startswith(base)}
        wanted = {p.relative_to(source / "skills" / skill).as_posix() for p in (source / "skills" / skill).rglob("*")
                  if p.is_file() and p.name != ".DS_Store" and not p.name.startswith("._")}
        if actual != wanted:
            raise ValueError("incomplete Skill inventory")
for name, digest in checksums.items():
    package = root / name
    if hashlib.sha256(package.read_bytes()).hexdigest() != digest:
        raise ValueError("checksum mismatch")
    if name.endswith(".zip"):
        with zipfile.ZipFile(package) as archive:
            names = archive.namelist()
            if len(names) != len(set(names)):
                raise ValueError("duplicate ZIP path")
            for item in names:
                path = PurePosixPath(item)
                if path.is_absolute() or ".." in path.parts or path.parts[0] not in skills:
                    raise ValueError("unsafe ZIP path")
            validate_skills({item: archive.read(item) for item in names})
        continue
    prefix = name.removesuffix(".tar.gz")
    with tarfile.open(package) as archive:
        members = archive.getmembers()
        names = {member.name.rstrip("/") for member in members}
        if len(names) != len(members):
            raise ValueError("duplicate archive path")
        for member in members:
            path = PurePosixPath(member.name)
            if path.is_absolute() or ".." in path.parts or path.parts[0] != prefix or not (member.isfile() or member.isdir()):
                raise ValueError("unsafe archive member")
            if path.name == ".DS_Store" or path.name.startswith("._"):
                raise ValueError("system metadata in release")
        binary = "starry-cli.exe" if "-windows-" in name else "starry-cli"
        if prefix + "/" + binary not in names:
            raise ValueError("missing CLI")
        capabilities = json.loads(archive.extractfile(prefix + "/file-capabilities.json").read())
        if capabilities["fileImportExport"] != capabilities["imagePreparation"] or capabilities["mediaUpload"] != capabilities["imagePreparation"]:
            raise ValueError("inconsistent image and format capabilities")
        if capabilities["fileImportExport"] and prefix + "/" + capabilities["host"] not in names:
            raise ValueError("capability host missing")
        if json.loads(archive.extractfile(prefix + "/release-manifest.json").read()) != manifest:
            raise ValueError("embedded release provenance differs")
        files = {m.name: archive.extractfile(m).read() for m in members if m.isfile() and any(m.name.startswith(prefix + "/" + s + "/") for s in skills)}
        validate_skills(files, prefix + "/")
        print(json.dumps({"package": name, "skills": list(skills), "fileImportExport": capabilities["fileImportExport"], "sha256": digest}))
print("Six CLI packages and standalone Skills ZIP verified against exact source.")
