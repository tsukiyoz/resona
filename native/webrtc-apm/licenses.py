"""Collect the native audio dependency notices into a shipped package."""

import json
import subprocess
import sys
from pathlib import Path
from shutil import copy2


def main() -> None:
    manifest, output, target = map(Path, sys.argv[1:])
    metadata = json.loads(
        subprocess.check_output(
            ["cargo", "metadata", "--locked", "--offline", "--format-version", "1", "--manifest-path", str(manifest)]
        )
    )
    output.mkdir(parents=True, exist_ok=True)
    for package in metadata["packages"]:
        source = Path(package["manifest_path"]).parent
        for file in source.iterdir():
            if file.is_file() and file.suffix != ".py" and file.name.upper().startswith(("LICENSE", "COPYING", "PATENTS", "NOTICE")):
                dest = output / f"{package['name']}-{package['version']}"
                dest.mkdir(exist_ok=True)
                copy2(file, dest / file.name)
        if package["name"] == "webrtc-audio-processing-sys":
            for file in (source / "webrtc-audio-processing").rglob("*"):
                if file.is_file() and file.name.upper().startswith(("LICENSE", "COPYING", "PATENTS", "NOTICE")):
                    dest = output / "webrtc-audio-processing" / file.relative_to(source / "webrtc-audio-processing").parent
                    dest.mkdir(parents=True, exist_ok=True)
                    copy2(file, dest / file.name)
    for file in target.glob("release/build/webrtc-audio-processing-sys-*/out/webrtc-audio-processing/subprojects/abseil-cpp-*/LICENSE"):
        copy2(file, output / "abseil-cpp-LICENSE")


if __name__ == "__main__":
    main()
