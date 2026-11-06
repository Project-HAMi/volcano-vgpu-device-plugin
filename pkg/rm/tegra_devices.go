/**
# Copyright (c) 2022, NVIDIA CORPORATION.  All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
**/

package rm

import (
	"fmt"
	"os"
	"strings"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
)

const (
	tegraDeviceName = "tegra"
)

var (
	// tegraSerialPaths defines the sysfs and procfs locations where Tegra SoC serial numbers are exposed.
	tegraSerialPaths = []string{
		"/sys/devices/soc0/serial_number",
		"/proc/device-tree/serial-number",
	}
)

// buildTegraDeviceMap creates a DeviceMap for the tegra devices in the sytesm.
// NOTE: At present only a single tegra device is expected.
func buildTegraDeviceMap(config *spec.Config) (DeviceMap, error) {
	devices := make(DeviceMap)

	name := tegraDeviceName
	i := 0
	for _, resource := range config.Resources.GPUs {
		if resource.Pattern.Matches(name) {
			index := fmt.Sprintf("%d", i)
			err := devices.setEntry(resource.Name, index, &tegraDevice{})
			if err != nil {
				return nil, err
			}
			i++
		}

	}
	return devices, nil
}

type tegraDevice struct{}

var _ deviceInfo = (*tegraDevice)(nil)

// GetUUID returns the UUID of the tegra device.
// On Tegra/Jetson platforms, it queries the hardware SoC serial number from sysfs or procfs.
// If the serial number cannot be read, it falls back to the legacy "tegra" identifier.
func (d *tegraDevice) GetUUID() (string, error) {
	for _, p := range tegraSerialPaths {
		data, err := os.ReadFile(p)
		if err == nil {
			serial := strings.TrimSpace(strings.TrimRight(string(data), "\x00"))
			if serial != "" {
				return "tegra-" + serial, nil
			}
		}
	}
	return tegraDeviceName, nil
}

// GetPaths returns the paths for a tegra device.
// A tegra device does not have paths associated with it.
func (d *tegraDevice) GetPaths() ([]string, error) {
	return nil, nil
}

// GetNumaNode always returns unsupported for a Tegra device
func (d *tegraDevice) GetNumaNode() (bool, int, error) {
	return false, -1, nil
}

// GetTotalMemory is unsupported for a Tegra device.
func (d *tegraDevice) GetTotalMemory() (uint64, error) {
	return 0, nil
}

// GetComputeCapability is unimplemented for a Tegra device.
func (d *tegraDevice) GetComputeCapability() (string, error) {
	return "0.0", nil
}
