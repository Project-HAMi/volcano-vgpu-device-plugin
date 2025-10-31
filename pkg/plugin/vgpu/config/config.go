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
	ResourceCountName            string                 `yaml:"resourceCountName"`
	ResourceMemoryName           string                 `yaml:"resourceMemoryName"`
	ResourceCoreName             string                 `yaml:"resourceCoreName"`
	ResourceMemoryPercentageName string                 `yaml:"resourceMemoryPercentageName"`
	ResourcePriority             string                 `yaml:"resourcePriorityName"`
	OverwriteEnv                 bool                   `yaml:"overwriteEnv"`
	DefaultMemory                int32                  `yaml:"defaultMemory"`
	DefaultCores                 int32                  `yaml:"defaultCores"`
	DefaultGPUNum                int32                  `yaml:"defaultGPUNum"`
	DeviceSplitCount             uint                   `yaml:"deviceSplitCount"`
	DeviceMemoryScaling          float64                `yaml:"deviceMemoryScaling"`
	DeviceCoreScaling            float64                `yaml:"deviceCoreScaling"`
	DisableCoreLimit             bool                   `yaml:"disableCoreLimit"`
	MigGeometriesList            []AllowedMigGeometries `yaml:"knownMigGeometries"`
	GPUMemoryFactor              uint                   `yaml:"gpuMemoryFactor"`
}

var (
	nvmllib = nvml.New()

	lock         sync.Mutex
	globalDevice device.Interface
)

var (
	// DevicePluginFilterDevice need device-plugin filter this device, don't register this device.
	DevicePluginFilterDevice *FilterDevice
)

func Nvml() nvml.Interface {
	return nvmllib
}

func Device() device.Interface {
	if globalDevice != nil {
		return globalDevice
	}

	lock.Lock()
	defer lock.Unlock()

	globalDevice = device.New(nvmllib)
	return globalDevice
}

var (
	DeviceSplitCount   uint
	GPUMemoryFactor    uint
	Mode               string
	DeviceCoresScaling float64
	NodeName           string
	RuntimeSocketFlag  string
	DisableCoreLimit   bool
)

type MigTemplate struct {
	Name   string `yaml:"name"`
	Memory int32  `yaml:"memory"`
	Count  int32  `yaml:"count"`
}

type MigTemplateUsage struct {
	Name   string `json:"name,omitempty"`
	Memory int32  `json:"memory,omitempty"`
	InUse  bool   `json:"inuse,omitempty"`
}

type Geometry struct {
	Group     string        `yaml:"group"`
	Instances []MigTemplate `yaml:"geometries"`
}

type MIGS []MigTemplateUsage

type MigInUse struct {
	Index     int32
	UsageList MIGS
}

type AllowedMigGeometries struct {
	Models     []string   `yaml:"models"`
	Geometries []Geometry `yaml:"allowedGeometries"`
}

type Config struct {
	NvidiaConfig NvidiaConfig `yaml:"nvidia"`
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
