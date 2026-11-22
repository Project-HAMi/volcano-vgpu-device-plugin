# Release notes

These summaries describe the existing release tags. Notes for v1.11.0 through
v1.13.0 were also published retrospectively on
[GitHub Releases](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/releases).
The older tags are summarized here from their tagged changes.

Add the plugin and chart summaries in the release pull request before tagging.
The [release procedure](CONTRIBUTING.md#preparing-a-release) describes the
required updates; publishing workflows verify the corresponding entries.

Only the latest released version receives security fixes. Images up to and
including v1.12.0 are unsigned. See [SECURITY.md](SECURITY.md) for the support
policy and image verification instructions.

## Helm chart 1.13.0

- Publishes the chart for the v1.13.0 device plugin and monitor images.
- Supports device and node configuration, CDI, `runtimeClassName` and GPU-free
  testing through nvml-mock. CDI and nvml-mock cannot be enabled together.

[Published notes](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/releases/tag/volcano-vgpu-device-plugin-1.13.0)

## v1.13.0

- Adds nvml-mock for testing without GPUs, a configurable device ConfigMap
  namespace, and the Helm `runtimeClassName` setting.
- Validates allocation state and device annotations, retains health state
  across stream reconnections, and stops registration when the plugin stops.
- Rejects ambiguous pending-Pod matches instead of guessing between different
  device allocations, skips completed or failed binds, and uses bind time to
  break allocation-time ties ([#189](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/pull/189)).
- Configures klog verbosity through `KLOG_LEVEL` (default `2`). The `-v` flag
  remains the CLI version flag rather than the logging verbosity option.
- Updates HAMi-core, Go and other dependencies, and adds keyless signing to
  release images. Dependency upgrades and removals address CVE-2026-84303,
  CVE-2026-84445, CVE-2026-84304, CVE-2026-33186, CVE-2026-25680 and
  CVE-2026-41579, as identified in the published release notes.

[Published notes](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/releases/tag/v1.13.0)
| [Changes since v1.12.0](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/compare/v1.12.0...v1.13.0)

## v1.12.0

- Adds a CDI deployment manifest and GPU filtering by UUID or index.
- Improves monitor collection by avoiding repeated cluster-wide Pod queries.
- Fixes ConfigMap value precedence, CDI/MIG allocation and preferred allocation.
- Updates HAMi-core and synchronizes with NVIDIA's v0.19.0 device plugin.

[Published notes](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/releases/tag/v1.12.0)
| [Changes since v1.11.0](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/compare/v1.11.0...v1.12.0)

## v1.11.0

- Fixes dynamic MIG partition tracking to keep a configuration per physical GPU.
- Updates HAMi-core, deployment examples and documentation.
- CVE-2024-45337 was reported in the historical image's `golang.org/x/crypto`
  dependency. The dependency upgrade in v1.12.0 fixes it; the published notes
  link to the original report.

[Published notes](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/releases/tag/v1.11.0)
| [Changes since v1.10.0](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/compare/v1.10.0...v1.11.0)

## v1.10.0

- Adds dynamic MIG configuration and ConfigMap-based device and node settings.
- Makes GPU health comparisons case-insensitive and replaces the deprecated
  GPU monitoring library with `github.com/NVIDIA/go-nvml`.
- Refreshes configuration documentation and deployment manifests.

[Changes since v1.9.4](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/compare/v1.9.4...v1.10.0)

## v1.9.4

- Adds ARM64 image support and applies `--gpu-memory-factor` to vGPU memory
  registration. Advertised memory uses the configured block size; the option
  already existed in the legacy GPU-sharing path in v1.9.0.
- Includes the vGPU monitor in the device plugin image and updates monitoring
  examples and permissions.
- Improves pending-Pod selection on multiple nodes and updates HAMi-core.

[Changes since v1.9.3](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/compare/v1.9.3...v1.9.4)

## v1.9.3

- Changes the allocation lock annotation from `vgpu` to `hamivgpu`.
- Adds a vGPU Deployment example and refines allocation logging.

[Changes since v1.9.2](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/compare/v1.9.2...v1.9.3)

## v1.9.2

- Registers separate vGPU memory and core resources with kubelet and adds
  configurable names for those resources.
- Removes the legacy gpu-share mode and its `volcano.sh/gpu-memory` resource.
  Use the vGPU deployment path and its `volcano.sh/vgpu-*` resources.

[Changes since v1.9.1](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/compare/v1.9.1...v1.9.2)

## v1.9.1

- Returns an allocation error instead of dereferencing a missing pending Pod.
- Documents the Volcano scheduler configuration and how to check advertised
  vGPU resources.

[Changes since v1.9.0](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/compare/v1.9.0...v1.9.1)

## v1.9.0

- Integrates HAMi-core to enforce in-container GPU memory and compute limits.
- Adds the vGPU resource model with GPU count, memory and core requests, and
  introduces the vGPU deployment manifest.
- Includes `--gpu-memory-factor` for configurable memory blocks in the legacy
  GPU-sharing path.
- The historical `v1.9` tag is an alias of `v1.9.0` at the same commit.

[Changes since v1.0.0](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/compare/v1.0.0...v1.9.0)

## v1.0.0

- Provides the initial tagged Volcano device plugin with GPU discovery, health
  reporting and GPU sharing through `volcano.sh/gpu-memory`.
- Documents node prerequisites, DaemonSet installation and GPU sharing jobs.

[Tagged source and documentation](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/tree/v1.0.0)
