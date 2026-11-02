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
	"errors"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

type errorTestDevice struct {
	nvml.Device
	uuid      string
	uuidRet   nvml.Return
	memoryRet nvml.Return
}

func (d *errorTestDevice) GetUUID() (string, nvml.Return) { return d.uuid, d.uuidRet }
func (d *errorTestDevice) GetMemoryInfo() (nvml.Memory, nvml.Return) {
	return nvml.Memory{Total: 2 * 1024 * 1024}, d.memoryRet
}

type errorTestNVML struct {
	nvml.Interface
	device    nvml.Device
	migDevice nvml.Device
	countRet  nvml.Return
	handleRet nvml.Return
	migRet    nvml.Return
}

func (l *errorTestNVML) DeviceGetCount() (int, nvml.Return) { return 1, l.countRet }
func (l *errorTestNVML) DeviceGetHandleByIndex(int) (nvml.Device, nvml.Return) {
	return l.device, l.handleRet
}
func (l *errorTestNVML) DeviceGetHandleByUUID(string) (nvml.Device, nvml.Return) {
	return l.device, l.handleRet
}
func (l *errorTestNVML) DeviceGetMigDeviceHandleByIndex(nvml.Device, int) (nvml.Device, nvml.Return) {
	return l.migDevice, l.migRet
}

func TestGetDevicesReturnsErrors(t *testing.T) {
	for _, stage := range []string{"count", "handle", "uuid", "memory", "zero factor"} {
		t.Run(stage, func(t *testing.T) {
			dev := &errorTestDevice{uuid: "GPU-a"}
			lib := &errorTestNVML{device: dev}
			factor := uint(1)
			switch stage {
			case "count":
				lib.countRet = nvml.ERROR_UNKNOWN
			case "handle":
				lib.handleRet = nvml.ERROR_UNKNOWN
			case "uuid":
				dev.uuidRet = nvml.ERROR_UNKNOWN
			case "memory":
				dev.memoryRet = nvml.ERROR_UNKNOWN
			case "zero factor":
				factor = 0
			}
			devices, indices, err := getDevices(lib, factor)
			if err == nil || devices != nil || indices != nil {
				t.Fatalf("discovery failure returned devices=%v indices=%v error=%v", devices, indices, err)
			}
		})
	}
}

func TestGetDevicesSuccess(t *testing.T) {
	lib := &errorTestNVML{device: &errorTestDevice{uuid: "GPU-a"}}
	devices, indices, err := getDevices(lib, 1)
	if err != nil || len(devices) != 2 || len(indices) != 1 || indices[0] != "GPU-a" {
		t.Fatalf("devices=%v indices=%v error=%v", devices, indices, err)
	}
	if devices[0].ID != "0-0" || devices[1].ID != "0-1" || devices[0].Health != "Healthy" || devices[1].Health != "Healthy" {
		t.Fatalf("unexpected virtual devices: %+v", devices)
	}
}

func TestGetMigUUIDFromIndexErrorsAndFallback(t *testing.T) {
	smiErr := errors.New("nvidia-smi unavailable")
	for _, stage := range []string{"handle", "uuid", "smi failure", "smi success", "nvml success"} {
		t.Run(stage, func(t *testing.T) {
			dev := &errorTestDevice{uuid: "MIG-a"}
			lib := &errorTestNVML{device: dev, migDevice: dev}
			wantErr := stage != "smi success" && stage != "nvml success"
			if stage == "handle" {
				lib.handleRet = nvml.ERROR_UNKNOWN
			}
			if stage == "uuid" {
				dev.uuidRet = nvml.ERROR_UNKNOWN
			}
			if stage == "smi failure" || stage == "smi success" {
				lib.migRet = nvml.ERROR_NOT_SUPPORTED
			}
			calls := 0
			query := func() ([]byte, error) {
				calls++
				if stage == "smi failure" {
					return nil, smiErr
				}
				return []byte("GPU 0: A100 (UUID: GPU-a)\n  MIG 1g.5gb Device 0: (UUID: MIG-a)\n"), nil
			}
			got, err := getMigUUIDFromIndex(lib, "GPU-a[0]", 0, query)
			if (err != nil) != wantErr || (wantErr && got != "") || (!wantErr && got != "MIG-a") {
				t.Fatalf("UUID=%q error=%v; want error=%v", got, err, wantErr)
			}
			if stage == "smi failure" && !errors.Is(err, smiErr) {
				t.Fatalf("command error was not preserved: %v", err)
			}
			wantCalls := 0
			if stage == "smi failure" || stage == "smi success" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("nvidia-smi called %d times, want %d", calls, wantCalls)
			}
		})
	}
}

func TestGetMigUUIDFromSmiOutput(t *testing.T) {
	for _, tt := range []struct {
		name string
		line string
		want string
	}{
		{name: "valid", line: "MIG 1g.5gb Device 0: (UUID: MIG-a)", want: "MIG-a"},
		{name: "invalid index", line: "MIG 1g.5gb Device bad: (UUID: MIG-a)"},
		{name: "missing device", line: "MIG 1g.5gb (UUID: MIG-a)"},
		{name: "missing separator", line: "MIG 1g.5gb Device 0"},
		{name: "missing UUID", line: "MIG 1g.5gb Device 0: ()"},
		{name: "empty UUID", line: "MIG 1g.5gb Device 0: (UUID: )"},
		{name: "missing requested device", line: "MIG 1g.5gb Device 1: (UUID: MIG-b)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			output := "GPU 0: A100 (UUID: GPU-other)\n  MIG 1g.5gb Device 0: (UUID: MIG-other)\nGPU 1: A100 (UUID: GPU-a)\n  " + tt.line + "\n"
			got, err := GetMigUUIDFromSmiOutput(output, "GPU-a", 0)
			if got != tt.want || (err != nil) != (tt.want == "") {
				t.Fatalf("UUID=%q error=%v; want UUID=%q", got, err, tt.want)
			}
		})
	}
}
