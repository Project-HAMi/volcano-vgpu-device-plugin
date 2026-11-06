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

package util

import (
	"strings"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"volcano.sh/k8s-device-plugin/pkg/config"
)

const sampleNvidiaSmiOutput = `GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-aaaa-1111)
  MIG 3g.20gb Device 0: (UUID: MIG-0000-aaaa)
  MIG 3g.20gb Device 1: (UUID: MIG-0001-bbbb)
GPU 1: NVIDIA A100-SXM4-40GB (UUID: GPU-bbbb-2222)
  MIG 1g.5gb Device 0: (UUID: MIG-1000-cccc)
  MIG 2g.10gb Device 1: (UUID: MIG-1001-dddd)
`

// TestGetMigUUIDFromSmiOutput_Success verifies parsing valid nvidia-smi -L outputs for multiple GPUs and MIG devices.
func TestGetMigUUIDFromSmiOutput_Success(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		uuid     string
		idx      int
		wantUUID string
	}{
		{
			name:     "GPU 0 Device 0",
			output:   sampleNvidiaSmiOutput,
			uuid:     "GPU-aaaa-1111",
			idx:      0,
			wantUUID: "MIG-0000-aaaa",
		},
		{
			name:     "GPU 0 Device 1",
			output:   sampleNvidiaSmiOutput,
			uuid:     "GPU-aaaa-1111",
			idx:      1,
			wantUUID: "MIG-0001-bbbb",
		},
		{
			name:     "GPU 1 Device 0",
			output:   sampleNvidiaSmiOutput,
			uuid:     "GPU-bbbb-2222",
			idx:      0,
			wantUUID: "MIG-1000-cccc",
		},
		{
			name:     "GPU 1 Device 1",
			output:   sampleNvidiaSmiOutput,
			uuid:     "GPU-bbbb-2222",
			idx:      1,
			wantUUID: "MIG-1001-dddd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetMigUUIDFromSmiOutput(tt.output, tt.uuid, tt.idx)
			require.NoError(t, err)
			assert.Equal(t, tt.wantUUID, got)
		})
	}
}

// TestGetMigUUIDFromSmiOutput_Errors verifies error reporting for missing devices, unknown UUIDs, non-numeric indices, and malformed lines.
func TestGetMigUUIDFromSmiOutput_Errors(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		uuid        string
		idx         int
		errContains string
	}{
		{
			name:        "device index not found",
			output:      sampleNvidiaSmiOutput,
			uuid:        "GPU-aaaa-1111",
			idx:         5,
			errContains: "MIG device with index 5 not found",
		},
		{
			name:        "GPU UUID not found",
			output:      sampleNvidiaSmiOutput,
			uuid:        "GPU-nonexistent",
			idx:         0,
			errContains: "not found for GPU GPU-nonexistent",
		},
		{
			name: "non-numeric device index",
			output: `GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-aaaa-1111)
  MIG 3g.20gb Device abc: (UUID: MIG-0000-aaaa)
`,
			uuid:        "GPU-aaaa-1111",
			idx:         0,
			errContains: "failed to parse MIG device index",
		},
		{
			name: "malformed MIG line missing UUID",
			output: `GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-aaaa-1111)
  MIG 3g.20gb Device 0
`,
			uuid:        "GPU-aaaa-1111",
			idx:         0,
			errContains: "missing UUID field",
		},
		{
			name: "malformed MIG line empty UUID field",
			output: `GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-aaaa-1111)
  MIG 3g.20gb Device 0: (UUID: )
`,
			uuid:        "GPU-aaaa-1111",
			idx:         0,
			errContains: "empty UUID field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetMigUUIDFromSmiOutput(tt.output, tt.uuid, tt.idx)
			require.Error(t, err)
			assert.Empty(t, got)
			assert.True(t, strings.Contains(err.Error(), tt.errContains),
				"expected error containing %q, got %v", tt.errContains, err)
		})
	}
}

// mockDevice implements nvml.Device for testing.
type mockDevice struct {
	nvml.Device
	name    string
	nameRet nvml.Return
	index   int
	idxRet  nvml.Return
	uuid    string
	uuidRet nvml.Return
}

// GetName returns the mocked device model name and return code.
func (m mockDevice) GetName() (string, nvml.Return) {
	return m.name, m.nameRet
}

// GetIndex returns the mocked device index and return code.
func (m mockDevice) GetIndex() (int, nvml.Return) {
	return m.index, m.idxRet
}

// GetUUID returns the mocked device UUID and return code.
func (m mockDevice) GetUUID() (string, nvml.Return) {
	return m.uuid, m.uuidRet
}

// mockNvml implements nvml.Interface with configurable function hooks for testing.
type mockNvml struct {
	nvml.Interface
	handleByUUIDFn func(uuid string) (nvml.Device, nvml.Return)
	migByIndexFn   func(device nvml.Device, index int) (nvml.Device, nvml.Return)
}

// DeviceGetHandleByUUID returns a mock device handle or error according to the configured hook.
func (m mockNvml) DeviceGetHandleByUUID(uuid string) (nvml.Device, nvml.Return) {
	if m.handleByUUIDFn != nil {
		return m.handleByUUIDFn(uuid)
	}
	return nil, nvml.ERROR_NOT_FOUND
}

