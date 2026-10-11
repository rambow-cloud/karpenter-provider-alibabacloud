---
title: Quick start
description: Install Alibaba Cloud Karpenter from matching source on ACK Kubernetes 1.34+, configure RAM credentials and Helm, define ECSNodeClass and NodePool, and inspect node provisioning.
---

# Quick start

This guide targets a **fresh provider installation** on an existing ACK cluster. The **0.3.0-dev** chart still needs real ACK lifecycle acceptance; start in an isolated test environment. [Versions and images](../support/index.md) describes version and OS boundaries.

## 1. Prepare the cluster and permissions

- ACK Kubernetes 1.34 or newer, working `kubectl` access and Helm 3.
- Identify the ACK cluster ID, region, VPC, vSwitches and security groups; node networking must reach the API server.
- The controller needs appropriate ECS, VPC, RAM and CS permissions. Public-image reads include `cs:DescribeClusterDetail`, `cs:DescribeKubernetesVersionMetadata` and `ecs:DescribeImages`.
- Prepare an ECS node RAM role separately from the controller's cloud API identity.
- Keep the controller on an existing real node so its initial startup does not depend on nodes it provisions itself.

RRSA is recommended; follow the repository's [RRSA and RAM permission guide](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/README.md) for cluster setup, role trust and controller environment variables. For an AccessKey-based test environment, create the `karpenter` namespace and a Kubernetes Secret inside it beforehand, with `access_key_id` and `access_key_secret` keys. Do not commit credentials. The values below reference that existing Secret; a local CLI profile is not automatically available inside the Pod.

## 2. Prepare a matching image and configuration

```sh
git clone https://github.com/rambow-cloud/karpenter-provider-alibabacloud.git
cd karpenter-provider-alibabacloud
```

Choose a commit, build its controller image and push it to your registry. See the [source installation guide](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/UPGRADING.md) for build requirements, gates and CRDs. Use image, chart and CRDs from the same commit rather than a historical release image.

Create `your-values.yaml` and replace every placeholder. Use the actual ACK region and cluster ID. RRSA users should configure the role environment variables described above instead of an AccessKey Secret; do not configure both.

```yaml
settings:
  clusterID: YOUR_ACK_CLUSTER_ID
  region: cn-hangzhou
controller:
  image:
    repository: YOUR_REGISTRY/YOUR_PROVIDER
    tag: YOUR_COMMIT_TAG
  credentialsSecretName: karpenter-credentials
  nodeSelector:
    kubernetes.io/hostname: YOUR_EXISTING_NODE_NAME
```

The controller RAM policy must authorize all required operations; the image read actions listed above are not a complete permission policy.

## 3. Install CRDs and controller

```sh
kubectl apply --server-side --field-manager=karpenter-install -f charts/karpenter/crds/
helm install karpenter ./charts/karpenter \
  --namespace karpenter --create-namespace -f your-values.yaml
kubectl rollout status deployment/karpenter -n karpenter
```

Configure the chart's `imagePullSecrets` for a private registry. Create NodeClass and NodePool resources after the controller starts.

## 4. Define node configuration and capacity limits

Save the following as `node-config.yaml`. Replace cluster ID, vSwitch, security group and the ECS node RAM role. The image-name pattern matches Alibaba Cloud Linux 3 amd64 images within the target ACK list. If no compatible candidate exists, NodeClass resolution fails; adjust selectors using the [image policy](../support/index.md).

```yaml
apiVersion: karpenter.alibabacloud.com/v1alpha1
kind: ECSNodeClass
metadata:
  name: default
spec:
  clusterID: YOUR_ACK_CLUSTER_ID
  vSwitchSelectorTerms:
    - id: vsw-YOUR_VSWITCH_ID
  securityGroupSelectorTerms:
    - id: sg-YOUR_SECURITY_GROUP_ID
  imageSelectorTerms:
    - name: aliyun_3_x64*
      imageOwnerAlias: system
  role: YOUR_ECS_NODE_RAM_ROLE
---
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: default
spec:
  limits:
    cpu: "10"
  template:
    spec:
      nodeClassRef:
        group: karpenter.alibabacloud.com
        kind: ECSNodeClass
        name: default
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: [amd64]
        - key: karpenter.sh/capacity-type
          operator: In
          values: [on-demand]
```

```sh
kubectl apply -f node-config.yaml
kubectl get ecsnodeclass default -o yaml
kubectl get nodepool default
```

The NodeClass must be `Ready=True` with `observedGeneration` equal to its current `metadata.generation`. `limits.cpu` bounds NodePool CPU capacity; it is not a hard cloud-spend budget.

## 5. Inspect provisioning and cleanup

In the test environment, submit a workload that remains pending because of insufficient capacity, then observe:

```sh
kubectl get pods -A
kubectl get nodeclaims
kubectl get nodes
kubectl logs -n karpenter deployment/karpenter --tail=100
```

Verify NodeClaim creation, node registration, Pod Ready and node reclamation after workload deletion, including ECS resource cleanup. Submitting a workload may create billable ECS resources. A successful controller install does not establish lifecycle acceptance. [Issue #23](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/23) tracks the repeatable smoke harness; this website change did not run cloud tests.

Continue to [core features](../features/index.md), [status and roadmap](../status/index.md) or [contributing](../participate/index.md) to report a sanitized issue.
