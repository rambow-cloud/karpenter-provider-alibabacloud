# Candidate image and Helm bundles

Issue #93 supplies the deployable artifacts needed by #23/#88. Packaging and
publishing are explicit operations and do not create ACK or trigger E2E.
The checked-out candidate must be clean and committed. The full commit SHA
is the image tag and Chart appVersion; the chart version is
`<source-chart-base>-dev.sha-<full-sha>`. This is a development candidate,
not a production release or a declaration that ACK acceptance has passed.

## Build locally

Install the pinned tools: Go from go.mod, ko 0.19.1, crane 0.22.1 and Helm 3.19.0
or compatible Helm 4. Use verified official release archives. Docker is not
required. From the repository root, use an empty ignored output directory:

```shell
python .github/scripts/release_bundle.py build --output .e2e/release
```

Linux amd64 and arm64 binaries are built once by ko with CGO disabled. The
digest-pinned distroless static base supplies CA certificates and a nonroot
user, without a shell/package manager. The bundle checker verifies blob hashes,
ELF architecture, absence of dynamic linking, binary Git revision/clean state,
runtime UID and exact packaged-chart image reference. It performs no provider
Go tests, cloud API calls or security scans.

Outputs:

- `controller.oci.tar`: both platforms in an OCI image layout archive.
- `karpenter-<version>.tgz`: matching templates and CRDs; its default image is
  `<repository>:<full-sha>@sha256:<index-digest>` rather than the original
  project's registry. Source Chart.yaml/values.yaml are not changed.
- `release.json`: revision, image/platform digests, binary build information,
  original CRD checksums, package checksums and publication status.
- `ack-image.yaml`: merge the two image fields into controller_rrsa in the
  private ACK E2E configuration. Other identity/cluster fields remain required.
- `SHA256SUMS` and `sbom/`: portable checksums and ko-generated dependency SBOMs.

An OCI layout archive is not a Docker archive; do not use `docker load`.
For an archived bundle, extract it into `image-layout/` with an OCI-aware tool
or tar, preserving `oci-layout`, `index.json` and `blobs/`. Keep release.json
and the Helm tgz alongside that directory for publishing. Do not extract
untrusted archives. The build stage also retains the original image-layout/
locally, so a later publish does not rebuild either architecture.

## Publish manually

After the reviewed workflow is separately published to the default branch,
open GitHub Actions **PackageCandidate**, choose dev or main and explicitly
select `publish=true`. Default `publish=false` only saves the build artifact.
There is no push/tag/PR/schedule trigger and no dependent E2E workflow call.
Ordinary Actions/docs-only PR CI continues to skip provider Go compilation,
tests and vulnerability scans. The manually selected packaging job necessarily
compiles the controller image; it does not repeat the full Go validation suite.

The default destinations are:

```text
ghcr.io/rambow-cloud/karpenter-provider-alibabacloud:<full-sha>
oci://ghcr.io/rambow-cloud/charts/karpenter:<chart-version>
```

The workflow uses its temporary GITHUB_TOKEN for GHCR; no static cloud keys.
Local publishing requires a registry credential with package write access,
provided to `crane auth login` and `helm registry login` using password-stdin.
Never place it in command arguments, repository files or artifact directories.

```shell
python .github/scripts/release_bundle.py publish --output .e2e/release
```

Other OCI registries can be selected at build time with `--repository` and
`--chart-repository`. ACR credentials/repository provisioning are separate
from cloud RRSA, and some ACR editions require repositories to exist first.
Neither stage provisions a registry or changes package visibility.

For first GHCR publishing, open the organization Packages pages and set both
packages public for this public project, or prepare pull authentication. ACK
smoke currently renders no imagePullSecret, so it requires a publicly pullable
controller image. Check the actual registry route from the selected ACK;
successful publishing does not prove that Hangzhou nodes can reach GHCR.
If GHCR is unsuitable, rebuild the chart bundle for an ACR destination and
publish there. The image bytes/digest can be copied without changing the code.

The image may have published before a later chart push fails. Retain the bundle
and fix that registry permission/availability problem; do not assume both
artifacts are available based on the image push alone. `published=true` is
written only after both registry artifacts are confirmed.

## Use with ACK smoke

Use the complete candidate image tag plus digest from release.json. Install the
packaged chart directly for a fresh deployment, or use the same candidate's
checked-out Chart/CRDs with #23's runner, which supplies its RRSA-only values.
Do not use `latest`, mix chart/CRDs from a different provider revision, or use
an original official image with this fork's configuration. The smoke runner
still needs a dedicated ACK, permissions, native node pool and explicit public
ACK-compatible worker image; packaging does not fill those fields.

The project's minimum Kubernetes compatibility version remains 1.34, but an
ACK API reporting a version as creatable does not establish ongoing support.
For a new E2E cluster choose a maintained ACK version and verify provider/core
compatibility. Actual image pulling, RRSA, bootstrap and worker lifecycle are
cloud acceptance evidence and remain open in #23/#88.
