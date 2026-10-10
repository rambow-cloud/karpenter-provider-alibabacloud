# Code review and delivery plan

Reviewed baseline: `67a27fd` (provider/chart 0.2.5, Karpenter core 1.8.0).

| Severity | Finding | Consequence | Planned correction |
| --- | --- | --- | --- |
| P1 | `pkg/cloudprovider/cloudprovider.go:List` filters only managed-by, without cluster ID. | Controllers can discover another cluster's instances in a shared account/region. | Require configured cluster ID and include its ownership tag in discovery. |
| P1 | `pkg/controllers/nodeclaim/garbagecollection/garbagecollection.go:Reconcile` treats a list omission as proof of deletion, calls cloud Delete, and removes a Node without a direct Get confirmation. It also processes NodeClaims with no providerID or another NodeClass kind. | Stale/eventually consistent lists can lead to destructive cleanup; pending/foreign claims can enter cleanup. | Check supported NodeClass ownership and nonempty providerID; confirm absence via Get; let upstream lifecycle own termination. |
| P2 | `pkg/cloudprovider/cloudprovider.go:Delete` returns nil for an invalid providerID. | Karpenter retries deletion indefinitely without a useful diagnostic. | Return an explicit invalid-providerID error; preserve not-found errors for truly absent instances. |
| P1 | Core 1.8.0, Go 1.24 container, alpha Kubernetes libraries, and copied CRDs predate stable core 1.14.1. | A module-only upgrade does not establish deployed compatibility. | Upgrade the coherent dependency set, adapt call sites, synchronize CRDs/RBAC, and add CI consistency checks. |
| P2 | No issue forms, contribution flow, PR template, or general provider unit/build CI. | Work lacks acceptance criteria and upgrades can bypass contract/build checks. | Add forms, policy, project/milestone tracking, and compatibility CI. |
| P2 | Core and provider feature gates share one CLI flag, while provider parsing rejects core gate names. | The existing E2E install value enabling NodeOverlay/NodeRepair can fail provider option parsing. | Separate core and provider flags/Helm values and test the combined configuration. |
| P2 | The E2E workflow uses the unavailable runner context in job-level environment configuration. | GitHub reports workflow validation failures before any job starts. | Use the supported github.workspace context, create the private config directory, and lint workflow syntax in CI. |
| P2 | ACK test reservations use a timestamp for their idempotency token; rapid calls can collide, and cleanup selectors duplicate the current managed-by value while omitting the legacy value. | Test requests may collapse into one reservation and cleanup can miss legacy test resources. | Generate random bounded tokens and retain both legacy/current ownership selectors. |

The upgrade target is the official stable [Karpenter v1.14.1](https://github.com/kubernetes-sigs/karpenter/releases/tag/v1.14.1). Its [go.mod](https://github.com/kubernetes-sigs/karpenter/blob/v1.14.1/go.mod) requires Go 1.26.6. This review focuses on lifecycle safety, compatibility, and delivery controls; it is not a claim of exhaustive auditing of every Alibaba Cloud API.

Delivery order: repository workflow setup; lifecycle safety with regression tests; pinned core upgrade with CRD/RBAC/toolchain/CI and migration documentation. Cloud end-to-end validation requires an ACK test cluster and is tracked explicitly as release evidence.

