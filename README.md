# Volcano vgpu device plugin for Kubernetes

[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2FProject-HAMi%2Fvolcano-vgpu-device-plugin.svg?type=shield)](https://app.fossa.com/projects/git%2Bgithub.com%2FProject-HAMi%2Fvolcano-vgpu-device-plugin?ref=badge_shield)
[![docker pulls](https://img.shields.io/docker/pulls/projecthami/volcano-vgpu-device-plugin.svg)](https://hub.docker.com/r/projecthami/volcano-vgpu-device-plugin)

**Note**:

Volcano vgpu device-plugin can provide device-sharing mechanism for NVIDIA devices managed by volcano.

This is based on [Nvidia Device Plugin](https://github.com/NVIDIA/k8s-device-plugin), it uses [HAMi-core](https://github.com/Project-HAMi/HAMi-core) to support hard isolation of GPU card.

And collaborate with volcano, it is possible to enable GPU sharing.

## Table of Contents

- [About](#about)
- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
  - [Preparing your GPU Nodes](#preparing-your-gpu-nodes)
  - [Enabling vGPU Support in Kubernetes](#enabling-gpu-support-in-kubernetes)
  - [Running vGPU Jobs](#running-vgpu-jobs)
- [Issues and Contributing](#issues-and-contributing)

## About

The Volcano device plugin for Kubernetes is a Daemonset that allows you to automatically:
- Expose the number of GPUs on each node of your cluster
- Keep track of the health of your GPUs
- Run GPU enabled containers in your Kubernetes cluster.
- Provide device-sharing mechanism for GPU tasks as the figure below.
- Enforce hard resource limit in container.
- Support dynamic-mig, for more details, see [config](doc/config.md)  

<img src="./doc/example.png" width = "600" /> 

## Prerequisites

The list of prerequisites for running the Volcano device plugin is described below:
* NVIDIA drivers > 440
* nvidia-docker version > 2.0 (see how to [install](https://github.com/NVIDIA/nvidia-docker) and it's [prerequisites](https://github.com/nvidia/nvidia-docker/wiki/Installation-\(version-2.0\)#prerequisites))
* docker configured with nvidia as the [default runtime](https://github.com/NVIDIA/nvidia-docker/wiki/Advanced-topics#default-runtime).
* Kubernetes version >= 1.16
* Volcano verison >= 1.9

## Quick Start

### Preparing your GPU Nodes

The following steps need to be executed on all your GPU nodes.
This README assumes that the NVIDIA drivers and nvidia-docker have been installed.

Note that you need to install the nvidia-docker2 package and not the nvidia-container-toolkit.
This is because the new `--gpus` options hasn't reached kubernetes yet. Example:
```bash
# Add the package repositories
$ distribution=$(. /etc/os-release;echo $ID$VERSION_ID)
$ curl -s -L https://nvidia.github.io/nvidia-docker/gpgkey | sudo apt-key add -
$ curl -s -L https://nvidia.github.io/nvidia-docker/$distribution/nvidia-docker.list | sudo tee /etc/apt/sources.list.d/nvidia-docker.list

$ sudo apt-get update && sudo apt-get install -y nvidia-docker2
$ sudo systemctl restart docker
```

You will need to enable the nvidia runtime as your default runtime on your node.
We will be editing the docker daemon config file which is usually present at `/etc/docker/daemon.json`:
```json
{
    "default-runtime": "nvidia",
    "runtimes": {
        "nvidia": {
            "path": "/usr/bin/nvidia-container-runtime",
            "runtimeArgs": []
        }
    }
}
```
> *if `runtimes` is not already present, head to the install page of [nvidia-docker](https://github.com/NVIDIA/nvidia-docker)*


### Configuration

You need to enable vgpu in volcano-scheduler configMap:

```shell script
kubectl edit cm -n volcano-system volcano-scheduler-configmap
```

For volcano v1.9+,, use the following configMap 
```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: volcano-scheduler-configmap
  namespace: volcano-system
data:
  volcano-scheduler.conf: |
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
          deviceshare.VGPUEnable: true # enable vgpu
          deviceshare.SchedulePolicy: binpack  # scheduling policy. binpack / spread
      - name: predicates
      - name: proportion
      - name: nodeorder
      - name: binpack
```

### Sharing Mode

Volcano-vgpu supports two types of device-sharing: `HAMi-core` and `dynamia-mig`, A node can either using `HAMi-core`, or `Dynamic-mig`. Heterogeneous is supported(a part of node using HAMi-core, the other using Dynamic-mig)

A brief introduction about these two modes:

HAMi-core is a user-layer resource isolator provided by HAMi community, works on all types of GPU.

Dynamic-mig is a hardware resource isolator, works on Ampere arch or later GPU. 

The table below shows the summary:
| Mode        | Isolation        | MIG GPU Required | Annotation | Core/Memory Control | Recommended For            |
| ----------- | ---------------- | ---------------- | ---------- | ------------------- | -------------------------- |
| HAMI-core   | Software (VCUDA) | No               | No         | Yes                 | General workloads          |
| Dynamic MIG | Hardware         | Yes              | Yes        | MIG-controlled      | Performance-sensitive jobs |

You can set the sharing mode and customize your installation by adjusting the [configs](doc/config.md)


### Enabling GPU Support in Kubernetes

Once you have enabled this option on *all* the GPU nodes you wish to use,
you can then enable GPU support in your cluster by deploying the following Daemonset:

```
$ kubectl create -f volcano-vgpu-device-plugin.yml
```

### Verify environment is ready

Check the node status, it is ok if `volcano.sh/vgpu-number` is included in the allocatable resources.

```shell script
$ kubectl get node {node name} -oyaml
...
  capacity:
    volcano.sh/vgpu-memory: "89424"
    volcano.sh/vgpu-number: "10"   # vGPU resource
```

### Running VGPU Jobs

VGPU can be requested by both set "volcano.sh/vgpu-number" , "volcano.sh/vgpu-cores" and "volcano.sh/vgpu-memory" in resource.limit

```shell script
$ cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: gpu-pod1
  annotations:
    volcano.sh/vgpu-mode: "hami-core" # (Optional, 'hami-core' or 'mig')
spec:
  schedulerName: volcano
  containers:
    - name: cuda-container
      image: nvidia/cuda:9.0-devel
      command: ["sleep"]
      args: ["100000"]
      resources:
        limits:
          volcano.sh/vgpu-number: 2 # requesting 2 gpu cards
          volcano.sh/vgpu-memory: 3000 # (optinal)each vGPU uses 3G device memory
          volcano.sh/vgpu-cores: 50 # (optional)each vGPU uses 50% core  
EOF
```

You can validate device memory using nvidia-smi inside container:

![img](./doc/hard_limit.jpg)

> **WARNING:** *if you don't request GPUs when using the device plugin with NVIDIA images all
> the GPUs on the machine will be exposed inside your container.
> The number of vgpu used by a container can not exceed the number of gpus on that node.*
> You can specify the mode of this task by assigning `volcano.sh/vgpu-mode` annotations, If not, both modes are possible.

### Monitor

volcano-scheduler-metrics records every GPU usage and limitation, visit the following address to get these metrics.

```
curl {volcano scheduler cluster ip}:8080/metrics
```

You can also collect the **GPU utilization**, **GPU memory usage**, **pods' GPU memory limitations** and **pods' GPU memory usage** metrics on nodes by visiting the following addresses:

```
curl {volcano device plugin pod ip}:9394/metrics
```
![img](./doc/vgpu_device_plugin_metrics.png)

# Issues and Contributing
[Checkout the Contributing document!](CONTRIBUTING.md)

* You can report a bug by [filing a new issue](https://github.com/Project-HAMi/volcano-vgpu-device-plugin)
* You can contribute by opening a [pull request](https://help.github.com/articles/using-pull-requests/)


## Upgrading Kubernetes with the device plugin

Upgrading Kubernetes when you have a device plugin deployed doesn't require you to do any,
particular changes to your workflow.
The API is versioned and is pretty stable (though it is not guaranteed to be non breaking),
upgrading kubernetes won't require you to deploy a different version of the device plugin and you will
see GPUs re-registering themselves after you node comes back online.


Upgrading the device plugin is a more complex task. It is recommended to drain GPU tasks as
we cannot guarantee that GPU tasks will survive a rolling upgrade.
However we make best efforts to preserve GPU tasks during an upgrade.


## License
[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2FProject-HAMi%2Fvolcano-vgpu-device-plugin.svg?type=large)](https://app.fossa.com/projects/git%2Bgithub.com%2FProject-HAMi%2Fvolcano-vgpu-device-plugin?ref=badge_large)