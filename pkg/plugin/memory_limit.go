/*
Copyright 2026 The HAMi Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/package plugin

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"volcano.sh/k8s-device-plugin/pkg/rm"
	"volcano.sh/k8s-device-plugin/pkg/util"
)

// memoryLimitEnvs returns the CUDA_DEVICE_MEMORY_LIMIT_<i> variables for the
// devices of one container.
//
// HAMi-core applies the i-th limit to the i-th device that NVML lists inside
// the container, and NVML lists devices by PCI bus ID, which is the order of
// the host device index. The scheduler annotation can list the devices in any
// order, so the limits are written in host index order. When a device has no
// numeric index (for example a MIG device), the annotation order is kept.
func memoryLimitEnvs(devs util.ContainerDevices, devices rm.Devices, factor int32) map[string]string {
	envs := make(map[string]string, len(devs))
	for i, dev := range orderByDeviceIndex(devs, devices) {
		envs[fmt.Sprintf("CUDA_DEVICE_MEMORY_LIMIT_%v", i)] = fmt.Sprintf("%vm", dev.Usedmem*factor)
	}
	return envs
}

// orderByDeviceIndex returns a copy of devs sorted by host device index, or
// devs unchanged when any device has no numeric index.
func orderByDeviceIndex(devs util.ContainerDevices, devices rm.Devices) util.ContainerDevices {
	index := make(map[string]int, len(devices))
	for _, d := range devices {
		if n, err := strconv.Atoi(d.Index); err == nil {
			index[d.GetUUID()] = n
		}
	}
	for _, d := range devs {
		if _, ok := index[d.UUID]; !ok {
			return devs
		}
	}
	sorted := slices.Clone(devs)
	slices.SortStableFunc(sorted, func(a, b util.ContainerDevice) int {
		return cmp.Compare(index[a.UUID], index[b.UUID])
	})
	return sorted
}
