"""Stage shared assets, then build Chinese and English with native Zensical UI."""

import argparse
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parent


def reset_directory(name: str) -> Path:
    target = (ROOT / name).resolve()
    # Recursive cleanup is restricted to these generated directories in website/.
    if target.parent != ROOT or target.name not in {".generated", "site"}:
        raise ValueError(f"Unexpected generated directory: {target}")
    if target.exists():
        shutil.rmtree(target)
    target.mkdir()
    return target


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--prepare-only", action="store_true")
    args = parser.parse_args()

    staged = reset_directory(".generated")
    for language in ("zh", "en"):
        destination = staged / language
        shutil.copytree(ROOT / "content" / language, destination)
        shutil.copytree(ROOT / "assets", destination / "assets")

    if args.prepare_only:
        return

    reset_directory("site")
    for config in ("zensical.toml", "zensical.en.toml"):
        subprocess.run(
            ["zensical", "build", "--strict", "--clean", "--config-file", config],
            cwd=ROOT,
            check=True,
        )
    (ROOT / "site" / ".nojekyll").touch()


if __name__ == "__main__":
    main()
