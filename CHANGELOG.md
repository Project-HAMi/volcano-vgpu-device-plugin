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

Go module upgrades, replacements and removals, together with the Go image
builder update from 1.23.7 to 1.25.5, address the following 57 advisory IDs.
These are version-level dependency/build-tool records; they do not establish
that every affected function was reachable in this plugin.

| Module or build tool | Version change | Advisory identifiers |
| --- | --- | --- |
| `github.com/dgrijalva/jwt-go` | `v3.2.0+incompatible` removed/replaced | `CVE-2020-26160` |
| `github.com/elazarl/goproxy` | `v0.0.0-20180725130230-947c36da3153` removed/replaced | `CVE-2023-37788` |
| `github.com/emicklei/go-restful` | `v0.0.0-20170410110728-ff4f55a20633` removed/replaced | `CVE-2022-1996` |
| `github.com/gogo/protobuf` | `v1.3.1` to `v1.3.2` | `CVE-2021-3121` |
| `github.com/golang/glog` | `v0.0.0-20160126235308-23def4e6c14b` to `v1.2.5` | `CVE-2024-45339` |
| `github.com/prometheus/client_golang` | `v1.0.0` to `v1.23.2` | `CVE-2022-21698` |
| `github.com/sirupsen/logrus` | `v1.2.0` to `v1.9.4` | `CVE-2025-65637` |
| `golang.org/x/crypto` | `v0.0.0-20200220183623-bac4c82f6975` to `v0.46.0` | `CVE-2020-29652`, `CVE-2021-43565`, `CVE-2022-27191`, `CVE-2022-30636`, `CVE-2023-48795`, `CVE-2024-45337`, `CVE-2025-22869`, `CVE-2025-47913`, `CVE-2025-47914`, `CVE-2025-58181` |
| `golang.org/x/net` | `v0.0.0-20200421231249-e086a090c8fd` to `v0.48.0` | `CVE-2021-31525`, `CVE-2021-33194`, `CVE-2021-44716`, `CVE-2022-27664`, `CVE-2022-41717`, `CVE-2022-41723`, `CVE-2023-39325`, `CVE-2023-3978`, `CVE-2023-44487`, `CVE-2023-45288`, `CVE-2024-45338`, `CVE-2025-22870`, `CVE-2025-22872`, `CVE-2025-47911`, `CVE-2025-58190` |
| `golang.org/x/oauth2` | `v0.0.0-20190604053449-0f29369cfe45` to `v0.34.0` | `CVE-2025-22868` |
| `golang.org/x/sys` | `v0.0.0-20200413165638-669c56c373c4` to `v0.41.0` | `CVE-2022-29526` |
| `golang.org/x/text` | `v0.3.2` to `v0.32.0` | `CVE-2020-14040`, `CVE-2021-38561`, `CVE-2022-32149` |
| `k8s.io/apimachinery` | `v0.18.2` to `v0.35.2` | `CVE-2020-8559` |
| `k8s.io/client-go` | `v0.18.2` to `v0.35.2` | `CVE-2020-8565` |
| `stdlib` | `1.23.7` to `1.25.5` | `CVE-2025-0913`, `CVE-2025-22871`, `CVE-2025-22873`, `CVE-2025-4673`, `CVE-2025-47906`, `CVE-2025-47907`, `CVE-2025-47912`, `CVE-2025-58183`, `CVE-2025-58185`, `CVE-2025-58186`, `CVE-2025-58187`, `CVE-2025-58188`, `CVE-2025-58189`, `CVE-2025-61723`, `CVE-2025-61724`, `CVE-2025-61725`, `CVE-2025-61727`, `CVE-2025-61729` |

Advisory records: [Go vulnerability database](https://vuln.go.dev/)
and [GitHub Advisory Database](https://github.com/advisories).

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
