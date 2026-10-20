#!/usr/bin/env python3
"""Build the Chrome Web Store upload for the Clawmeter extension.

The store rejects a manifest with a "key" field and assigns its own ID, so the
package drops the key that pins the unpacked extension's ID. Tests and docs
stay out of the package.
"""

import json
import sys
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
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as archive:
    archive.writestr(zipfile.ZipInfo("manifest.json", stamp), json.dumps(manifest, indent=2) + "\n")
    for name in files:
        archive.writestr(zipfile.ZipInfo(name, stamp), (root / name).read_bytes())
print(out)
