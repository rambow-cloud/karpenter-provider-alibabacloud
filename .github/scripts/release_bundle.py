"""Build an explicit candidate bundle; publishing is a separate opt-in operation."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import tarfile
import tempfile


ROOT = Path(__file__).resolve().parents[2]
SOURCE = "https://github.com/rambow-cloud/karpenter-provider-alibabacloud"
PLATFORMS = {"amd64": 62, "arm64": 183}
DIGEST = re.compile(r"sha256:[0-9a-f]{64}")
REPOSITORY = re.compile(r"[a-z0-9.-]+(?::[0-9]+)?/[a-z0-9._/-]+")


def valid_repository(value):
    return bool(REPOSITORY.fullmatch(value)) and all(part not in ("", ".", "..") for part in value.split("/"))


def ko_layout_path(path):
    # ko also parses its local layout destination as an image reference. Windows
    # drive letters/backslashes cannot be used there; use a POSIX relative path.
    value = Path(os.path.relpath(path, ROOT)).as_posix()
    if not re.fullmatch(r"[a-z0-9_./-]+", value):
        raise ValueError("Use a lowercase workspace-relative bundle output path")
    return value


def call(*args, env=None):
    return subprocess.run(args, cwd=ROOT, env=env, check=True, text=True,
                          stdout=subprocess.PIPE).stdout.strip()


def checksum(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def replace_one(text, pattern, replacement):
    text, count = re.subn(pattern, lambda _: replacement, text, flags=re.M)
    if count != 1:
        raise ValueError(f"Expected exactly one field matching {pattern}")
    return text


def stage_chart(destination, sha, repository, digest):
    shutil.copytree(ROOT / "charts/karpenter", destination)
    chart_file = destination / "Chart.yaml"
    text = chart_file.read_text(encoding="utf-8")
    base = re.search(r"^version: ([0-9]+\.[0-9]+\.[0-9]+)(?:[-+].*)?$", text, re.M)
    if not base:
        raise ValueError("Chart must have a SemVer base version")
    version = base[1] + "-dev.sha-" + sha
    text = replace_one(text, r"^version: .+$", "version: " + version)
    text = replace_one(text, r"^appVersion: .+$", 'appVersion: "' + sha + '"')
    # Fork metadata belongs to the packaged fork, without rewriting source defaults.
    text = replace_one(text, r"^home: .+$", "home: " + SOURCE)
    text = replace_one(text, r"^sources:\n(?:  .+\n)+", "sources:\n  - " + SOURCE + "\n")
    text = replace_one(text, r"^maintainers:\n(?:  .+\n?)+", "maintainers:\n  - name: rambow-cloud\n    url: https://github.com/rambow-cloud\n")
    text += 'annotations:\n  org.opencontainers.image.revision: "' + sha + '"\n'
    chart_file.write_text(text, encoding="utf-8", newline="\n")
    values_file = destination / "values.yaml"
    values = values_file.read_text(encoding="utf-8")
    values = replace_one(values, r"^    repository: .+$", "    repository: " + repository)
    values = replace_one(values, r"^    tag: .*$", '    tag: "' + sha + "@" + digest + '"')
    values_file.write_text(values, encoding="utf-8", newline="\n")
    return version


def read_blob(layout, descriptor):
    digest = descriptor.get("digest", "")
    if not DIGEST.fullmatch(digest):
        raise ValueError("Invalid OCI digest")
    path = layout / "blobs/sha256" / digest.split(":")[1]
    if path.is_symlink() or checksum(path) != digest.split(":")[1] or path.stat().st_size != descriptor["size"]:
        raise ValueError("OCI blob integrity failed")
    return path


def binary_evidence(path, arch, sha, go):
    data = path.read_bytes()
    if data[:6] != b"\x7fELF\x02\x01" or struct.unpack_from("<H", data, 18)[0] != PLATFORMS[arch]:
        raise ValueError("Expected a 64-bit Linux ELF for " + arch)
    phoff = struct.unpack_from("<Q", data, 32)[0]
    phentsize, phnum = struct.unpack_from("<HH", data, 54)
    if any(struct.unpack_from("<I", data, phoff + i * phentsize)[0] in (2, 3) for i in range(phnum)):
        raise ValueError("Controller must have no dynamic interpreter or dependencies")
    info = call(go, "version", "-m", str(path))
    if any(value not in info for value in ("CGO_ENABLED=0", "vcs.revision=" + sha, "vcs.modified=false")):
        raise ValueError("Binary must be CGO-free and built from the clean candidate SHA")
    return {"sha256": checksum(path), "build_info": info.splitlines()[1:]}


def inspect_layout(layout, sha, go):
    index = json.loads((layout / "index.json").read_text())
    if len(index["manifests"]) != 1:
        raise ValueError("Expected one controller image index")
    root = index["manifests"][0]
    manifests = json.loads(read_blob(layout, root).read_text())["manifests"]
    evidence = {}
    for descriptor in manifests:
        platform = descriptor["platform"]
        arch = platform["architecture"]
        if platform["os"] != "linux" or arch not in PLATFORMS or arch in evidence:
            raise ValueError("Unexpected or duplicate controller platform")
        manifest = json.loads(read_blob(layout, descriptor).read_text())
        config = json.loads(read_blob(layout, manifest["config"]).read_text())
        runtime = config["config"]
        if runtime.get("User") not in ("65532", "65532:65532") or runtime.get("Labels", {}).get("org.opencontainers.image.revision") != sha:
            raise ValueError("Controller must run as nonroot with the candidate revision")
        entrypoint = runtime["Entrypoint"]
        if len(entrypoint) != 1 or not entrypoint[0].startswith("/ko-app/"):
            raise ValueError("Unexpected controller entrypoint")
        found = None
        with tempfile.TemporaryDirectory(prefix="ack-controller-elf-") as directory:
            for layer in manifest["layers"]:
                path = read_blob(layout, layer)
                with tarfile.open(path) as archive:
                    for member in archive:
                        if member.name.lstrip("./") == entrypoint[0].lstrip("/"):
                            if not member.isfile():
                                raise ValueError("Controller entrypoint is not a regular file")
                            target = Path(directory) / "controller"
                            with archive.extractfile(member) as source, target.open("wb") as output:
                                shutil.copyfileobj(source, output)
                            found = binary_evidence(target, arch, sha, go)
            if found is None:
                raise ValueError("Controller entrypoint missing from image layers")
        evidence[arch] = {"digest": descriptor["digest"], "entrypoint": entrypoint[0], **found}
    if set(evidence) != set(PLATFORMS):
        raise ValueError("Both amd64 and arm64 are required")
    return root["digest"], evidence


def build(args):
    if not valid_repository(args.repository) or not valid_repository(args.chart_repository):
        raise ValueError("Use an OCI repository path without a tag, digest or scheme")
    if call("git", "status", "--porcelain"):
        raise ValueError("Commit the candidate and use a clean checkout before packaging")
    sha = call("git", "rev-parse", "HEAD")
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("Expected full Git SHA")
    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        raise ValueError("Bundle output directory must be empty")
    env = {**os.environ, "CGO_ENABLED": "0", "KO_DOCKER_REPO": args.repository,
           "KO_CONFIG_PATH": str(ROOT / ".github/release.ko.yaml"), "KO_GO_PATH": args.go,
           "SOURCE_DATE_EPOCH": call("git", "show", "-s", "--format=%ct", "HEAD")}
    layout = output / "image-layout"
    call(args.ko, "build", "./cmd/controller", "--bare", "--platform=linux/amd64,linux/arm64",
         "--push=false", "--tags=" + sha, "--image-user=65532:65532", "--jobs=1",
         "--oci-layout-path=" + ko_layout_path(layout), "--sbom-dir=" + str(output / "sbom"),
         "--image-label=org.opencontainers.image.source=" + SOURCE,
         "--image-label=org.opencontainers.image.revision=" + sha, env=env)
    digest, platforms = inspect_layout(layout, sha, args.go)
    archive_path = output / "controller.oci.tar"
    with tarfile.open(archive_path, "w") as archive:
        archive.add(layout, arcname=".")
    with tempfile.TemporaryDirectory(prefix="ack-chart-") as directory:
        staged = Path(directory) / "karpenter"
        version = stage_chart(staged, sha, args.repository, digest)
        call(args.helm, "lint", str(staged), "--kube-version=1.35.0")
        call(args.helm, "package", str(staged), "--destination", str(output))
    chart = output / ("karpenter-" + version + ".tgz")
    rendered = call(args.helm, "template", "karpenter", str(chart), "--namespace=karpenter", "--kube-version=1.35.0", "--include-crds")
    image = args.repository + ":" + sha + "@" + digest
    if 'image: "' + image + '"' not in rendered:
        raise ValueError("Packaged chart does not reference the built immutable image")
    crds = {path.name: checksum(path) for path in sorted((ROOT / "charts/karpenter/crds").glob("*.yaml"))}
    manifest = {"schema": 1, "source": SOURCE, "git_sha": sha, "chart_version": version,
                "image": image, "image_digest": digest, "image_repository": args.repository,
                "chart_repository": args.chart_repository, "chart": chart.name,
                "platforms": platforms, "crds": crds, "published": False,
                "files": {chart.name: checksum(chart), archive_path.name: checksum(archive_path)}}
    (output / "release.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    (output / "ack-image.yaml").write_text("# Merge into controller_rrsa; this bundle must be published before ACK can pull it.\ncontroller_rrsa:\n  image_repository: " + args.repository + '\n  image_tag: "' + sha + "@" + digest + '"\n', encoding="utf-8")
    write_checksums(output)
    print(json.dumps({"git_sha": sha, "image": image, "chart": str(chart), "published": False}))


def write_checksums(output):
    paths = [*output.glob("*.tgz"), output / "controller.oci.tar", output / "release.json", output / "ack-image.yaml"]
    (output / "SHA256SUMS").write_text("".join(checksum(path) + "  " + path.name + "\n" for path in paths), encoding="utf-8")


def existing_digest(crane, target):
    result = subprocess.run([crane, "digest", target], cwd=ROOT, capture_output=True, text=True)
    if result.returncode == 0:
        digest = result.stdout.strip()
        if not DIGEST.fullmatch(digest):
            raise ValueError("Registry returned an invalid digest")
        return digest
    if not any(code in result.stderr for code in ("MANIFEST_UNKNOWN", "NAME_UNKNOWN")):
        raise ValueError("Registry existence check failed; fix registry authorization or availability")
    return None


def publish(args):
    output = Path(args.output).resolve()
    manifest = json.loads((output / "release.json").read_text())
    sha = manifest["git_sha"]
    if manifest["schema"] != 1 or not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("Invalid bundle manifest")
    for name, expected in manifest["files"].items():
        if Path(name).name != name or checksum(output / name) != expected:
            raise ValueError("Bundle checksum mismatch")
    repository, chart_repository = manifest["image_repository"], manifest["chart_repository"]
    if not valid_repository(repository) or not valid_repository(chart_repository):
        raise ValueError("Invalid destination repository")
    digest, _ = inspect_layout(output / "image-layout", sha, args.go)
    if digest != manifest["image_digest"]:
        raise ValueError("Image layout does not match bundle")
    image_target = repository + ":" + sha
    chart_target = chart_repository + "/karpenter:" + manifest["chart_version"]
    # Validate both destinations before mutation. Resume only identical artifacts;
    # an existing tag never authorizes replacing a different candidate.
    image_existing = existing_digest(args.crane, image_target)
    chart_existing = existing_digest(args.crane, chart_target)
    if image_existing and image_existing != digest:
        raise ValueError("A different controller already uses this candidate tag")
    if chart_existing:
        chart_manifest = json.loads(call(args.crane, "manifest", chart_target))
        layers = chart_manifest.get("layers", [])
        chart_layers = [layer for layer in layers if layer.get("mediaType") == "application/vnd.cncf.helm.chart.content.v1.tar+gzip"]
        if len(chart_layers) != 1 or chart_layers[0]["digest"] != "sha256:" + manifest["files"][manifest["chart"]]:
            raise ValueError("A different chart already uses this candidate version")
    if not image_existing:
        call(args.crane, "push", "--index", str(output / "image-layout"), image_target)
    if call(args.crane, "digest", image_target) != digest:
        raise ValueError("Published controller digest mismatch")
    if not chart_existing:
        call(args.helm, "push", str(output / manifest["chart"]), "oci://" + chart_repository)
    # Helm may write push metadata to stderr; query the registry after pushing.
    manifest["chart_digest"] = call(args.crane, "digest", chart_target)
    if not DIGEST.fullmatch(manifest["chart_digest"]):
        raise ValueError("Published chart returned an invalid digest")
    manifest["published"] = True
    (output / "release.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    write_checksums(output)
    print(json.dumps({"image": manifest["image"], "chart": "oci://" + chart_repository + "/karpenter", "chart_version": manifest["chart_version"], "published": True}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stage", choices=("build", "publish"))
    parser.add_argument("--output", required=True)
    parser.add_argument("--repository", default="ghcr.io/rambow-cloud/karpenter-provider-alibabacloud")
    parser.add_argument("--chart-repository", default="ghcr.io/rambow-cloud/charts")
    parser.add_argument("--go", default="go")
    parser.add_argument("--ko", default="ko")
    parser.add_argument("--helm", default="helm")
    parser.add_argument("--crane", default="crane")
    args = parser.parse_args()
    (build if args.stage == "build" else publish)(args)


if __name__ == "__main__":
    main()
