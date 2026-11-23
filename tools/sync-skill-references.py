"""Materialize common references so every distributed Skill works alone."""
from pathlib import Path
import sys

root = Path(__file__).resolve().parent.parent
skills = ("starry-community", "starry-character", "starry-worldbook", "starry-assets")
sources = {root / f"skills/{skill}/references/workflows.md": root / "FILES.md" for skill in skills}
sources.update({root / f"skills/{skill}/references/resources.md": root / "skills/starry-assets/references/commands.md" for skill in skills[:-1]})
for target, source in sources.items():
    data = source.read_bytes()
    if "--check" in sys.argv:
        if not target.is_file() or target.read_bytes() != data:
            raise ValueError(f"stale Skill reference: {target.relative_to(root)}")
    else:
        target.write_bytes(data)
print("Shared references verified." if "--check" in sys.argv else "Shared references synchronized.")
