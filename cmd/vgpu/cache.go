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
	"os"
	"path/filepath"
	"time"

	"k8s.io/klog/v2"

	"volcano.sh/k8s-device-plugin/pkg/vgpucache"
)

const (
	defaultVGPUCacheRoot         = "/usr/local/vgpu/vgpu/containers"
	defaultVGPUCacheScanInterval = 5 * time.Second
	defaultVGPUCacheGracePeriod  = 5 * time.Minute
	vgpuCacheRootEnvName         = "HAMI_VGPU_CACHE_ROOT"
	vgpuCacheGracePeriodEnvName  = "HAMI_VGPU_CACHE_GRACE_PERIOD"
)

// resolveVGPUCacheConfig resolves the shared cache root and GC grace period from the deployment
// environment.
func resolveVGPUCacheConfig() vgpucache.Config {
	root := os.Getenv(vgpuCacheRootEnvName)
	if root == "" {
		hookPath := os.Getenv("HOOK_PATH")
		if hookPath == "" {
			root = defaultVGPUCacheRoot
		} else {
			root = filepath.Join(hookPath, "vgpu", "containers")
		}
	}

	gracePeriod := defaultVGPUCacheGracePeriod
	if raw := os.Getenv(vgpuCacheGracePeriodEnvName); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			klog.Warningf("Invalid %s value %q, using default %v", vgpuCacheGracePeriodEnvName, raw, gracePeriod)
		} else {
			gracePeriod = parsed
		}
	}
	return vgpucache.Config{
		Root:         root,
		ScanInterval: defaultVGPUCacheScanInterval,
		GracePeriod:  gracePeriod,
	}
}
