// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildVulkanManifestMount_Present(t *testing.T) {
	tmp := t.TempDir()

	manifestDir := filepath.Join(tmp, "vulkan", "implicit_layer.d")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(manifestDir, "hami.json")
	if err := os.WriteFile(manifestPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	mounts := buildVulkanManifestMount(tmp)
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
	if mounts[0].ContainerPath != "/etc/vulkan/implicit_layer.d/hami.json" {
		t.Errorf("unexpected ContainerPath: %s", mounts[0].ContainerPath)
	}
	if mounts[0].HostPath != manifestPath {
		t.Errorf("unexpected HostPath: %s (want %s)", mounts[0].HostPath, manifestPath)
	}
	if !mounts[0].ReadOnly {
		t.Error("expected ReadOnly=true")
	}
}

func TestBuildVulkanManifestMount_Absent(t *testing.T) {
	tmp := t.TempDir()
	mounts := buildVulkanManifestMount(tmp)
	if len(mounts) != 0 {
		t.Errorf("expected 0 mounts when manifest absent, got %d", len(mounts))
	}
}
