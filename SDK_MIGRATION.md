# Alibaba Cloud SDK V2.0 migration

Tracks [issue #36](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/36) and [milestone #8](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/8). Baseline: `ed71a65e0f571e53c4afa08d7ee95c5582602e8a`; audit date: 2026-10-11.

This report records the SDK migration snapshot in PR #37. A subsequent cleanup, [issue #39](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/39), removes unused ALB/NLB sweeper coverage and their SDK modules: selected modules **294 → 292**, with no new modules. The version table and scan evidence below describe the earlier migration snapshot.

The current minimal ACK/ECS baseline retains SLB listener/ACL handling and tagged SLB resource sweep. It does not query or explicitly delete ALB/NLB resources, or include an explicit ALB option in the ACK cluster deletion request. ALB/NLB workload testing and residual-resource cleanup are outside this baseline. ACK cluster deletion still uses the CS API; these scope statements concern the helper's explicit operations, not the service's internal resource-deletion behavior.

Alibaba Cloud [SDK V2.0](https://www.alibabacloud.com/help/en/sdk/developer-reference/v2-go-integrated-sdk) is the Tea/OpenAPI SDK family. Product modules have independent major versions; ECS `/v5`, ALB `/v2` and SLB/NLB `/v4` all belong to that family.

## Scope and versions

The main ECS, VPC and ACK paths already used V2.0. This change migrates the remaining instance batcher, the interruption controller's unused client type, and ACK helper SLB/ALB/NLB calls. Spot interruption remains unimplemented and tracked separately in #20. No CRD/RBAC/feature-gate or deployment configuration changes are required.

| Component | Version |
| --- | --- |
| ECS | ecs-20140526/v5 v5.1.0 (retained) |
| SLB | slb-20140515/v4 v4.0.14 |
| ALB | alb-20200616/v2 v2.3.2 |
| NLB | nlb-20220430/v4 v4.1.3 |
| OpenAPI / Tea / Tea utils | v2.2.1 / v1.5.0 / v2.0.9 |
| Karpenter core / AWS harness | v1.14.1 / v1.14.1 (retained) |

The new product SDKs require the supporting OpenAPI/Tea upgrades. Versions are pinned in `go.mod`; their package APIs and provider behavior are covered by offline tests and required CI. Actual cloud behavior still needs the deferred ACK release acceptance.

## Behavior and Go API compatibility

The batcher now uses ECS V2 pointer models, reads instance IDs under `response.Body.InstanceIdSets`, and takes a client with `RunInstances(context.Context, *ecs.RunInstancesRequest)`. Use the existing `clients.NewDefaultECSClient(sdkClient, region)` adapter when constructing `NewInstanceBatcher`; a raw generated ECS SDK client does not implement that context-taking interface. Passing the context through this interface does not add HTTP cancellation to the existing adapter.

Batch requests are deep copied instead of copying a subset of fields. The batch key covers the full request except the batch-assigned `Amount`, including client tokens, billing, launch templates and metadata options. Security-group ordering/duplicates, tag ordering, ESSD PL0 defaults and disk encryption/delete defaults are normalized. Requests with different tokens stay separate. Empty/partial response bodies report errors to waiters instead of dereferencing missing pointers; no instance with a missing ID is treated as successfully launched.

SLB/NLB RPC requests explicitly carry `RegionId`; client configuration alone must not substitute for a required request parameter. Existing resource selectors, tag filters, SLB page size 50, ALB/NLB token page size 100, protection flags, deletion IDs and listener/ACL operations are retained. Missing response bodies return an error so a failed discovery is not reported as a clean sweep; repeated token pagination also fails. Product clients use the existing explicit access-key configuration and region. Helpers are not executed against a real account by this migration.

## Dependency result

Selected modules, including the root: **314 → 294**. Three product SDK modules are added and 23 obsolete modules disappear. Removed modules include:

- `github.com/aliyun/alibaba-cloud-sdk-go`
- `github.com/HdrHistogram/hdrhistogram-go`
- `gonum.org/v1/gonum`, `gonum.org/v1/netlib`, `gonum.org/v1/plot`
- `rsc.io/pdf`, `github.com/jung-kurt/gofpdf` and associated historical graphics modules
- `golang.org/x/image`, whose now-unused security replacement is removed
- old JMESPath, OpenTracing and Jaeger dependencies

The PDF advisory GO-2026-5781 is eliminated from this root's selected graph by removing its dependency chain; no PDF fix version is claimed. Other version-specific security replacements remain where upstream metadata still selects the affected original versions. CI rejects any reintroduction of the legacy SDK or PDF module.

## Verification and limitations

Local checks use **Go 1.26.9**, **govulncheck v1.8.0**, `CGO_ENABLED=0`, and the Go vulnerability database snapshot reporting **2026-10-08T22:31:09Z**. Effective replacement-aware module/toolchain querying finds zero active CVE records. One advisory without a CVE or known fix remains: [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) for the unimported OpenPGP package. Source package scanning of Linux amd64 `./...`, tests and `integration` tags finds no affected imports.

Linux amd64 and arm64 controller builds and binary symbol scans pass. Both binaries record CGO disabled and contain neither an ELF interpreter nor shared-library dependencies. SDK upgrades do not guarantee a smaller executable: local amd64 size is 130,229,884 bytes, versus 129,959,334 before migration. The reduction measured here is the selected dependency graph.

Local unit tests cover batch request fields/deep-copy ownership, partial/empty IDs, parameter grouping, SLB region/tag pagination, ALB/NLB tokens, missing bodies, protection error sequencing, restricted ACL requests and credential/region validation. Provider, client and cloud-helper tests also pass. Required Linux CI checks controller contracts, vet, harness compilation, Helm/generated artifacts, dependency consistency, security floors and fresh source/binary scans. CI retains `go-security-reports` artifacts.

Local binary hashes (before commit, with dirty-tree build information):

| Architecture | SHA-256 |
| --- | --- |
| amd64 | `1fed0ef44b697a0a405e0ca60dd971b074d6ebe5d65e5fcc0b6b934e492d76d8` |
| arm64 | `263be094e8d98499d54bd54d28f308c79f9141862097cb97252c4798de5093a8` |

Raw local graph/source/binary evidence is retained under `.git/sdk-migration/`. These Go checks do not scan final container OS packages or separately executed upstream module graphs. No real cloud APIs, cleanup or ACK E2E were run; #5 remains the release acceptance item. The earlier [security audit](SECURITY_AUDIT.md) documents the pre-migration snapshot.
