# Go dependency security remediation — 2026-10-11

This report records the pre-SDK-migration snapshot. The subsequent [SDK V2.0 migration](SDK_MIGRATION.md) removes the legacy SDK/PDF chain and reduces selected modules from 314 to 294; only the unused OpenPGP advisory remains in that later graph.

Tracks [issues #8–#14](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/3) and [build/dependency hygiene #34](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/34).

The October 10 audit found 46 distinct CVEs in the effective selected module graph and Go 1.26.6 toolchain; nine CVEs also had affected symbols in the Linux amd64 controller. Version or symbol presence alone is not an exploitability assessment. The remediation scan found **zero active CVE records affecting the effective selected versions**, zero affected imported packages, and zero affected symbols in freshly built Linux amd64 and arm64 controllers.

## Final versions

| Issue | Component | Effective version |
| --- | --- | --- |
| #8 | Go / x/net | 1.26.9 / v0.60.0 |
| #9 | x/crypto / x/text / x/mod | v0.57.0 / v0.42.0 / v0.41.0 |
| #10 | gRPC | v1.83.2 |
| #11 | OpenTelemetry API, trace, SDK and OTLP trace exporters | v1.45.0 |
| #12 | etcd api, client, client/pkg, pkg and server Go modules | v3.6.14 |
| #13 | cel-go / klauspost/compress | v0.30.0 / v1.18.7 |
| #14 | glog / goldmark / x/image | v1.2.4 / v1.7.17 / v0.45.0 |

x/net's requirements set the final x/text and x/mod versions above the original issue proposals. Supporting CEL, logging and Go tooling dependencies follow the upgraded libraries' requirements. Karpenter core and the AWS test harness remain pinned to **v1.14.1**.

Several libraries are selected through upstream module metadata without any imported package in this repository. Version-specific `replace` directives patch those selections without adding unused direct dependencies or applying replacements to every historical module graph. `go list -m -json all` may display the original `Version`; the **`Replace.Version` is the effective version** used for the audit and security floor check. Remove these overrides when upstream requirements catch up.

Examples of upstream graph origins (historical edges are not final selected versions):

- etcd: Kubernetes apiextensions/apiserver test metadata.
- gRPC/glog: `tjfoc/gmsm` and Kubernetes metadata.
- goldmark: Alibaba Cloud `endpoint-util` metadata.
- x/image/PDF: legacy Alibaba Cloud SDK → HdrHistogram → historical x/exp or gonum/plot metadata.
- OpenTelemetry SDK/exporters: Kubernetes/gRPC metadata. Updating etcd Go modules does **not** upgrade an ACK control plane, an envtest etcd executable, or a running etcd server.

## Two remaining advisories with no CVE or known fix

| Advisory | Remaining module | Disposition |
| --- | --- | --- |
| [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) | x/crypto v0.57.0, specifically `openpgp` | No imported `openpgp` package in all repository packages, tests and integration-tag sources; no affected binary symbols. The package is unmaintained and must not be introduced. |
| [GO-2026-5781](https://pkg.go.dev/vuln/GO-2026-5781) | rsc.io/pdf v0.1.1 | Selected solely through upstream SDK/test metadata; no imported PDF package in the same scan scope and no affected binary symbols. No PDF functionality is implemented here. |

These advisories are **not fixed or removed**. The present repository has no affected package import in the scanned scope. Keeping required SDK functionality while documenting this evidence is the disposition for #9/#14. CI fails if an affected package becomes imported. The withdrawn GO-2026-4923 and GO-2026-6115 records are excluded from active findings.

## CGO and dependency hygiene

Production builds already disabled CGO. The default is now exported by Makefile and set for CI tests as well. Docker uses Go 1.26.9, `CGO_ENABLED=0` and `-trimpath`; unnecessary forced rebuilding with `-a` is removed. CI builds both Linux architectures and checks Go build information, absence of an ELF interpreter, and absence of shared-library dependencies. Race testing may explicitly opt into CGO with a C compiler.

Project-owned YAML imports now use the already-selected maintained `go.yaml.in/yaml/v3` module. `go mod tidy` removes the obsolete explicit `stoewer/go-strcase` requirement. Older YAML modules remain where upstream packages require them. Selected module count stays **314 → 314**, including the root module; no overall graph reduction or binary size reduction is claimed.

Both Alibaba Cloud SDK families remain necessary: the modular ECS SDK serves provider operations, while the legacy SDK still serves the instance batcher and cloud E2E cleanup. Removing the legacy SDK requires a separate migration. Updating HdrHistogram alone would not remove the PDF metadata chain.

## Evidence and reproduction

Tool: **govulncheck v1.8.0**. Database: `https://vuln.go.dev`, reported modification time **2026-10-08T22:31:09Z**. Toolchain: **Go 1.26.9**. Source scan: Linux amd64, CGO disabled, `./...` including tests and the `integration` build tag. Binary scan: Linux amd64 and arm64, symbol level. Binary symbol presence checks do not constitute full source call-graph or exploit-path analysis.

```sh
export CGO_ENABLED=0
go run ./hack/security
go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
GOOS=linux GOARCH=amd64 govulncheck -scan=package -test -tags=integration ./...
GOOS=linux GOARCH=amd64 go build -trimpath -o bin/controller ./cmd/controller
GOOS=linux GOARCH=arm64 go build -trimpath -o bin/controller-arm64 ./cmd/controller
govulncheck -mode=binary bin/controller
govulncheck -mode=binary bin/controller-arm64
```

The effective graph audit queries all selected replacement-aware module versions plus `stdlib@v1.26.9` and `toolchain@v1.26.9`, then filters OSV SEMVER ranges and withdrawn entries. `go.sum` historical versions are not counted as selected dependencies. CI separately enforces audited effective version floors and scans imported packages and both binaries; its `go-security-reports` artifact retains readable scan results.

Local build hashes (before commit; Go build information includes the dirty working tree):

| Architecture | SHA-256 |
| --- | --- |
| amd64 | `0a875e4c95711ae3c8edfb6b9ee7e93da81f93fea4027e02bb0528296f1ba08e` |
| arm64 | `bf58be164d76a70601098b40056b95d7be47c420a2892fea3a090343940a45c8` |

Raw local graph, source, binary and static-link evidence is retained in `.git/security-remediation/`; baseline evidence is in `.git/security-scan/`. CI rebuilds and scans the reviewed commit. Local security floor tests exercise fixed replacements, vulnerable replacements, missing versions, fork replacements, prerelease versions and removed modules.

Scope limits: the Docker daemon was unavailable locally, so the final Debian container OS packages were **not scanned**. Independently executed upstream modules and actual ACK cluster/control-plane versions are outside the root module audit. ACK E2E remains a release acceptance item in #5; no cloud resources were created for this remediation. Linux controller contract tests and integration harness compilation are required in CI.
