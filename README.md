# Volcano device plugin for Kubernetes

**Note**:
This is based on [Nvidia Device Plugin](https://github.com/NVIDIA/k8s-device-plugin), it uses [HAMi-core](https://github.com/Project-HAMi/HAMi-core) to support hard isolation of GPU card.

And collaborate with volcano, it is possible to enable GPU sharing.

## Table of Contents

- [About](#about)
- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
  - [Preparing your GPU Nodes](#preparing-your-gpu-nodes)
  - [Enabling GPU Support in Kubernetes](#enabling-gpu-support-in-kubernetes)
  - [Running GPU Sharing Jobs](#running-gpu-sharing-jobs)
  - [Running GPU Number Jobs](#running-gpu-number-jobs)
- [Docs](#docs)
- [Issues and Contributing](#issues-and-contributing)


## About

The Volcano device plugin for Kubernetes is a Daemonset that allows you to automatically:
- Expose the number of GPUs on each node of your cluster
- Keep track of the health of your GPUs
- Run GPU enabled containers in your Kubernetes cluster.
- Enforce hard resource limit in container.

This repository contains Volcano's official implementation of the [Kubernetes device plugin](https://github.com/kubernetes/community/blob/master/contributors/design-proposals/resource-management/device-plugin.md).

## Prerequisites

The list of prerequisites for running the Volcano device plugin is described below:
* NVIDIA drivers ~= 384.81
* nvidia-docker version > 2.0 (see how to [install](https://github.com/NVIDIA/nvidia-docker) and it's [prerequisites](https://github.com/nvidia/nvidia-docker/wiki/Installation-\(version-2.0\)#prerequisites))
* docker configured with nvidia as the [default runtime](https://github.com/NVIDIA/nvidia-docker/wiki/Advanced-topics#default-runtime).
* Kubernetes version >= 1.10

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


### Configure scheduler

update the scheduler configuration:

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
      - name: predicates
      - name: proportion
      - name: nodeorder
      - name: binpack
```

For volcano v1.8.2-(v1.8.2 included), use the following configMap 
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
      - name: predicates
        arguments:
          predicate.VGPUEnable: true # enable vgpu
      - name: proportion
      - name: nodeorder
      - name: binpack
```

### Enabling GPU Support in Kubernetes

Once you have enabled this option on *all* the GPU nodes you wish to use,
you can then enable GPU support in your cluster by deploying the following Daemonset:

VGPU:
```
$ kubectl create -f volcano-vgpu-device-plugin.yml
```

### Verify environment is ready

Check the node status, it is ok if `volcano.sh/vgpu-number` is included in the allocatable resources.

> **Note** `volcano.sh/vgpu-memory` and `volcano.sh/vgpu-cores` won't be listed in the allocatable resources, because these are more like a parameter of `volcano.sh/vgpu-number` than a seperate resource. If you wish to keep track of these field, please use volcano metrics.

```shell script
$ kubectl get node {node name} -oyaml
...
status:
  addresses:
  - address: 172.17.0.3
    type: InternalIP
  - address: volcano-control-plane
    type: Hostname
  allocatable:
    cpu: "4"
    ephemeral-storage: 123722704Ki
    hugepages-1Gi: "0"
    hugepages-2Mi: "0"
    memory: 8174332Ki
    pods: "110"
    volcano.sh/gpu-number: "10"    # vGPU resource
  capacity:
    cpu: "4"
    ephemeral-storage: 123722704Ki
    hugepages-1Gi: "0"
    hugepages-2Mi: "0"
    memory: 8174332Ki
    pods: "110"
    volcano.sh/gpu-memory: "89424"
    volcano.sh/gpu-number: "10"   # vGPU resource
```

### Running VGPU Jobs

VGPU can be requested by both set "volcano.sh/vgpu-number" , "volcano.sh/vgpu-cores" and "volcano.sh/vgpu-memory" in resource.limit

```shell script
$ cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: gpu-pod1
spec:
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

> **WARNING:** *if you don't request GPUs when using the device plugin with NVIDIA images all
> the GPUs on the machine will be exposed inside your container.*

## Docs

Please note that:
- The number of vgpu used by a container can not exceed the number of gpus on that node.

### With Docker

#### Deploy as DaemonSet:

VGPU:
```shell
$ kubectl create -f nvidia-vgpu-device-plugin.yml
```

# Issues and Contributing
[Checkout the Contributing document!](CONTRIBUTING.md)

* You can report a bug by [filing a new issue](https://github.com/Project-HAMi/volcano-vgpu-device-plugin)
* You can contribute by opening a [pull request](https://help.github.com/articles/using-pull-requests/)

## Versioning

The version exactly matches with [Volcano](https://github.com/volcano-sh/volcano).

## Upgrading Kubernetes with the device plugin

Upgrading Kubernetes when you have a device plugin deployed doesn't require you to do any,
particular changes to your workflow.
The API is versioned and is pretty stable (though it is not guaranteed to be non breaking),
upgrading kubernetes won't require you to deploy a different version of the device plugin and you will
see GPUs re-registering themselves after you node comes back online.


Upgrading the device plugin is a more complex task. It is recommended to drain GPU tasks as
we cannot guarantee that GPU tasks will survive a rolling upgrade.
However we make best efforts to preserve GPU tasks during an upgrade.
