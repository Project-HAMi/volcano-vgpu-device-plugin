/*
Copyright 2025 The Volcano Authors.

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
	"testing"

	"volcano.sh/k8s-device-plugin/pkg/plugin/vgpu/config"
)

type MigDeviceConfigs struct {
	Configs []map[string]int32
}

func TestProcessMigConfigs(t *testing.T) {
	type testCase struct {
		name        string
		migConfigs  map[string]config.MigConfigSpecSlice
		deviceCount int
		expectError bool
		validate    func(t *testing.T, result config.MigConfigSpecSlice)
	}

	testConfigs := MigDeviceConfigs{
		Configs: []map[string]int32{
			{
				"1g.10gb": 4,
				"2g.20gb": 1,
			},
			{
				"3g.30gb": 2,
			},
			{},
		},
	}

	testCases := []testCase{
		{
			name: "SingleConfigForAllDevices",
			migConfigs: map[string]config.MigConfigSpecSlice{
				"current": {
					{
						Devices:    []int32{},
						MigEnabled: true,
						MigDevices: testConfigs.Configs[1],
					},
				},
			},
			deviceCount: 3,
			expectError: false,
			validate: func(t *testing.T, result config.MigConfigSpecSlice) {
				if len(result) != 3 {
					t.Errorf("Expected 3 configs, got %d", len(result))
				}
				for i, config := range result {
					if len(config.Devices) != 1 || config.Devices[0] != int32(i) {
						t.Errorf("Config for device %d is incorrect: %v", i, config)
					}
					if !config.MigEnabled {
						t.Error("MigEnabled should be true")
					}
					if len(config.MigDevices) != 1 || config.MigDevices["3g.30gb"] != 2 {
						t.Error("MigDevices not preserved correctly")
					}
				}
			},
		},
		{
			name: "MultipleConfigsForSpecificDevicesWithNoEnabled",
			migConfigs: map[string]config.MigConfigSpecSlice{
				"current": {
					{
						Devices:    []int32{0, 1},
						MigEnabled: true,
						MigDevices: testConfigs.Configs[0],
					},
					{
						Devices:    []int32{2},
						MigEnabled: false,
						MigDevices: testConfigs.Configs[1],
					},
				},
			},
			deviceCount: 3,
			expectError: false,
			validate: func(t *testing.T, result config.MigConfigSpecSlice) {
				if len(result) != 3 {
					t.Errorf("Expected 3 configs, got %d", len(result))
				}
				for i := 0; i < 2; i++ {
					if len(result[i].Devices) != 1 || result[i].Devices[0] != int32(i) {
						t.Errorf("Config for device %d is incorrect: %v", i, result[i])
					}
					if !result[i].MigEnabled {
						t.Error("MigEnabled should be true for device", i)
					}
					if len(result[i].MigDevices) != 2 || (result[i].MigDevices["1g.10gb"] != 4 || result[i].MigDevices["2g.20gb"] != 1) {
						t.Error("MigDevices not preserved correctly for device", i)
					}
				}
				if len(result[2].Devices) != 1 || result[2].Devices[0] != 2 {
					t.Errorf("Config for device 2 is incorrect: %v", result[2])
				}
				if result[2].MigEnabled {
					t.Error("MigEnabled should be false for device 2")
				}
				if len(result[2].MigDevices) != 1 || result[2].MigDevices["3g.30gb"] != 2 {
					t.Error("MigDevices not preserved correctly for device 2")
				}
			},
		},
		{
			name: "MultipleConfigsForSpecificDevicesWithAllEnabled",
			migConfigs: map[string]config.MigConfigSpecSlice{
				"current": {
					{
						Devices:    []int32{0, 1},
						MigEnabled: true,
						MigDevices: testConfigs.Configs[0],
					},
					{
						Devices:    []int32{2},
						MigEnabled: true,
						MigDevices: testConfigs.Configs[1],
					},
				},
			},
			deviceCount: 3,
			expectError: false,
			validate: func(t *testing.T, result config.MigConfigSpecSlice) {
				if len(result) != 3 {
					t.Errorf("Expected 3 configs, got %d", len(result))
				}
				for i := 0; i < 2; i++ {
					if len(result[i].Devices) != 1 || result[i].Devices[0] != int32(i) {
						t.Errorf("Config for device %d is incorrect: %v", i, result[i])
					}
					if !result[i].MigEnabled {
						t.Error("MigEnabled should be true for device", i)
					}
					if len(result[i].MigDevices) != 2 || (result[i].MigDevices["1g.10gb"] != 4 || result[i].MigDevices["2g.20gb"] != 1) {
						t.Error("MigDevices not preserved correctly for device", i)
					}
				}
				if len(result[2].Devices) != 1 || result[2].Devices[0] != 2 {
					t.Errorf("Config for device 2 is incorrect: %v", result[2])
				}
				if !result[2].MigEnabled {
					t.Error("MigEnabled should be false for device 2")
				}
				if len(result[2].MigDevices) != 1 || result[2].MigDevices["3g.30gb"] != 2 {
					t.Error("MigDevices not preserved correctly for device 2")
				}
				t.Log(result)
			},
		},
		{
			name: "DeviceNotMatched",
			migConfigs: map[string]config.MigConfigSpecSlice{
				"current": {
					{
						Devices:    []int32{0, 1},
						MigEnabled: true,
					},
				},
			},
			deviceCount: 3,
			expectError: true,
			validate:    nil,
		},
	}

	plugin := &NvidiaDevicePlugin{}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := plugin.processMigConfigs(tc.migConfigs, tc.deviceCount)

			if tc.expectError {
				if err == nil {
					t.Error("Expected error but got nil")
				}
				t.Log(err)
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if tc.validate != nil {
				tc.validate(t, result)
			}
		})
	}
}
