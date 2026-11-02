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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

func TestPodAllocationTrySuccessLockState(t *testing.T) {
	// Isolate the process-wide client while exercising the public wrapper.
	const worker = "VOLCANO_ALLOCATION_CONFIRMATION_TEST"
	if os.Getenv(worker) != "1" {
		executable, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestPodAllocationTrySuccessLockState$", "-test.v")
		cmd.Env = append(os.Environ(), worker+"=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}

	const nodeName, namespace, podName = "gpu-node", "default", "gpu-pod"
	var mu sync.Mutex
	var pod *v1.Pod
	var node *v1.Node
	var failRead bool
	var podGets, podPatches, nodeUpdates int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var result interface{}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/namespaces/" + namespace + "/pods/" + podName:
			podGets++
			if failRead {
				w.WriteHeader(http.StatusServiceUnavailable)
				result = &metav1.Status{
					TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
					Status:   metav1.StatusFailure, Reason: metav1.StatusReasonServiceUnavailable,
					Code: http.StatusServiceUnavailable, Message: "injected Pod read failure",
				}
			} else {
				result = pod
			}
		case "PATCH /api/v1/namespaces/" + namespace + "/pods/" + podName:
			podPatches++
			var patch v1.Pod
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			for key, value := range patch.Annotations {
				pod.Annotations[key] = value
			}
			result = pod
		case "GET /api/v1/nodes/" + nodeName:
			result = node
		case "PUT /api/v1/nodes/" + nodeName:
			nodeUpdates++
			var updated v1.Node
			body, err := io.ReadAll(r.Body)
			if err == nil {
				_, _, err = scheme.Codecs.UniversalDeserializer().Decode(body, nil, &updated)
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			node = &updated
			result = node
		default:
			t.Errorf("unexpected Kubernetes request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
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

	for _, tt := range []struct {
		name     string
		pending  string
		failRead bool
		complete bool
	}{
		{name: "failed refresh retains lock", pending: "GPU-a,NVIDIA,512,50:", failRead: true},
		{name: "pending devices retain lock", pending: "GPU-a,NVIDIA,512,50:"},
		{name: "complete allocation releases lock", complete: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			initialPod := &v1.Pod{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
				ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: namespace, Annotations: map[string]string{
					AssignedIDsToAllocateAnnotations: tt.pending,
					DeviceBindPhase:                  DeviceBindAllocating,
				}},
			}
			initialNode := &v1.Node{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Node"},
				ObjectMeta: metav1.ObjectMeta{Name: nodeName, Annotations: map[string]string{
					VGPUDeviceName: time.Now().Format(time.RFC3339), "other": "unchanged",
				}},
			}
			mu.Lock()
			pod, node, failRead = initialPod.DeepCopy(), initialNode.DeepCopy(), tt.failRead
			podGets, podPatches, nodeUpdates = 0, 0, 0
			mu.Unlock()

			PodAllocationTrySuccess(nodeName, initialPod)

			mu.Lock()
			gotPod, gotNode := pod.DeepCopy(), node.DeepCopy()
			gets, patches, updates := podGets, podPatches, nodeUpdates
			mu.Unlock()
			require.Positive(t, gets, "completion must read the latest Pod")
			if tt.complete {
				require.Equal(t, 1, patches)
				require.Equal(t, 1, updates)
				require.Equal(t, DeviceBindSuccess, gotPod.Annotations[DeviceBindPhase])
				require.NotContains(t, gotNode.Annotations, VGPUDeviceName)
				require.Equal(t, "unchanged", gotNode.Annotations["other"])
			} else {
				require.Zero(t, patches, "unconfirmed allocation must not patch the Pod")
				require.Zero(t, updates, "unconfirmed allocation must not release the lock")
				require.Equal(t, initialPod.Annotations, gotPod.Annotations)
				require.Equal(t, initialNode.Annotations, gotNode.Annotations)
			}
		})
	}
}
