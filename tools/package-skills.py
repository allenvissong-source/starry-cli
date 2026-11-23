"""Archive complete, independently installable Skill folders."""
from pathlib import Path
import sys
import zipfile

root = Path(__file__).resolve().parent.parent / "skills"
with zipfile.ZipFile(sys.argv[1], "w", compression=zipfile.ZIP_DEFLATED) as archive:
    for skill in ("starry-community", "starry-character", "starry-worldbook", "starry-assets"):
        for path in sorted((root / skill).rglob("*")):
            if path.is_file() and path.name != ".DS_Store" and not path.name.startswith("._"):
                archive.write(path, path.relative_to(root).as_posix())
