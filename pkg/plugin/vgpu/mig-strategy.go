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
	"fmt"
	"log"

	"github.com/NVIDIA/go-gpuallocator/gpuallocator"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"volcano.sh/k8s-device-plugin/pkg/plugin/vgpu/config"
	"volcano.sh/k8s-device-plugin/pkg/plugin/vgpu/util"
)

// Constants representing the various MIG strategies
const (
	MigStrategyNone   = "none"
	MigStrategySingle = "single"
	MigStrategyMixed  = "mixed"
)

// MigStrategyResourceSet holds a set of resource names for a given MIG strategy
type MigStrategyResourceSet map[string]struct{}

// MigStrategy provides an interface for building the set of plugins required to implement a given MIG strategy
type MigStrategy interface {
	GetPlugins(cache *DeviceCache) []*NvidiaDevicePlugin
	MatchesResource(mig *nvml.Device, resource string) bool
}

// NewMigStrategy returns a reference to a given MigStrategy based on the 'strategy' passed in
func NewMigStrategy(strategy string) (MigStrategy, error) {
	switch strategy {
	case MigStrategyNone:
		return &migStrategyNone{}, nil
	case MigStrategySingle:
		return &migStrategySingle{}, nil
	case MigStrategyMixed:
		return &migStrategyMixed{}, nil
	}
	return nil, fmt.Errorf("unknown strategy: %v", strategy)
}

type migStrategyNone struct{}
type migStrategySingle struct{}
type migStrategyMixed struct{}

// migStrategyNone
func (s *migStrategyNone) GetPlugins(cache *DeviceCache) []*NvidiaDevicePlugin {
	return []*NvidiaDevicePlugin{
		NewNvidiaDevicePlugin(
			//"nvidia.com/gpu",
			util.ResourceName,
			cache,
			gpuallocator.NewBestEffortPolicy(),
			pluginapi.DevicePluginPath+"nvidia-gpu.sock"),
		NewNvidiaDevicePlugin(
			util.ResourceMem,
			cache,
			gpuallocator.NewBestEffortPolicy(),
			pluginapi.DevicePluginPath+"nvidia-gpu-memory.sock"),
		NewNvidiaDevicePlugin(
			util.ResourceCores,
			cache,
			gpuallocator.NewBestEffortPolicy(),
			pluginapi.DevicePluginPath+"nvidia-gpu-cores.sock"),
	}
}

func (s *migStrategyNone) MatchesResource(mig *nvml.Device, resource string) bool {
	panic("Should never be called")
}

// migStrategySingle
func (s *migStrategySingle) GetPlugins(cache *DeviceCache) []*NvidiaDevicePlugin {
	panic("single mode in MIG currently not supported")
}

func (s *migStrategySingle) MatchesResource(mig *nvml.Device, resource string) bool {
	return true
}

// migStrategyMixed
func (s *migStrategyMixed) GetPlugins(cache *DeviceCache) []*NvidiaDevicePlugin {
	devices := NewMIGCapableDevices()

	if err := devices.AssertAllMigEnabledDevicesAreValid(); err != nil {
		panic(fmt.Errorf("at least one device with migEnabled=true was not configured correctly: %v", err))
	}

	resources := make(MigStrategyResourceSet)
	migs, err := devices.GetAllMigDevices()
	if err != nil {
		panic(fmt.Errorf("unable to retrieve list of MIG devices: %v", err))
	}
	for _, mig := range migs {
		// Convert old NVML device to new NVML device
		uuid, ret := (*mig).GetUUID()
		check(ret)
		newDevice, ret := config.Nvml().DeviceGetHandleByUUID(uuid)
		check(ret)

		r := s.getResourceName(&newDevice)
		if !s.validMigDevice(&newDevice) {
			log.Printf("Skipping unsupported MIG device: %v", r)
			continue
		}
		resources[r] = struct{}{}
	}

	plugins := []*NvidiaDevicePlugin{
		NewNvidiaDevicePlugin(
			util.ResourceName,
			cache,
			gpuallocator.NewBestEffortPolicy(),
			pluginapi.DevicePluginPath+"nvidia-gpu.sock"),
	}

	for resource := range resources {
		plugin := NewMIGNvidiaDevicePlugin(
			"nvidia.com/"+resource,
			NewMigDeviceManager(s, resource),
			"NVIDIA_VISIBLE_DEVICES",
			gpuallocator.Policy(nil),
			pluginapi.DevicePluginPath+"nvidia-"+resource+".sock")
		plugins = append(plugins, plugin)
	}

	return plugins
}

func (s *migStrategyMixed) validMigDevice(mig *nvml.Device) bool {
	gi, ret := config.Nvml().DeviceGetGpuInstanceId(*mig)
	check(ret)
	ci, ret := config.Nvml().DeviceGetComputeInstanceId(*mig)
	check(ret)
	return gi == ci
}

func (s *migStrategyMixed) getResourceName(mig *nvml.Device) string {
	gi, ret := config.Nvml().DeviceGetGpuInstanceId(*mig)
	check(ret)
	ci, ret := config.Nvml().DeviceGetComputeInstanceId(*mig)
	check(ret)

	memory, ret := config.Nvml().DeviceGetMemoryInfo(*mig)
	check(ret)
	gb := ((memory.Total/(1024*1024) + 1024 - 1) / 1024)

	var r string
	if gi == ci {
		r = fmt.Sprintf("mig-%dg.%dgb", gi, gb)
	} else {
		r = fmt.Sprintf("mig-%dc.%dg.%dgb", ci, gi, gb)
	}

	return r
}

func (s *migStrategyMixed) MatchesResource(mig *nvml.Device, resource string) bool {
	return s.getResourceName(mig) == resource
}
