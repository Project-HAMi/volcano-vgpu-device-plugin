// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"volcano.sh/k8s-device-plugin/pkg/util"
)

// HostCapabilities contains the observations made during node startup.
type HostCapabilities struct {
	DriverVersion       string
	VidmemCapacityBytes map[string]uint64
}

// AllocationBackend couples readiness to the implementation that serves the request.
// Ready must cover all devices the node will advertise under this mode.
type AllocationBackend interface {
	Mode() string
	Ready(HostCapabilities) bool
	Allocate(context.Context, *v1.Pod, v1.Container, util.ContainerDevices, *pluginapi.ContainerAllocateResponse) error
}

// SelectBackend resolves a node-wide mode. The built-in paths have no separate allocator.
func SelectBackend(pin string, host HostCapabilities, probeOK bool, candidates ...AllocationBackend) (string, AllocationBackend, error) {
	switch pin {
	case "mig", "hami-core":
		return pin, nil, nil
	}
	var selected AllocationBackend
	for _, candidate := range candidates {
		if pin != "" && pin != candidate.Mode() {
			continue
		}
		if !probeOK || !candidate.Ready(host) {
			continue
		}
		if selected != nil {
			// No automatic preference is defined between ready backends.
			return "hami-core", nil, nil
		}
		selected = candidate
	}
	if selected != nil {
		return selected.Mode(), selected, nil
	}
	if pin != "" {
		return "", nil, fmt.Errorf("configured operatingmode %q has no ready allocation backend", pin)
	}
	return "hami-core", nil, nil
}
