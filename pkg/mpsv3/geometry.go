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
	"encoding/json"
	"fmt"
)

// MPSV3Geometry captures the hardware multiprocessor properties for static SM partitioning.
type MPSV3Geometry struct {
	SMs   int64 `json:"sms"`
	Chunk int64 `json:"chunk"`
}

// Partition calculates the chunk count and remainder status for a requested SM count.
// Static SM allocations must exactly align to chunk boundaries, except when matching
// the device's physical non-power-of-two remainder.
func (g MPSV3Geometry) Partition(sms int64) (chunks int64, remainder bool, err error) {
	if g.SMs <= 0 || g.SMs > 100000 || g.Chunk <= 0 || sms <= 0 || sms > g.SMs || sms/g.Chunk == 0 {
		return 0, false, fmt.Errorf("SM count %d does not fit device geometry %+v", sms, g)
	}
	rem := sms % g.Chunk
	if rem != 0 && rem != g.SMs%g.Chunk {
		return 0, false, fmt.Errorf("SM count %d cannot be represented in %d-SM chunks", sms, g.Chunk)
	}
	return sms / g.Chunk, rem != 0, nil
}

// Percent maps a static SM allocation to its equivalent integer compute charge percentage (ceiling).
func (g MPSV3Geometry) Percent(sms int64) (int64, error) {
	if _, _, err := g.Partition(sms); err != nil {
		return 0, err
	}
	return (sms*100 + g.SMs - 1) / g.SMs, nil
}

// DeviceGeometry extracts and validates an MPSV3Geometry instance from a custom info map.
func DeviceGeometry(info map[string]any) (MPSV3Geometry, error) {
	var g MPSV3Geometry
	if info == nil {
		return g, fmt.Errorf("missing device custom info")
	}
	raw, ok := info[MPSV3GeometryKey]
	if !ok || raw == nil {
		return g, fmt.Errorf("missing MPS SM geometry key %s", MPSV3GeometryKey)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return g, err
	}
	err = json.Unmarshal(data, &g)
	if err != nil {
		return g, fmt.Errorf("failed to decode geometry: %w", err)
	}
	if g.SMs <= 0 || g.Chunk <= 0 {
		return g, fmt.Errorf("invalid MPS SM geometry %+v", g)
	}
	return g, nil
}
