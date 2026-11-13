# Volcano vGPU device plugin

A Kubernetes device plugin that lets pods share NVIDIA GPUs under the [Volcano](https://volcano.sh) scheduler. It is a subproject of [HAMi](https://github.com/Project-HAMi/HAMi), based on the NVIDIA device plugin, and uses [HAMi-core](https://github.com/Project-HAMi/HAMi-core) to enforce per-container memory and core limits.

A GPU is split into several vGPUs. Each pod asks for a number of vGPUs and, optionally, device memory and a share of the cores. There are two sharing modes:

- `hami-core`: software isolation, works on any GPU.
- `mig`: dynamic MIG, hardware isolation, needs an Ampere or newer GPU.

A node uses one mode, and a cluster can mix both. Plugin settings are described in [doc/config.md](doc/config.md).

## Compatibility

| volcano-vgpu | Volcano |
|---|---|
| v1.13.0 | v1.15.0 and above |
| v1.12.0 and below | v1.15.0 and below |
| master | master |

The plugin is built with client-go 0.37. The manifests in this repository were validated against Kubernetes 1.36.

## Requirements

- An NVIDIA driver and the NVIDIA Container Toolkit on every GPU node, with `nvidia` as the default container runtime.
- The Volcano scheduler with vGPU enabled (below).

## Enable vGPU in Volcano

In `volcano-scheduler-configmap` (namespace `volcano-system`), add the `deviceshare` plugin with `VGPUEnable`:

```yaml
actions: "enqueue, allocate, backfill"
tiers:
- plugins:
  - name: priority
  - name: gang
  - name: conformance
- plugins:
  - name: drf
  - name: deviceshare
    arguments:
      deviceshare.VGPUEnable: true
      deviceshare.SchedulePolicy: binpack  # or spread
  - name: predicates
  - name: proportion
  - name: nodeorder
  - name: binpack
```

## Install

No Helm repository is published yet, so install from a checkout:

```shell
git clone https://github.com/Project-HAMi/volcano-vgpu-device-plugin
cd volcano-vgpu-device-plugin
helm install volcano-vgpu-device-plugin deployments/helm/volcano-vgpu-device-plugin
```

Or apply the plain manifest:

```shell
kubectl apply -f deployments/static/volcano-vgpu-device-plugin.yml
```

Useful chart options:

- `--set cdi.enabled=true` passes devices through CDI instead of environment variables.
- `--set nvmlMock.enabled=true` runs against [nvml-mock](https://github.com/NVIDIA/k8s-test-infra) on a cluster without GPUs, which is enough to check scheduling and registration. It cannot be combined with `cdi.enabled`.

Check that the node reports vGPUs:

```shell
kubectl get node <node> -o jsonpath='{.status.allocatable}'
```

`volcano.sh/vgpu-number` and `volcano.sh/vgpu-memory` should be listed. The
number capacity is the GPU count times `deviceSplitCount` (10 by default). The
memory capacity is expressed in the blocks described below, not always in MiB.

## Run a vGPU job

Request vGPUs in `resources.limits`:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: gpu-pod
  annotations:
    volcano.sh/vgpu-mode: "hami-core"  # optional: hami-core or mig
spec:
  schedulerName: volcano
  containers:
  - name: cuda
    image: nvidia/cuda:12.4.1-base-ubuntu22.04
    command: ["sleep", "infinity"]
    resources:
      limits:
        volcano.sh/vgpu-number: 2    # two vGPUs
        volcano.sh/vgpu-memory: 3000 # optional: memory blocks per vGPU
        volcano.sh/vgpu-cores: 50    # optional: percent of a GPU per vGPU
```

### GPU memory units

`volcano.sh/vgpu-memory` is an integer count of memory blocks. Each block is
`gpuMemoryFactor` MiB, configured through
`deviceConfig.nvidia.gpuMemoryFactor` in the Helm chart. The default factor is
1, so the request value equals MiB only with the default configuration.

```text
memory limit per vGPU (MiB) = vgpu-memory request * gpuMemoryFactor
vgpu-memory request = ceil(desired memory in MiB / gpuMemoryFactor)
```

For example, with `gpuMemoryFactor: 2`, a request of `1500` gives each requested
vGPU a 3000 MiB limit. When the desired limit is not an exact multiple of the
factor, round the request up to a whole block.

The plugin registers one synthetic device for every memory block. For GPU `i`,
the registered block count is
`floor(GPU memory in MiB / gpuMemoryFactor)`. Keep the sum across a node at or
below 60000; a larger `ListAndWatch` response can exceed kubelet's default gRPC
receive limit and leave `volcano.sh/vgpu-memory` at zero. Use the smallest
factor that keeps the largest node in the cluster below this limit.

Use the same factor for every node that exposes the same memory resource name.
Otherwise, the same Pod request represents different memory limits depending
on the selected node. Changing the factor also changes the meaning of existing
Pod manifests; update their `vgpu-memory` values before deploying the new
configuration.

In `hami-core` mode `nvidia-smi` inside the container shows the resulting
memory limit. In `mig` mode it shows the memory of the assigned MIG instance,
which comes from the configured MIG geometry. A pod cannot request more vGPUs
than the node has GPUs. Without the annotation, either mode may be used.

## Metrics

The plugin pod serves per-container GPU usage and limits on port 9394:

```shell
curl <plugin-pod-ip>:9394/metrics
```

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md). Report vulnerabilities as described in [SECURITY.md](SECURITY.md), not in a public issue. Questions go to [GitHub issues](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/issues) or the [HAMi community](https://github.com/Project-HAMi/community).

## License

Apache-2.0, see [LICENSE](LICENSE).
