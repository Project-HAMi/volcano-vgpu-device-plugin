# Scale GPU nodes from zero with Karpenter

Karpenter launches a node only if a simulation says the pending pods fit on it. The simulation knows each instance type's CPU, memory and `nvidia.com/gpu`, but not `volcano.sh/vgpu-number`, `volcano.sh/vgpu-memory` or `volcano.sh/vgpu-cores`. Those exist only after this plugin registers on a running node. So a pod that requests vGPUs fits no instance type, and it stays `Pending` unless a GPU node is already up.

A [NodeOverlay](https://karpenter.sh/docs/concepts/nodeoverlays/) tells Karpenter what a node will register. NodeOverlay is alpha (`karpenter.sh/v1alpha1`) and off by default. Turn it on with the `NodeOverlay` feature gate (Helm: `settings.featureGates.nodeOverlay: true`).

## What a node registers

| Resource | Per node |
|---|---|
| `volcano.sh/vgpu-number` | GPUs × `deviceSplitCount` |
| `volcano.sh/vgpu-memory` | Sum over GPUs of (memory in MiB as NVML reports it ÷ `gpuMemoryFactor`) |
| `volcano.sh/vgpu-cores` | GPUs × 100 × `deviceCoreScaling` (1 by default) |

Read the values off a running node of each type rather than computing them from a spec sheet. NVML reports less memory than the marketed size, and per-node settings in `volcano-vgpu-node-config` override the global ones:

```shell
kubectl get node <node> -o jsonpath='{.status.allocatable}'
```

If an overlay states more than the node registers, Karpenter launches a node the pod does not fit on, and the pod stays `Pending`. If it states less, Karpenter only passes over that type for pods near the limit.

## Example

One overlay per GPU model. In this NodePool every instance type has a single L4, the plugin runs with `deviceSplitCount: 8` and `gpuMemoryFactor: 4`, and the node registers `vgpu-memory: 5758` (23,032 MiB ÷ 4):

```yaml
apiVersion: karpenter.sh/v1alpha1
kind: NodeOverlay
metadata:
  name: gpu-shared-l4
spec:
  requirements:
    - key: karpenter.sh/nodepool
      operator: In
      values: [gpu-shared]
    - key: karpenter.k8s.aws/instance-family
      operator: In
      values: [g6]
  capacity:
    volcano.sh/vgpu-number: "8"
    volcano.sh/vgpu-memory: "5758"
    volcano.sh/vgpu-cores: "100"
```

If a NodePool mixes GPU counts, add one overlay per count and select on `karpenter.k8s.aws/instance-gpu-count`, since the capacity grows with the number of GPUs.

An overlay changes only Karpenter's simulation. The node still registers whatever the plugin reports, and Volcano places pods by that.

Tested with Karpenter 1.15.0, Volcano v1.15.4 and this plugin at v1.13.0 on EKS: pods that request only vGPU resources brought up L4 nodes from an empty NodePool.