// DeviceGetMigDeviceHandleByIndex returns a mock MIG device handle or error according to the configured hook.
func (m mockNvml) DeviceGetMigDeviceHandleByIndex(device nvml.Device, index int) (nvml.Device, nvml.Return) {
	if m.migByIndexFn != nil {
		return m.migByIndexFn(device, index)
	}
	return nil, nvml.ERROR_NOT_FOUND
}

// TestGetIndexAndTypeFromUUID_InvalidUUID verifies error return when NVML cannot find device handle by UUID.
func TestGetIndexAndTypeFromUUID_InvalidUUID(t *testing.T) {
	config.SetNvml(mockNvml{})
	t.Cleanup(config.ResetNvml)

	// Must return error, not panic
	_, _, err := GetIndexAndTypeFromUUID("invalid-gpu-uuid[0]")
	assert.Error(t, err)
}

// TestGetIndexAndTypeFromUUID_GetNameError verifies error return when NVML device name retrieval fails.
func TestGetIndexAndTypeFromUUID_GetNameError(t *testing.T) {
	config.SetNvml(mockNvml{
		handleByUUIDFn: func(uuid string) (nvml.Device, nvml.Return) {
			return mockDevice{nameRet: nvml.ERROR_UNKNOWN}, nvml.SUCCESS
		},
	})
	t.Cleanup(config.ResetNvml)

	_, _, err := GetIndexAndTypeFromUUID("GPU-1234[0]")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "device name")
}

// TestGetIndexAndTypeFromUUID_GetIndexError verifies error return when NVML device index retrieval fails.
func TestGetIndexAndTypeFromUUID_GetIndexError(t *testing.T) {
	config.SetNvml(mockNvml{
		handleByUUIDFn: func(uuid string) (nvml.Device, nvml.Return) {
			return mockDevice{name: "A100", nameRet: nvml.SUCCESS, idxRet: nvml.ERROR_UNKNOWN}, nvml.SUCCESS
		},
	})
	t.Cleanup(config.ResetNvml)

	_, _, err := GetIndexAndTypeFromUUID("GPU-1234[0]")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "device index")
}

// TestGetIndexAndTypeFromUUID_Success verifies successful retrieval of device model and index.
func TestGetIndexAndTypeFromUUID_Success(t *testing.T) {
	config.SetNvml(mockNvml{
		handleByUUIDFn: func(uuid string) (nvml.Device, nvml.Return) {
			return mockDevice{name: "NVIDIA A100-SXM4-40GB", nameRet: nvml.SUCCESS, index: 2, idxRet: nvml.SUCCESS}, nvml.SUCCESS
		},
	})
	t.Cleanup(config.ResetNvml)

	model, idx, err := GetIndexAndTypeFromUUID("GPU-1234[0]")
	require.NoError(t, err)
	assert.Equal(t, "NVIDIA A100-SXM4-40GB", model)
	assert.Equal(t, 2, idx)
}

// TestGetMigUUIDFromIndex_InvalidUUID verifies error return when GPU handle lookup fails for MIG index lookup.
func TestGetMigUUIDFromIndex_InvalidUUID(t *testing.T) {
	config.SetNvml(mockNvml{})
	t.Cleanup(config.ResetNvml)

	// Must return error, not panic
	_, err := GetMigUUIDFromIndex("invalid-gpu-uuid[0]", 0)
	assert.Error(t, err)
}

// TestGetMigUUIDFromIndex_MigGetUUIDError verifies error return when MIG device UUID retrieval fails.
func TestGetMigUUIDFromIndex_MigGetUUIDError(t *testing.T) {
	config.SetNvml(mockNvml{
		handleByUUIDFn: func(uuid string) (nvml.Device, nvml.Return) {
			return mockDevice{}, nvml.SUCCESS
		},
		migByIndexFn: func(device nvml.Device, index int) (nvml.Device, nvml.Return) {
			return mockDevice{uuidRet: nvml.ERROR_UNKNOWN}, nvml.SUCCESS
		},
	})
	t.Cleanup(config.ResetNvml)

	_, err := GetMigUUIDFromIndex("GPU-1234[0]", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mig device uuid")
}

// TestGetMigUUIDFromIndex_Success verifies successful MIG device UUID retrieval.
func TestGetMigUUIDFromIndex_Success(t *testing.T) {
	config.SetNvml(mockNvml{
		handleByUUIDFn: func(uuid string) (nvml.Device, nvml.Return) {
			return mockDevice{}, nvml.SUCCESS
		},
		migByIndexFn: func(device nvml.Device, index int) (nvml.Device, nvml.Return) {
			return mockDevice{uuid: "MIG-valid-uuid-1", uuidRet: nvml.SUCCESS}, nvml.SUCCESS
		},
	})
	t.Cleanup(config.ResetNvml)

	migUUID, err := GetMigUUIDFromIndex("GPU-1234[0]", 0)
	require.NoError(t, err)
	assert.Equal(t, "MIG-valid-uuid-1", migUUID)
}

