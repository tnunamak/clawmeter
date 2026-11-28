#!/usr/bin/env python3
"""Build the Chrome Web Store upload for the Clawmeter extension.

The store rejects a manifest with a "key" field and assigns its own ID, so the
package drops the key that pins the unpacked extension's ID. Tests and docs
stay out of the package.
"""

import json
import sys
import tempfile
import zipfile
from pathlib import Path

root = Path(__file__).resolve().parent.parent / "extension"
manifest = json.loads((root / "manifest.json").read_text())
manifest.pop("key", None)

files = ["background.js", "console.js", "resets.js", "popup.html", "popup.js", *manifest["icons"].values()]
missing = [name for name in files if not (root / name).is_file()]
if missing:
    sys.exit(f"missing from extension/: {', '.join(missing)}")

out = Path(sys.argv[1] if len(sys.argv) > 1 else f"clawmeter-extension-{manifest['version']}.zip")
# Fixed timestamps keep the archive identical across builds.
stamp = (2026, 1, 1, 0, 0, 0)
def write_entry(archive, name, data):
    entry = zipfile.ZipInfo(name, stamp)
    entry.compress_type = zipfile.ZIP_DEFLATED
    archive.writestr(entry, data)


# Never replace a usable output until the archive has been fully written and closed.
temporary = tempfile.NamedTemporaryFile(dir=out.parent, prefix=f".{out.name}.", suffix=".tmp", delete=False)
staged = Path(temporary.name)
try:
    with temporary, zipfile.ZipFile(temporary, "w") as archive:
        write_entry(archive, "manifest.json", json.dumps(manifest, indent=2) + "\n")
        for name in files:
            write_entry(archive, name, (root / name).read_bytes())
    # NamedTemporaryFile creates 0600; the output gets the mode a plain open() would give it.
    umask = os.umask(0)
    os.umask(umask)
    os.chmod(staged, 0o666 & ~umask)
    staged.replace(out)
finally:
    staged.unlink(missing_ok=True)
print(out)
