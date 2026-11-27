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
*/

package mpsv3

import (
	"context"
	"fmt"
	"slices"
	"strings"

	v1 "k8s.io/api/core/v1"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"volcano.sh/k8s-device-plugin/pkg/util"
)

// HostCapabilities mirrors the capability probe results provided by the plugin runner.
type HostCapabilities struct {
	DriverVersion       string
	VidmemCapacityBytes map[string]uint64
}

// Allocator implements the Volcano AllocationBackend interface for NVIDIA MPS v3.
type Allocator struct {
	manager *Manager
}

// NewAllocator creates a new MPS v3 backend allocator.
func NewAllocator(mgr *Manager) *Allocator {
	return &Allocator{manager: mgr}
}

// Mode returns the unique operating mode identifier.
func (a *Allocator) Mode() string {
	return OperatingModeMPSV3
}

// Manager returns the underlying MPS v3 manager.
func (a *Allocator) Manager() *Manager {
	return a.manager
}

// Ready determines whether the host satisfies the prerequisites for MPS v3.
// Requires NVIDIA driver branch r620 (e.g. 620.xx.xx).
func (a *Allocator) Ready(host HostCapabilities) bool {
	if host.DriverVersion == "" {
		return false
	}
	// Driver branch must be r620
	if !strings.HasPrefix(host.DriverVersion, "620.") {
		return false
	}
	if a.manager == nil {
		return false
	}
	return true
}

// Allocate executes the MPS v3 allocation for a target container and devices.
func (a *Allocator) Allocate(ctx context.Context, pod *v1.Pod, ctr v1.Container, devs util.ContainerDevices, response *pluginapi.ContainerAllocateResponse) error {
	if a.manager == nil {
		return fmt.Errorf("MPS v3 manager is uninitialized")
	}
	if pod == nil || pod.UID == "" || ctr.Name == "" || len(devs) == 0 {
		return fmt.Errorf("MPS allocation requires valid Pod UID, container, and device list")
	}
	if err := ValidateContainerEnvironment(&ctr); err != nil {
		return err
	}

	mode, sms, smPolicy, err := ParseRequest(pod.Annotations)
	if err != nil {
		return fmt.Errorf("invalid MPS v3 request annotations: %w", err)
	}

	var gpus []string
	for _, d := range devs {
		if !strings.HasPrefix(d.UUID, "GPU-") {
			return fmt.Errorf("invalid GPU UUID %q for MPS v3", d.UUID)
		}
		if !slices.Contains(gpus, d.UUID) {
			gpus = append(gpus, d.UUID)
		}
	}
	slices.Sort(gpus)

	percent := int64(devs[0].Usedcores)
	if percent <= 0 {
		percent = 100
	}
	if sms > 0 && len(gpus) > 0 {
		if geom, ok := a.manager.Geometry(gpus[0]); ok {
			pct, pErr := geom.Percent(sms)
			if pErr == nil {
				percent = pct
			}
		}
	}

	memMiB := int64(devs[0].Usedmem)

	alloc := Allocation{
		ComputeMode: mode,
		SMPolicy:    smPolicy,
		PodUID:      string(pod.UID),
		Container:   ctr.Name,
		GPUs:        gpus,
		Memory:      memMiB,
		Percent:     percent,
		SMCount:     sms,
	}
	alloc.Server = alloc.ServerName()
	alloc.Namespace = HashName("ns", alloc.PodUID+"/"+alloc.Container)

	_, err = a.manager.Ensure(ctx, alloc)
	if err != nil {
		return fmt.Errorf("failed to ensure MPS v3 resources: %w", err)
	}

	if response.Envs == nil {
		response.Envs = make(map[string]string)
	}
	for k, v := range alloc.Envs() {
		response.Envs[k] = v
	}

	// Inject CDI device name
	cdiDeviceID := CDIKind + "=" + alloc.Namespace
	response.CdiDevices = append(response.CdiDevices, &pluginapi.CDIDevice{
		Name: cdiDeviceID,
	})

	return nil
}
