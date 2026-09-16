"""Package upstream notices from Cargo's structured dependency inventory."""
import json
import os
from pathlib import Path
import shutil
import sys

metadata, output, rnnoise = map(Path, sys.argv[1:])
output.mkdir(parents=True, exist_ok=True)
packages = json.loads(metadata.read_text(encoding="utf-8"))["packages"]
for package in packages:
    root = Path(package["manifest_path"]).parent
    # Includes bundled WebRTC/Abseil notices, not just the Rust wrapper license.
    for source in root.rglob("*"):
        if source.is_file() and source.name.upper().startswith(("LICENSE", "COPYING", "PATENTS", "NOTICE")):
            target = output / f'{package["name"]}-{package["version"]}' / source.relative_to(root)
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, target)
for name, root in [("rnnoise", rnnoise), ("speexdsp", Path("internal/audio/speexdsp"))]:
    target = output / name
    target.mkdir(exist_ok=True)
    shutil.copyfile(root / "COPYING", target / "COPYING")
for source in Path(os.environ.get("CARGO_TARGET_DIR", "build/audio3a/target")).glob("release/build/webrtc-audio-processing-sys-*/out/webrtc-audio-processing/subprojects/abseil-cpp-*/LICENSE"):
    shutil.copyfile(source, output / "abseil-LICENSE")
