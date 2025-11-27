# Global Config

## Device Configs: ConfigMap

**Note:**
All the configurations listed below are managed within the `volcano-vgpu-device-config` ConfigMap.
You can update these configurations using the following methods:

1. Directly edit the ConfigMap: If `volcano-vgpu-device-plugin` has already been successfully installed, you can manually update the `volcano-vgpu-device-config` ConfigMap using the `kubectl edit` command to manually update the hami-scheduler-device ConfigMap.

```bash
kubectl edit configmap volcano-vgpu-device-config -n <namespace>
```

After making changes, restart the volcano-vgpu-device-plugin and volcano-scheduler to apply the updated configurations.

* `nvidia.deviceMemoryScaling`: 
  Float type, by default: 1. The ratio for NVIDIA device memory scaling, can be greater than 1 (enable virtual device memory, experimental feature). For NVIDIA GPU with *M* memory, if we set `nvidia.deviceMemoryScaling` argument to *S*, vGPUs splitted by this GPU will totally get `S * M` memory in Kubernetes with our device plugin.
* `nvidia.deviceSplitCount`: 
  Integer type, by default: equals 10. Maximum tasks assigned to a simple GPU device.
* `nvidia.migstrategy`: 
  String type, "none" for ignoring MIG features or "mixed" for allocating MIG device by seperate resources. Default "none"
* `nvidia.disablecorelimit`: 
  String type, "true" for disable core limit, "false" for enable core limit, default: false
* `nvidia.defaultMem`: 
  Integer type, by default: 0. The default device memory of the current task, in MB.'0' means use 100% device memory
* `nvidia.defaultCores`: 
  Integer type, by default: equals 0. Percentage of GPU cores reserved for the current task. If assigned to 0, it may fit in any GPU with enough device memory. If assigned to 100, it will use an entire GPU card exclusively.
* `nvidia.defaultGPUNum`: 
  Integer type, by default: equals 1, if configuration value is 0, then the configuration value will not take effect and will be filtered. When a user does not set nvidia.com/gpu this key in pod resource, webhook should check nvidia.com/gpumem、resource-mem-percentage、nvidia.com/gpucores these three keys, anyone a key having value, webhook should add nvidia.com/gpu key and this default value to resources limits map.
* `nvidia.resourceCountName`: 
  String type, vgpu number resource name, default: "volcano.sh/vgpu-number"
* `nvidia.resourceMemoryName`: 
  String type, vgpu memory size resource name, default: "volcano.sh/vgpu-memory"
* `nvidia.resourceMemoryPercentageName`: 
  String type, vgpu memory fraction resource name, default: "volcano.sh/vgpu-memory-percentage" 
* `nvidia.resourceCoreName`: 
  String type, vgpu cores resource name, default: "volcano.sh/vgpu-cores"

## Node Configs

**Note:**
volcano-vgpu-device-plugin allows for per-node configuration of the device plugin behavior, and all these settings are centrally managed within the volcano-vgpu-node-config ConfigMap. You can update them using the following methods:

```bash
kubectl edit configmap volcano-vgpu-node-config -n <namespace>
```

After making changes, restart the volcano-vgpu-device-plugin and volcano-scheduler to apply the updated configurations.

* `name`: the name of the node, the following parameters will only take effect on this node. 
* `operatingmode`: 
String type, `hami-core` for using hami-core for container resource limitation, `mig` for using mig for container resource limition (only available for on architect Ampere or later GPU)
* `devicememoryscaling`:
Integer type, device memory oversubscription on that node
* `devicecorescaling`: 
Integer type, device core oversubscription on that node 
* `devicesplitcount`: Allowed number of tasks sharing a device.
* `filterdevices`: Devices that are not registered to HAMi.
  * `uuid`: UUIDs of devices to ignore
  * `index`: Indexes of devices to ignore.
  * A device is ignored by HAMi if it's in `uuid` or `index` list.
