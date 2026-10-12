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

package main

import (
	"testing"
	"time"
)

func TestResolveVGPUCacheConfigReusesExistingEnvironment(t *testing.T) {
	t.Setenv(vgpuCacheRootEnvName, "/var/lib/hami/containers")
	t.Setenv(vgpuCacheGracePeriodEnvName, "7m")
	config := resolveVGPUCacheConfig()
	if config.Root != "/var/lib/hami/containers" {
		t.Fatalf("Root = %q", config.Root)
	}
	if config.GracePeriod != 7*time.Minute {
		t.Fatalf("GracePeriod = %v", config.GracePeriod)
	}
	if config.ScanInterval != defaultVGPUCacheScanInterval {
		t.Fatalf("ScanInterval = %v", config.ScanInterval)
	}
}

func TestResolveVGPUCacheConfigDefaultsFromHookPath(t *testing.T) {
	t.Setenv(vgpuCacheRootEnvName, "")
	t.Setenv(vgpuCacheGracePeriodEnvName, "invalid")
	t.Setenv("HOOK_PATH", "/opt/hami")
	config := resolveVGPUCacheConfig()
	if config.Root != "/opt/hami/vgpu/containers" {
		t.Fatalf("Root = %q", config.Root)
	}
	if config.GracePeriod != defaultVGPUCacheGracePeriod {
		t.Fatalf("GracePeriod = %v", config.GracePeriod)
	}
}
