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

package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/config"
	"volcano.sh/k8s-device-plugin/pkg/rm"
	"volcano.sh/k8s-device-plugin/pkg/util"
	"volcano.sh/k8s-device-plugin/pkg/vgpucache"
)

// The production Kubernetes client is process-scoped, so its API fixture must
// remain available across repeated test runs in the same process.
var allocationAPI struct {
	once   sync.Once
	mu     sync.Mutex
	pod    corev1.Pod
	node   corev1.Node
	server *httptest.Server
}

func TestMain(m *testing.M) {
	code := m.Run()
	if allocationAPI.server != nil {
		allocationAPI.server.Close()
	}
	os.Exit(code)
}

func TestAllocateCachePreparation(t *testing.T) {
	allocationAPI.once.Do(func() {
		allocationAPI.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allocationAPI.mu.Lock()
			defer allocationAPI.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/v1/pods":
				_ = json.NewEncoder(w).Encode(corev1.PodList{Items: []corev1.Pod{allocationAPI.pod}})
			case "/api/v1/namespaces/default/pods/pod":
				if r.Method == http.MethodPatch {
					var patch corev1.Pod
					if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					for k, v := range patch.Annotations {
						allocationAPI.pod.Annotations[k] = v
					}
				}
				_ = json.NewEncoder(w).Encode(allocationAPI.pod)
			case "/api/v1/nodes/node":
				if r.Method == http.MethodPut {
					data, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					if _, _, err := scheme.Codecs.UniversalDeserializer().Decode(data, nil, &allocationAPI.node); err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
				}
				_ = json.NewEncoder(w).Encode(allocationAPI.node)
			default:
				http.Error(w, "unexpected API path", http.StatusNotFound)
			}
		}))
	})
	kubeconfig := filepath.Join(t.TempDir(), "config")
	require.NoError(t, clientcmd.WriteToFile(clientcmdapi.Config{Clusters: map[string]*clientcmdapi.Cluster{"test": {Server: allocationAPI.server.URL}}, Contexts: map[string]*clientcmdapi.Context{"test": {Cluster: "test"}}, CurrentContext: "test"}, kubeconfig))
	t.Setenv("KUBECONFIG", kubeconfig)
	t.Setenv("NODE_NAME", "node")
	t.Setenv("HOOK_PATH", t.TempDir())
	mode := config.Mode
	config.Mode = "hami-core"
	t.Cleanup(func() { config.Mode = mode })
	annotation := util.EncodePodDevices(util.PodDevices{{{UUID: "GPU-test", Type: util.NvidiaGPUDevice, Usedmem: 128, Usedcores: 50}}})
	failure := errors.New("cache unavailable")
	for _, tc := range []string{"success", "prepare failure", "missing preparer"} {
		t.Run(tc, func(t *testing.T) {
			allocationAPI.mu.Lock()
			allocationAPI.pod = corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default", UID: "uid", Annotations: map[string]string{util.AssignedNodeAnnotations: "node", util.AssignedTimeAnnotations: "1", util.AssignedIDsToAllocateAnnotations: annotation, util.DeviceBindPhase: util.DeviceBindAllocating}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main"}}}, Status: corev1.PodStatus{Phase: corev1.PodPending}}
			allocationAPI.node = corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node", Annotations: map[string]string{util.VGPUDeviceName: "locked"}}}
			allocationAPI.mu.Unlock()
			root := t.TempDir()
			manager, err := vgpucache.New(vgpucache.Config{Root: root, ScanInterval: 1}, func() ([]*corev1.Pod, error) { return nil, nil }, func() ([]*corev1.Pod, error) { return nil, nil })
			require.NoError(t, err)
			preparer := manager.Prepare
			if tc == "prepare failure" {
				preparer = func(string, string) (string, error) { return "", failure }
			}
			if tc == "missing preparer" {
				preparer = nil
			}
			p := &nvidiaDevicePlugin{prepareCache: preparer, rm: &rm.ResourceManagerMock{ResourceFunc: func() spec.ResourceName { return spec.ResourceName(util.ResourceName) }}, config: &spec.Config{Flags: spec.Flags{CommandLineFlags: spec.CommandLineFlags{Plugin: &spec.PluginCommandLineFlags{DeviceIDStrategy: ptr(spec.DeviceIDStrategyUUID)}}}}, deviceListStrategies: spec.DeviceListStrategies{"envvar": true}}
			resp, err := p.Allocate(context.Background(), &pluginapi.AllocateRequest{ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: []string{"GPU-test"}}}})
			allocationAPI.mu.Lock()
			defer allocationAPI.mu.Unlock()
			if tc == "success" {
				require.NoError(t, err)
				require.Empty(t, allocationAPI.pod.Annotations[util.AssignedIDsToAllocateAnnotations])
				require.Equal(t, util.DeviceBindSuccess, allocationAPI.pod.Annotations[util.DeviceBindPhase])
				require.NotContains(t, allocationAPI.node.Annotations, util.VGPUDeviceName)
				require.DirExists(t, filepath.Join(root, "uid_main"))
				require.Len(t, resp.ContainerResponses, 1)
				found := false
				for _, mount := range resp.ContainerResponses[0].Mounts {
					if mount.ContainerPath == "/tmp/vgpu" {
						found = true
						require.Equal(t, filepath.Join(root, "uid_main"), mount.HostPath)
						require.False(t, mount.ReadOnly)
					}
				}
				require.True(t, found, "Allocate must mount the prepared cache directory")
			} else {
				require.Error(t, err)
				if tc == "prepare failure" {
					require.ErrorIs(t, err, failure)
				}
				require.Nil(t, resp)
				require.Equal(t, annotation, allocationAPI.pod.Annotations[util.AssignedIDsToAllocateAnnotations])
				require.Equal(t, util.DeviceBindFailed, allocationAPI.pod.Annotations[util.DeviceBindPhase])
			}
		})
	}
}
