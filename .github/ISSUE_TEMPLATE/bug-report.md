---
name: Bug Report
about: Report a problem encountered while using volcano-vgpu-device-plugin
labels: bug
---

<!-- Please use this template while reporting a bug and provide as much info as possible. Not doing so may result in your bug not being addressed in a timely manner. Thanks!
-->

**What happened**:

**What you expected to happen**:

**How to reproduce it (as minimally and precisely as possible)**:

**Anything else we need to know?**:

- Relevant excerpts from `nvidia-smi -a`; mask GPU UUIDs, serial numbers, and host or node identifiers
- Relevant Docker or containerd configuration sections. Omit credentials, tokens, passwords, private keys, certificates, and unrelated host data.
- Relevant, time-bounded excerpts from the volcano-vgpu-device-plugin container logs
- Relevant, time-bounded excerpts from the volcano-vgpu-monitor container logs, if applicable
- Relevant, time-bounded excerpts from the volcano-scheduler container logs
- Relevant, time-bounded excerpts from the kubelet logs on the node (e.g: `sudo journalctl -r -u kubelet`)
- The relevant Helm values or deployment manifests
- Relevant, time-bounded kernel output lines from `dmesg`

Before posting, remove or mask credentials, tokens, GPU identifiers, node or host identifiers, and other sensitive data from configuration and logs.

**Environment**:
- volcano-vgpu-device-plugin version:
- Volcano version:
- Kubernetes version:
- HAMi-core version, if applicable:
- NVIDIA driver and CUDA version:
- Docker or containerd version:
- Installation method, image, and tag used:
- Sharing mode (`HAMi-core` or `dynamic-mig`):
- Kernel version from `uname -a`:
- Others:
