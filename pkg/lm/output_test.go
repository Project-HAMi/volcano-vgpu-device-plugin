/**
# Copyright 2026 NVIDIA CORPORATION
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
**/

package lm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	nfdclientsetfake "sigs.k8s.io/node-feature-discovery/api/generated/clientset/versioned/fake"
	nfdv1alpha1 "sigs.k8s.io/node-feature-discovery/api/nfd/v1alpha1"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/flags"
)

// TestGetOwnerRefs verifies resolution of DaemonSet and Pod owner references.
func TestGetOwnerRefs(t *testing.T) {
	testCases := []struct {
		description      string
		podName          string
		namespace        string
		pod              *corev1.Pod
		expectedOwnerRef int
		expectError      bool
	}{
		{
			description:      "empty pod name returns nil",
			podName:          "",
			namespace:        "default",
			expectedOwnerRef: 0,
			expectError:      false,
		},
		{
			description: "pod owned by DaemonSet returns two owner refs",
			podName:     "gfd-pod",
			namespace:   "gpu-operator",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gfd-pod",
					Namespace: "gpu-operator",
					UID:       types.UID("pod-uid-123"),
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "DaemonSet",
							Name:       "nvidia-gfd",
							UID:        types.UID("ds-uid-456"),
						},
					},
				},
			},
			expectedOwnerRef: 2,
			expectError:      false,
		},
		{
			description: "pod not owned by anything returns nil",
			podName:     "standalone-pod",
			namespace:   "default",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "standalone-pod",
					Namespace: "default",
					UID:       types.UID("pod-uid-789"),
				},
			},
			expectedOwnerRef: 0,
			expectError:      false,
		},
		{
			description: "pod owned by ReplicaSet returns nil",
			podName:     "rs-pod",
			namespace:   "default",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "rs-pod",
					Namespace: "default",
					UID:       types.UID("pod-uid-abc"),
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "ReplicaSet",
							Name:       "my-replicaset",
							UID:        types.UID("rs-uid-def"),
						},
					},
				},
			},
			expectedOwnerRef: 0,
			expectError:      false,
		},
		{
			description:      "pod not found returns error",
			podName:          "nonexistent-pod",
			namespace:        "default",
			pod:              nil,
			expectedOwnerRef: 0,
			expectError:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			var client kubernetes.Interface
			if tc.pod != nil {
				client = fake.NewClientset(tc.pod)
			} else {
				client = fake.NewClientset()
			}

			ownerRefs, err := getOwnerReferences(context.Background(), client, tc.namespace, tc.podName)

			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Len(t, ownerRefs, tc.expectedOwnerRef)

			if tc.expectedOwnerRef == 2 {
				// Verify the DaemonSet owner ref is controller
				require.Equal(t, "DaemonSet", ownerRefs[0].Kind)
				require.NotNil(t, ownerRefs[0].Controller)
				require.True(t, *ownerRefs[0].Controller)
				require.Equal(t, tc.pod.OwnerReferences[0].Name, ownerRefs[0].Name)
				require.Equal(t, tc.pod.OwnerReferences[0].UID, ownerRefs[0].UID)

				// Verify the Pod owner ref
				require.Equal(t, "Pod", ownerRefs[1].Kind)
				require.Equal(t, tc.pod.Name, ownerRefs[1].Name)
				require.Equal(t, tc.pod.UID, ownerRefs[1].UID)
			}
		})
	}
}

