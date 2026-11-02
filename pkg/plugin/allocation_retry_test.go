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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/rm"
	"volcano.sh/k8s-device-plugin/pkg/util"
	"volcano.sh/k8s-device-plugin/pkg/util/nodelock"
)

func TestAllocateResolutionErrorKeepsPodEligible(t *testing.T) {
	// Isolate the process-wide Kubernetes client without adding a global setter.
	const worker = "VOLCANO_ALLOCATION_RETRY_TEST"
	if os.Getenv(worker) != "1" {
		executable, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestAllocateResolutionErrorKeepsPodEligible$", "-test.v")
		cmd.Env = append(os.Environ(), worker+"=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}

	const nodeName, namespace, podName = "gpu-node", "default", "gpu-pod"
	requested := util.ContainerDevices{{UUID: "GPU-a[group1-0]", Type: util.NvidiaGPUDevice, Usedmem: 512, Usedcores: 50}}
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: namespace, Annotations: map[string]string{
			util.AssignedNodeAnnotations:          nodeName,
			util.AssignedIDsToAllocateAnnotations: util.EncodeContainerDevices(requested),
			util.DeviceBindPhase:                  util.DeviceBindAllocating,
		}},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "gpu"}}},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
	node := &corev1.Node{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Node"},
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Annotations: map[string]string{
			util.VGPUDeviceName: time.Now().Format(time.RFC3339),
			"other":             "unchanged",
		}},
	}
	cs := fake.NewClientset(pod, node)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result interface{}
		var err error
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/pods":
			var pods *corev1.PodList
			pods, err = cs.CoreV1().Pods("").List(r.Context(), metav1.ListOptions{})
			if err == nil {
				pods.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}
			}
			result = pods
		case "GET /api/v1/nodes/" + nodeName:
			result, err = cs.CoreV1().Nodes().Get(r.Context(), nodeName, metav1.GetOptions{})
		case "PUT /api/v1/nodes/" + nodeName:
			var updated corev1.Node
			var data []byte
			data, err = io.ReadAll(r.Body)
			if err == nil {
				_, _, err = scheme.Codecs.UniversalDeserializer().Decode(data, nil, &updated)
			}
			if err == nil {
				result, err = cs.CoreV1().Nodes().Update(r.Context(), &updated, metav1.UpdateOptions{})
			}
		case "GET /api/v1/namespaces/" + namespace + "/pods/" + podName:
			result, err = cs.CoreV1().Pods(namespace).Get(r.Context(), podName, metav1.GetOptions{})
		case "PATCH /api/v1/namespaces/" + namespace + "/pods/" + podName:
			var patch []byte
			patch, err = io.ReadAll(r.Body)
			if err == nil {
				result, err = cs.CoreV1().Pods(namespace).Patch(r.Context(), podName, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
			}
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("encode API response: %v", err)
		}
	}))
	defer server.Close()
	kubeconfig := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(kubeconfig, []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: %s
contexts:
- name: test
  context:
    cluster: test
current-context: test
`, server.URL)), 0600))
	t.Setenv("KUBECONFIG", kubeconfig)
	t.Setenv("NODE_NAME", nodeName)
	plugin := &nvidiaDevicePlugin{rm: &rm.ResourceManagerMock{
		ResourceFunc: func() spec.ResourceName { return spec.ResourceName(util.ResourceName) },
	}}
	resolutionErr := errors.New("temporary MIG UUID lookup failure")
	calls := 0
	resolve := func(devices util.ContainerDevices) ([]string, error) {
		calls++
		require.Equal(t, requested, devices)
		return nil, resolutionErr
	}
	request := &pluginapi.AllocateRequest{ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: []string{"GPU-a-0"}}}}
	for attempt := 0; attempt < 2; attempt++ {
		response, err := plugin.allocate(context.Background(), request, resolve)
		require.Nil(t, response)
		require.ErrorIs(t, err, resolutionErr)
		require.ErrorContains(t, err, "resolve allocated devices")
		updated, err := cs.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		require.NoError(t, err)
		require.NotContains(t, updated.Annotations, util.VGPUDeviceName)
		require.Equal(t, "unchanged", updated.Annotations["other"])
		retryPod, err := util.GetPendingPod(nodeName, 1)
		require.NoError(t, err, "the Pod must remain eligible after a transient resolution error")
		require.Equal(t, pod.Annotations, retryPod.Annotations)
		if attempt == 0 {
			require.NoError(t, nodelock.LockNode(nodeName, util.VGPUDeviceName))
		}
	}
	require.Equal(t, 2, calls, "both allocation attempts must reach device resolution")
	for _, action := range cs.Actions() {
		require.False(t, action.Matches("patch", "pods"), "resolution errors must not patch the Pod")
	}
}
