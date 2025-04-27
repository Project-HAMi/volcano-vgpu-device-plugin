/*
Copyright 2023 The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package vgpu

import (
	"bufio"
	"fmt"
	"log"
	"os"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"volcano.sh/k8s-device-plugin/pkg/plugin/vgpu/config"
)

const (
	nvidiaProcDriverPath   = "/proc/driver/nvidia"
	nvidiaCapabilitiesPath = nvidiaProcDriverPath + "/capabilities"

	nvcapsProcDriverPath = "/proc/driver/nvidia-caps"
	nvcapsMigMinorsPath  = nvcapsProcDriverPath + "/mig-minors"
	nvcapsDevicePath     = "/dev/nvidia-caps"
)

// MIGCapableDevices stores information about all devices on the node
type MIGCapableDevices struct {
	// devicesMap holds a list of devices, separated by whether they have MigEnabled or not
	devicesMap map[bool][]*nvml.Device
}

// NewMIGCapableDevices creates a new MIGCapableDevices struct and returns a pointer to it.
func NewMIGCapableDevices() *MIGCapableDevices {
	return &MIGCapableDevices{
		devicesMap: nil, // Is initialized on first use
	}
}

func (devices *MIGCapableDevices) getDevicesMap() (map[bool][]*nvml.Device, error) {
	if devices.devicesMap == nil {
		n, ret := config.Nvml().DeviceGetCount()
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("error getting device count: %v", ret)
		}

		migEnabledDevicesMap := make(map[bool][]*nvml.Device)
		for i := 0; i < int(n); i++ {
			d, ret := config.Nvml().DeviceGetHandleByIndex(i)
			if ret != nvml.SUCCESS {
				return nil, fmt.Errorf("error getting device handle: %v", ret)
			}

			isMigEnabled, _, ret := config.Nvml().DeviceGetMigMode(d)
			if ret != nvml.SUCCESS {
				if ret == nvml.ERROR_NOT_SUPPORTED {
					isMigEnabled = nvml.DEVICE_MIG_DISABLE
				} else {
					return nil, fmt.Errorf("error getting MIG mode: %v", ret)
				}
			}

			migEnabledDevicesMap[isMigEnabled == 1] = append(migEnabledDevicesMap[isMigEnabled == 1], &d)
		}

		devices.devicesMap = migEnabledDevicesMap
	}
	return devices.devicesMap, nil
}

// GetDevicesWithMigEnabled returns a list of devices with migEnabled=true
func (devices *MIGCapableDevices) GetDevicesWithMigEnabled() ([]*nvml.Device, error) {
	devicesMap, err := devices.getDevicesMap()
	if err != nil {
		return nil, err
	}
	return devicesMap[true], nil
}

// GetDevicesWithMigDisabled returns a list of devices with migEnabled=false
func (devices *MIGCapableDevices) GetDevicesWithMigDisabled() ([]*nvml.Device, error) {
	devicesMap, err := devices.getDevicesMap()
	if err != nil {
		return nil, err
	}
	return devicesMap[false], nil
}

// AssertAllMigEnabledDevicesAreValid ensures that all devices with migEnabled=true are valid. This means:
// * The have at least 1 mig devices associated with them
// Returns nill if the device is valid, or an error if these are not valid
func (devices *MIGCapableDevices) AssertAllMigEnabledDevicesAreValid() error {
	devicesMap, err := devices.getDevicesMap()
	if err != nil {
		return err
	}

	for _, d := range devicesMap[true] {
		var migs []*nvml.Device
		maxMigDevices, ret := config.Nvml().DeviceGetMaxMigDeviceCount(*d)
		if ret != nvml.SUCCESS {
			return fmt.Errorf("error getting max MIG device count: %v", ret)
		}
		for i := 0; i < int(maxMigDevices); i++ {
			mig, ret := config.Nvml().DeviceGetMigDeviceHandleByIndex(*d, i)
			if ret == nvml.SUCCESS {
				migs = append(migs, &mig)
			}
		}
		if len(migs) == 0 {
			uuid, ret := config.Nvml().DeviceGetUUID(*d)
			if ret != nvml.SUCCESS {
				return fmt.Errorf("error getting device UUID: %v", ret)
			}
			return fmt.Errorf("no MIG devices associated with device: %v", uuid)
		}
	}
	return nil
}

// GetAllMigDevices returns a list of all MIG devices.
func (devices *MIGCapableDevices) GetAllMigDevices() ([]*nvml.Device, error) {
	devicesMap, err := devices.getDevicesMap()
	if err != nil {
		return nil, err
	}

	var migs []*nvml.Device
	for _, d := range devicesMap[true] {
		maxMigDevices, ret := config.Nvml().DeviceGetMaxMigDeviceCount(*d)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("error getting max MIG device count: %v", ret)
		}
		for i := 0; i < int(maxMigDevices); i++ {
			mig, ret := config.Nvml().DeviceGetMigDeviceHandleByIndex(*d, i)
			if ret == nvml.SUCCESS {
				migs = append(migs, &mig)
			}
		}
	}
	return migs, nil
}

// GetMigCapabilityDevicePaths returns a mapping of MIG capability path to device node path
func GetMigCapabilityDevicePaths() (map[string]string, error) {
	// Open nvcapsMigMinorsPath for walking.
	// If the nvcapsMigMinorsPath does not exist, then we are not on a MIG
	// capable machine, so there is nothing to do.
	// The format of this file is discussed in:
	//     https://docs.nvidia.com/datacenter/tesla/mig-user-guide/index.html#unique_1576522674
	minorsFile, err := os.Open(nvcapsMigMinorsPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error opening MIG minors file: %v", err)
	}
	defer minorsFile.Close()

	// Define a function to process each each line of nvcapsMigMinorsPath
	processLine := func(line string) (string, int, error) {
		var gpu, gi, ci, migMinor int

		// Look for a CI access file
		n, _ := fmt.Sscanf(line, "gpu%d/gi%d/ci%d/access %d", &gpu, &gi, &ci, &migMinor)
		if n == 4 {
			capPath := fmt.Sprintf(nvidiaCapabilitiesPath+"/gpu%d/mig/gi%d/ci%d/access", gpu, gi, ci)
			return capPath, migMinor, nil
		}

		// Look for a GI access file
		n, _ = fmt.Sscanf(line, "gpu%d/gi%d/access %d", &gpu, &gi, &migMinor)
		if n == 3 {
			capPath := fmt.Sprintf(nvidiaCapabilitiesPath+"/gpu%d/mig/gi%d/access", gpu, gi)
			return capPath, migMinor, nil
		}

		// Look for the MIG config file
		n, _ = fmt.Sscanf(line, "config %d", &migMinor)
		if n == 1 {
			capPath := fmt.Sprintf(nvidiaCapabilitiesPath + "/mig/config")
			return capPath, migMinor, nil
		}

		// Look for the MIG monitor file
		n, _ = fmt.Sscanf(line, "monitor %d", &migMinor)
		if n == 1 {
			capPath := fmt.Sprintf(nvidiaCapabilitiesPath + "/mig/monitor")
			return capPath, migMinor, nil
		}

		return "", 0, fmt.Errorf("unparsable line: %v", line)
	}

	// Walk each line of nvcapsMigMinorsPath and construct a mapping of nvidia
	// capabilities path to device minor for that capability
	capsDevicePaths := make(map[string]string)
	scanner := bufio.NewScanner(minorsFile)
	for scanner.Scan() {
		capPath, migMinor, err := processLine(scanner.Text())
		if err != nil {
			log.Printf("Skipping line in MIG minors file: %v", err)
			continue
		}
		capsDevicePaths[capPath] = fmt.Sprintf(nvcapsDevicePath+"/nvidia-cap%d", migMinor)
	}
	return capsDevicePaths, nil
}

// GetMigDeviceNodePaths returns a list of device node paths associated with a MIG device
func GetMigDeviceNodePaths(parent nvml.Device, mig *nvml.Device) ([]string, error) {
	capDevicePaths, err := GetMigCapabilityDevicePaths()
	if err != nil {
		return nil, fmt.Errorf("error getting MIG capability device paths: %v", err)
	}

	gpu, ret := parent.GetMinorNumber()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting GPU device minor number: %v", ret)
	}

	gi, ret := config.Nvml().DeviceGetGpuInstanceId(*mig)
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting MIG GPU instance ID: %v", ret)
	}

	ci, ret := config.Nvml().DeviceGetComputeInstanceId(*mig)
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting MIG compute instance ID: %v", ret)
	}

	giCapPath := fmt.Sprintf(nvidiaCapabilitiesPath+"/gpu%d/mig/gi%d/access", gpu, gi)
	if _, exists := capDevicePaths[giCapPath]; !exists {
		return nil, fmt.Errorf("missing MIG GPU instance capability path: %v", giCapPath)
	}

	ciCapPath := fmt.Sprintf(nvidiaCapabilitiesPath+"/gpu%d/mig/gi%d/ci%d/access", gpu, gi, ci)
	if _, exists := capDevicePaths[ciCapPath]; !exists {
		return nil, fmt.Errorf("missing MIG GPU instance capability path: %v", giCapPath)
	}

	devicePaths := []string{
		fmt.Sprintf("/dev/nvidia%d", gpu),
		capDevicePaths[giCapPath],
		capDevicePaths[ciCapPath],
	}

	return devicePaths, nil
}
