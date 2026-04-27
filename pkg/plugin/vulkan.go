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

	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

// buildVulkanManifestMount returns a kubelet device-plugin Mount that exposes
// the HAMi Vulkan implicit layer manifest to the container. The manifest file
// is placed on the host by the device-plugin postStart lifecycle hook (which
// recursively copies /k8s-vgpu/lib/nvidia/. to HOOK_PATH). When the host file
// is absent the mount is skipped so we do not block pod startup on nodes that
// have not yet been populated.
//
// hostHookPath corresponds to the HOOK_PATH env (typically /usr/local/vgpu in
// this fork). The manifest path mirrors the directory layout shipped by the
// Dockerfile: <HOOK_PATH>/vulkan/implicit_layer.d/hami.json.
//
// Pods that opt into Vulkan partitioning by setting hami.io/vulkan="true"
// receive the layer activation env (HAMI_VULKAN_ENABLE=1) from the HAMi
// mutating webhook; the manifest's enable_environment guard then triggers the
// Vulkan layer load.
func buildVulkanManifestMount(hostHookPath string) []*pluginapi.Mount {
	vulkanManifestHost := hostHookPath + "/vulkan/implicit_layer.d/hami.json"
	if _, err := os.Stat(vulkanManifestHost); err != nil {
		return nil
	}
	return []*pluginapi.Mount{{
		ContainerPath: "/etc/vulkan/implicit_layer.d/hami.json",
		HostPath:      vulkanManifestHost,
		ReadOnly:      true,
	}}
}
