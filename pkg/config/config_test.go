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
	"testing"

	"github.com/stretchr/testify/require"
)

// resetNvml resets the NVML singleton state so tests can run independently.
func resetNvml() {
	nvmlOnce = sync.Once{}
	nvmlLibPath = ""
	nvmllib = nil
	globalDeviceOnce = sync.Once{}
	globalDevice = nil
}

func TestSetNvmlLibraryPath(t *testing.T) {
	t.Cleanup(resetNvml)

	path := "/fake/libnvidia-ml.so.1"
	SetNvmlLibraryPath(path)
	require.Equal(t, path, nvmlLibPath,
		"SetNvmlLibraryPath should store the given path")
}

func TestNvml_WithLibraryPath(t *testing.T) {
	t.Cleanup(resetNvml)

	path := "/fake/libnvidia-ml.so.1"
	SetNvmlLibraryPath(path)

	nv := Nvml()
	require.NotNil(t, nv,
		"Nvml() should return a non-nil interface when a library path is set")
}

func TestNvml_WithoutLibraryPath(t *testing.T) {
	t.Cleanup(resetNvml)

	// Do not call SetNvmlLibraryPath — nvmlLibPath stays empty.
	nv := Nvml()
	require.NotNil(t, nv,
		"Nvml() should still return a non-nil interface when no library path is set")
}

func TestSetNvmlLibraryPath_MustBeCalledBeforeNvml(t *testing.T) {
	t.Cleanup(resetNvml)

	// Call Nvml() first (captures empty nvmlLibPath at that point).
	_ = Nvml()

	// Now set a path — too late, sync.Once already fired.
	path := "/toolate/libnvidia-ml.so.1"
	SetNvmlLibraryPath(path)
	require.Equal(t, path, nvmlLibPath,
		"the path variable is updated even if Nvml() was already called")
	// The singleton itself was already created without the path,
	// demonstrating the "must be called before first Nvml()" contract.
}

func TestDevice_ReturnsNonNil(t *testing.T) {
	t.Cleanup(resetNvml)

	d := Device()
	require.NotNil(t, d,
		"Device() should return a non-nil interface")
}

func TestDevice_Idempotent(t *testing.T) {
	t.Cleanup(resetNvml)

	d1 := Device()
	d2 := Device()
	require.Same(t, d1, d2,
		"Device() should return the same singleton instance")
}
