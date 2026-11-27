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
	"fmt"
)

const (
	// ComputeModeNamespaceCap partitions or caps compute within shared or dedicated MPS servers.
	ComputeModeNamespaceCap = "namespace-cap"
	// ComputeModeServerTimeslice provisions dedicated servers per container and balances timeslices.
	ComputeModeServerTimeslice = "server-timeslice"

	// OperatingModeMPSV3 represents the operating mode enum value in volcano-vgpu configuration.
	OperatingModeMPSV3 = "mps-v3"
)

// MPSV3NodeConfig defines the node-level MPS v3 execution settings.
type MPSV3NodeConfig struct {
	ComputeMode         string `json:"computeMode,omitempty" yaml:"computeMode,omitempty"`
	Timeslice           string `json:"timeslice,omitempty" yaml:"timeslice,omitempty"`
	TimesliceYieldLevel *int   `json:"timesliceYieldLevel,omitempty" yaml:"timesliceYieldLevel,omitempty"`
}

// Resolve normalizes configuration defaults and verifies mode-specific field constraints.
func (c MPSV3NodeConfig) Resolve() (MPSV3NodeConfig, error) {
	if c.ComputeMode == "" {
		c.ComputeMode = ComputeModeNamespaceCap
	}
	if c.ComputeMode == ComputeModeNamespaceCap {
		if c.Timeslice != "" || c.TimesliceYieldLevel != nil {
			return c, fmt.Errorf("MPS timeslice settings require %s", ComputeModeServerTimeslice)
		}
		return c, nil
	}
	if c.ComputeMode != ComputeModeServerTimeslice {
		return c, fmt.Errorf("unknown MPS compute mode %q", c.ComputeMode)
	}
	if c.Timeslice == "" {
		c.Timeslice = "default"
	}
	switch c.Timeslice {
	case "default", "short", "medium", "long":
	default:
		return c, fmt.Errorf("unknown MPS timeslice %q", c.Timeslice)
	}
	if c.TimesliceYieldLevel != nil && *c.TimesliceYieldLevel != 1 && *c.TimesliceYieldLevel != 2 {
		return c, fmt.Errorf("MPS timesliceYieldLevel must be omitted, 1 or 2")
	}
	return c, nil
}