// TestNewOutputer verifies NewOutputer creation with file and NodeFeature modes.
func TestNewOutputer(t *testing.T) {
	t.Run("file output mode when UseNodeFeatureAPI is false", func(t *testing.T) {
		useNFD := false
		outputFile := "/tmp/test-output"
		cfg := &spec.Config{
			Flags: spec.Flags{
				CommandLineFlags: spec.CommandLineFlags{
					UseNodeFeatureAPI: &useNFD,
					GFD: &spec.GFDCommandLineFlags{
						OutputFile: &outputFile,
					},
				},
			},
		}

		out, err := NewOutputer(context.Background(), cfg, flags.NodeConfig{}, flags.ClientSets{})
		require.NoError(t, err)
		require.NotNil(t, out)
	})

	t.Run("NodeFeature mode requires node name and namespace", func(t *testing.T) {
		useNFD := true
		cfg := &spec.Config{
			Flags: spec.Flags{
				CommandLineFlags: spec.CommandLineFlags{
					UseNodeFeatureAPI: &useNFD,
				},
			},
		}

		_, err := NewOutputer(context.Background(), cfg, flags.NodeConfig{}, flags.ClientSets{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "node-name")

		_, err = NewOutputer(context.Background(), cfg, flags.NodeConfig{Name: "node-1"}, flags.ClientSets{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "namespace")
	})

	t.Run("NodeFeature mode with valid config", func(t *testing.T) {
		useNFD := true
		cfg := &spec.Config{
			Flags: spec.Flags{
				CommandLineFlags: spec.CommandLineFlags{
					UseNodeFeatureAPI: &useNFD,
				},
			},
		}

		clientSets := flags.ClientSets{
			Core: fake.NewClientset(),
			NFD:  nfdclientsetfake.NewClientset(),
		}

		out, err := NewOutputer(context.Background(), cfg, flags.NodeConfig{
			Name:      "node-1",
			Namespace: "default",
		}, clientSets)
		require.NoError(t, err)
		require.NotNil(t, out)
	})
}

// TestNodeFeatureObject_Output verifies creating and updating NodeFeature CRs with context.
func TestNodeFeatureObject_Output(t *testing.T) {
	nfdClient := nfdclientsetfake.NewClientset()
	nfrStore := make(map[string]*nfdv1alpha1.NodeFeature)
	nfdClient.PrependReactor("get", "nodefeatures", func(action k8stesting.Action) (bool, runtime.Object, error) {
		getAction := action.(k8stesting.GetAction)
		nfr, ok := nfrStore[getAction.GetName()]
		if !ok {
			return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: "nfd.k8s-sigs.io", Resource: "nodefeatures"}, getAction.GetName())
		}
		return true, nfr.DeepCopy(), nil
	})
	nfdClient.PrependReactor("create", "nodefeatures", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createAction := action.(k8stesting.CreateAction)
		nfr := createAction.GetObject().(*nfdv1alpha1.NodeFeature).DeepCopy()
		nfrStore[nfr.Name] = nfr
		return true, nfr, nil
	})
	nfdClient.PrependReactor("update", "nodefeatures", func(action k8stesting.Action) (bool, runtime.Object, error) {
		updateAction := action.(k8stesting.UpdateAction)
		nfr := updateAction.GetObject().(*nfdv1alpha1.NodeFeature).DeepCopy()
		nfrStore[nfr.Name] = nfr
		return true, nfr, nil
	})

	obj := &nodeFeatureObject{
		nodeConfig: flags.NodeConfig{
			Name:      "test-node",
			Namespace: "default",
		},
		nfdClientset: nfdClient,
	}

	labels := Labels{
		"nvidia.com/gpu.count":   "1",
		"nvidia.com/gpu.product": "A100",
	}

	// 1. Initial creation
	err := obj.Output(context.Background(), labels)
	require.NoError(t, err)

	nfr, err := nfdClient.NfdV1alpha1().NodeFeatures("default").Get(context.Background(), "nvidia-features-for-test-node", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, labels, Labels(nfr.Spec.Labels))

	// 2. Idempotent call with no changes
	err = obj.Output(context.Background(), labels)
	require.NoError(t, err)

	// 3. Update with new labels
	updatedLabels := Labels{
		"nvidia.com/gpu.count":   "2",
		"nvidia.com/gpu.product": "A100",
	}
	err = obj.Output(context.Background(), updatedLabels)
	require.NoError(t, err)

	nfrUpdated, err := nfdClient.NfdV1alpha1().NodeFeatures("default").Get(context.Background(), "nvidia-features-for-test-node", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, updatedLabels, Labels(nfrUpdated.Spec.Labels))

	// 4. Cancelled context is respected
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err = obj.Output(canceledCtx, updatedLabels)
	require.Error(t, err)
}

// TestToFile_Output verifies atomic file writing and context cancellation.
func TestToFile_Output(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "labels.txt")
	out := ToFile(outPath)

	labels := Labels{
		"nvidia.com/gpu.count": "1",
	}

	// Successful write
	err := out.Output(context.Background(), labels)
	require.NoError(t, err)

	content, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "nvidia.com/gpu.count=1")

	// Cancelled context fails early
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err = out.Output(canceledCtx, labels)
	require.Error(t, err)
}
