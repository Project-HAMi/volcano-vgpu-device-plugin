## Config the volcano device plugin binary

The volcano device plugin has a number of options that can be configured. These options can be configured as command line flags, environment variables, or via a config file when launching the device plugin. The following section explains these configurations.

### As command line flags or envvars

| Flag                     | Envvar                  | Default Value   |
|--------------------------|-------------------------|-----------------|
| `--gpu-memory-factor`    | `-`                     | `1`             |
| `--config-file`          | `$CONFIG_FILE`          | `""`            |

when starting volcano-device-plugin.yml, users can specify these parameters by adding args to the container 'volcano-device-plugin'.
For example: 
 - args: ["--gpu-memory-factor=10"] will let device plugin use 10 MiB memory blocks

The `--gpu-memory-factor` flag overrides only the device plugin's factor; Volcano
scheduler reads `gpuMemoryFactor` from the device ConfigMap (`volcano-vgpu-device-config`).
Keep both factors aligned and restart both the device plugin and
`volcano-scheduler` after changing the ConfigMap.

`volcano.sh/vgpu-memory` counts memory blocks rather than bytes. One block is
`gpu-memory-factor` MiB. In non-MIG (`hami-core`) mode, a request represents:

```text
memory limit per vGPU (MiB) = request * gpu-memory-factor
request = ceil(desired memory in MiB / gpu-memory-factor)
```

In MIG mode, the plugin does not set `CUDA_DEVICE_MEMORY_LIMIT_<i>`; the
allocated MIG slice geometry determines the available memory and may exceed the
requested size, so the formula above is not an exact limit in MIG mode.

The device plugin registers each block as a separate synthetic device. The
number of registered memory devices on a node is:

```text
sum(floor(each GPU's memory in MiB / gpu-memory-factor))
```

Keep this total at or below 60000. Kubelet uses the gRPC default receive limit
for the Device Plugin `ListAndWatch` stream, so a larger device list can be
dropped and leave the resource at zero. A single 80 GiB card needs a factor of
at least 2; a node with eight 80 GiB cards needs at least 11.

Use the same factor on nodes that expose the same resource name. Changing the
factor changes the meaning of existing Pod requests and requires those requests
to be converted to the new block size.

### Configuration Option Details
**`--gpu-memory-factor`(uint)**:
  the memory block size for vGPU shared memory virtual devices, in MiB

  `(default 1)`

  Set it with the `--gpu-memory-factor` command line flag or the Helm
  `gpuMemoryFactor` value. There is no `GPU_MEMORY_FACTOR` environment
  variable. Each block is 1 MiB by default. Nodes with more aggregate GPU
  memory can use a larger block size, such as 10 MiB or 100 MiB, to keep
  the registered device list within kubelet's gRPC receive limit.

**`CONFIG_FILE`**:
  point the plugin at a configuration file instead of relying on command line
  flags or environment variables

  `(default '')`

  The order of precedence for setting each option is (1) command line flag, (2)
  environment variable, (3) configuration file. In this way, one could use a
  pre-defined configuration file, but then override the values set in it at
  launch time. 
