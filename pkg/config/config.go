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

package config

import (
	"sync"

	"github.com/NVIDIA/go-nvlib/pkg/nvlib/device"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

type NvidiaConfig struct {
	ResourceCountName            string                 `json:"resourceCountName" yaml:"resourceCountName"`
	ResourceMemoryName           string                 `json:"resourceMemoryName" yaml:"resourceMemoryName"`
	ResourceCoreName             string                 `json:"resourceCoreName" yaml:"resourceCoreName"`
	ResourceMemoryPercentageName string                 `json:"resourceMemoryPercentageName" yaml:"resourceMemoryPercentageName"`
	ResourcePriority             string                 `json:"resourcePriorityName" yaml:"resourcePriorityName"`
	OverwriteEnv                 bool                   `json:"overwriteEnv" yaml:"overwriteEnv"`
	DefaultMemory                int32                  `json:"defaultMemory" yaml:"defaultMemory"`
	DefaultCores                 int32                  `json:"defaultCores" yaml:"defaultCores"`
	DefaultGPUNum                int32                  `json:"defaultGPUNum" yaml:"defaultGPUNum"`
	DeviceSplitCount             uint                   `json:"deviceSplitCount" yaml:"deviceSplitCount"`
	DeviceMemoryScaling          float64                `json:"deviceMemoryScaling" yaml:"deviceMemoryScaling"`
	DeviceCoreScaling            float64                `json:"deviceCoreScaling" yaml:"deviceCoreScaling"`
	DisableCoreLimit             bool                   `json:"disableCoreLimit" yaml:"disableCoreLimit"`
	MigGeometriesList            []AllowedMigGeometries `json:"knownMigGeometries" yaml:"knownMigGeometries"`
	GPUMemoryFactor              uint                   `json:"gpuMemoryFactor" yaml:"gpuMemoryFactor"`
	// PassDeviceSpecs enables explicit device mounting via kubelet Device Plugin API
	// When true, device files are passed to kubelet via DeviceSpec, allowing containers
	// to access GPU devices without requiring nvidia-container-runtime
	PassDeviceSpecs bool `json:"passDeviceSpecs" yaml:"passDeviceSpecs"`
}

var (
	nvmllib     nvml.Interface
	nvmlOnce    sync.Once
	nvmlLibPath string

	globalDevice     device.Interface
	globalDeviceOnce sync.Once
)

var (
	// DevicePluginFilterDevice need device-plugin filter this device, don't register this device.
	DevicePluginFilterDevice *FilterDevice
)

// SetNvmlLibraryPath configures the library path for the NVML singleton.
// Must be called before the first call to Nvml().
func SetNvmlLibraryPath(path string) {
	nvmlLibPath = path
}

// SetNvml overrides the NVML interface singleton (primarily for testing).
func SetNvml(nv nvml.Interface) {
	nvmllib = nv
	nvmlOnce = sync.Once{}
	nvmlOnce.Do(func() {})
}

// ResetNvml resets the NVML singleton (primarily for testing).
func ResetNvml() {
	nvmllib = nil
	nvmlOnce = sync.Once{}
	nvmlLibPath = ""
	globalDeviceOnce = sync.Once{}
	globalDevice = nil
}

// Nvml returns the NVML interface singleton.
func Nvml() nvml.Interface {
	nvmlOnce.Do(func() {
		if nvmlLibPath != "" {
			nvmllib = nvml.New(nvml.WithLibraryPath(nvmlLibPath))
		} else {
			nvmllib = nvml.New()
		}
	})
	return nvmllib
}

// Device returns the device interface singleton.
func Device() device.Interface {
	globalDeviceOnce.Do(func() {
		globalDevice = device.New(Nvml())
	})
	return globalDevice
}

var (
	DeviceSplitCount   uint
	GPUMemoryFactor    uint
	Mode               string
	DeviceCoresScaling float64
	RuntimeSocketFlag  string
	DisableCoreLimit   bool
	// PassDeviceSpecs controls whether GPU device files are explicitly passed to kubelet
	// via the DeviceSpec field in ContainerAllocateResponse, enabling GPU access without
	// nvidia-container-runtime (compatible with standard OCI runtimes like containerd/docker)
	PassDeviceSpecs bool
	SchedulerConfig NvidiaConfig
)

type MigTemplate struct {
	Name   string `json:"name" yaml:"name"`
	Memory int32  `json:"memory" yaml:"memory"`
	Count  int32  `json:"count" yaml:"count"`
}

type MigTemplateUsage struct {
	Name   string `json:"name,omitempty"`
	Memory int32  `json:"memory,omitempty"`
	InUse  bool   `json:"inuse,omitempty"`
}

type Geometry struct {
	Group     string        `json:"group" yaml:"group"`
	Instances []MigTemplate `json:"geometries" yaml:"geometries"`
}

type MIGS []MigTemplateUsage

type MigInUse struct {
	Index     int32
	UsageList MIGS
}

type AllowedMigGeometries struct {
	Models     []string   `json:"models" yaml:"models"`
	Geometries []Geometry `json:"allowedGeometries" yaml:"allowedGeometries"`
}

type Config struct {
	NvidiaConfig NvidiaConfig `json:"nvidia" yaml:"nvidia"`
}

type MigPartedSpec struct {
	Version    string                        `json:"version"               yaml:"version"`
	MigConfigs map[string]MigConfigSpecSlice `json:"mig-configs,omitempty" yaml:"mig-configs,omitempty"`
}

// MigConfigSpec defines the spec to declare the desired MIG configuration for a set of GPUs.
type MigConfigSpec struct {
	DeviceFilter interface{}      `json:"device-filter,omitempty" yaml:"device-filter,flow,omitempty"`
	Devices      []int32          `json:"devices"                 yaml:"devices,flow"`
	MigEnabled   bool             `json:"mig-enabled"             yaml:"mig-enabled"`
	MigDevices   map[string]int32 `json:"mig-devices"             yaml:"mig-devices"`
}

// MigConfigSpecSlice represents a slice of 'MigConfigSpec'.
type MigConfigSpecSlice []MigConfigSpec

type FilterDevice struct {
	// UUID is the device ID.
	UUID []string `json:"uuid"`
	// Index is the device index.
	Index []uint `json:"index"`
}

type DevicePluginConfigs struct {
	Nodeconfig []struct {
		Name                string        `json:"name"`
		OperatingMode       string        `json:"operatingmode"`
		Devicememoryscaling float64       `json:"devicememoryscaling"`
		Devicecorescaling   float64       `json:"devicecorescaling"`
		Devicesplitcount    uint          `json:"devicesplitcount"`
		Migstrategy         string        `json:"migstrategy"`
		FilterDevice        *FilterDevice `json:"filterdevices"`
	} `json:"nodeconfig"`
}

var (
	filterOnce sync.Once
	uuidMap    map[string]struct{}
	indexMap   map[uint]struct{}
)

func FilterDeviceToRegister(uuid string, index int) bool {
	filterOnce.Do(initFilter)
	if len(uuidMap) == 0 && len(indexMap) == 0 {
		return false
	}

	if _, ok := uuidMap[uuid]; ok {
		return true
	}

	if _, ok := indexMap[uint(index)]; ok {
		return true
	}

	return false
}

func initFilter() {
	uuidMap = make(map[string]struct{})
	indexMap = make(map[uint]struct{})
	if DevicePluginFilterDevice == nil {
		return
	}

	if len(DevicePluginFilterDevice.UUID) > 0 {
		uuidMap = make(map[string]struct{}, len(DevicePluginFilterDevice.UUID))
		for _, u := range DevicePluginFilterDevice.UUID {
			uuidMap[u] = struct{}{}
		}
	}

	if len(DevicePluginFilterDevice.Index) > 0 {
		indexMap = make(map[uint]struct{}, len(DevicePluginFilterDevice.Index))
		for _, idx := range DevicePluginFilterDevice.Index {
			indexMap[idx] = struct{}{}
		}
	}
}
