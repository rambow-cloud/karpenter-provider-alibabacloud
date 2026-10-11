# ECSNodeClass readiness and image selection

The controller uses resolved `ECSNodeClass.status` only when `Ready=True` and its `observedGeneration` equals `metadata.generation`. All resource resolution conditions record the generation they resolved. Updating a spec blocks new scheduling and creation until its required resources have resolved again. A failure can leave previous resource details in status for diagnosis, but those details are not ready to use. A result computed before another spec update or object replacement is discarded at status writeback.

Existing NodeClasses with conditions written by an older controller must reconcile before they can provision again. Running nodes are not deleted by this readiness check. Apply the matching CRD before upgrading the controller; Helm does not automatically upgrade files in its `crds/` directory. Follow [UPGRADING.md](UPGRADING.md).

## Image selector contract

Resolved images retain ECS `creationTime`. For each instance architecture, new launches prefer the newest valid RFC3339 timestamp among the selected candidates. Missing/invalid timestamps rank last; equal or unknown times use image ID ascending for a stable tie-break. Duplicate IDs retain the entry with the preferred metadata. Selectors remain an unordered OR: a single exact ID pins that image, while multiple IDs or mixed dynamic/pinned terms allow any matching candidate and use the same version ranking. No image outside the selectors or ACK compatibility list is introduced.

Scheduling excludes instance types without a known compatible image architecture, and creation checks the pair again before RunInstances. Known ECS/Kubernetes aliases normalize consistently; unknown or missing image/instance architecture cannot authorize a launch. Reconcile old NodeClass status after upgrading so its architecture metadata is current.

Dynamic discovery prefers newer images for new nodes. Older IDs remain allowed while they still resolve, so a newer release alone does not mark an existing node ImageDrift; removing its image from the resolved set or changing the NodeClass can still cause drift under the existing policy. This deliberately preserves the current membership-based drift behavior rather than adding automatic image rollouts.

The selector cache uses dereferenced field values and normalized tag/OR-term ordering. Positive entries default to 5 minutes, the cache holds at most 256 entries, and reads/writes reclaim all expired entries. Empty results and errors are not cached, and callers receive copied image slices. The ACK compatibility snapshot remains a separate, single 5-minute snapshot keyed by cluster context.

`imageSelectorTerms` is an OR of 1–30 terms. Fields within one term are combined as AND filters. Admission uses the generated CRD schema/CEL rules; runtime resolution validates the entire expression before reading its cache or making any ECS query. The request builder also rejects invalid or unsupported filters instead of silently removing them.

- Each term needs an image ID, family, source alias, name, or a non-empty tag map.
- An exact `id` is exclusive with other fields. Public `.vhd` and `.qcow2` IDs and `m-` IDs are supported.
- `imageOwnerAlias` accepts `system`, `self`, `others`, or `marketplace`.
- `imageOwnerID` requires an explicitly specified `imageOwnerAlias: others`. A missing alias is rejected; it is not silently defaulted. This selects shared images. The current API has no community `IsPublic` selector.
- Owner IDs use 6–19 decimal digits without leading zeroes and must fit a positive SDK `int64` (maximum `9223372036854775807`). They remain strings in YAML to avoid numeric conversion.
- Tag keys and values must be non-empty; a term accepts at most 20 tags. Empty tag values are rejected, not dropped.
- Names and families must contain a non-whitespace character and contain at most 128 characters. IDs contain at most 128 characters and must match the supported ID format.

Example for a shared image (the account ID is synthetic):

```yaml
imageSelectorTerms:
  - name: app-*
    imageOwnerAlias: others
    imageOwnerID: "1234567890123456"
    tags:
      environment: test
```

The [ECS DescribeImages API](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-describeimages) limits `ImageOwnerId` to shared/community images. It defines `others` as that source and requires `IsPublic=true` to query community images. This provider's owner selector currently covers shared images only. `ShowExpired` remains unchanged pending the evidence gathering in #29; selector regression tests do not establish ContainerOS behavior on the real ECS API.

## 中文说明

只有 `Ready=True` 且 `observedGeneration` 与当前 `metadata.generation` 一致，控制器才使用 NodeClass 解析结果进行调度和创建。修改镜像、网络等 spec 后，必须重新解析当前版本；失败时保留的旧状态只用于诊断。解析期间 spec 再次变化或对象被重建时，旧结果不会写回新对象。旧控制器写入的条件需重新 reconcile 后才能恢复扩容；已有节点不会因此被删除。

镜像 selector 在 CRD admission、解析器和请求构造器三个层次校验。空 term、空标签键值、非法来源、ID 与其他条件混用，以及超出 SDK int64 范围的 OwnerID 都会被拒绝。`imageOwnerID` 必须显式配合 `imageOwnerAlias: others`，用于阿里云共享镜像；省略来源不会自动补全，也不支持通过该字段查询社区镜像。升级前应先修正已有无效配置，再应用对应 CRD 并部署控制器；不要删除 CRD。

真实 ACK/ECS 的生命周期与升级验收仍由 #5 跟踪。ACK 测试工具的 CS/ECS/VPC 连接超时为 **30 秒**，通过 SDK 的毫秒参数 `30000` 表示；离线测试只检查配置，不声称实际云端连接已验证。
