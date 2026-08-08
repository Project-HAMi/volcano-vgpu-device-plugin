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

- The output of `nvidia-smi -a` on your host
- Relevant Docker or containerd configuration sections. Omit credentials, tokens, passwords, private keys, certificates, and unrelated host data.
- The volcano-vgpu-device-plugin container logs
- The volcano-vgpu-monitor container logs, if applicable
- The volcano-scheduler container logs
- The kubelet logs on the node (e.g: `sudo journalctl -r -u kubelet`)
- The relevant Helm values or deployment manifests
- Any relevant kernel output lines from `dmesg`

Before posting, remove or mask credentials, tokens, and other sensitive data from configuration and logs.

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
