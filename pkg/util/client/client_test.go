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

package client

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"
)

// TestNewClientWithoutConfigReturnsError verifies that NewClient returns an error when no configuration is available.
func TestNewClientWithoutConfigReturnsError(t *testing.T) {
	t.Setenv("KUBECONFIG", t.TempDir()+"/missing")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	c, err := NewClient()
	if err == nil || c != nil {
		t.Fatalf("expected error and nil client, got client=%v err=%v", c, err)
	}
}

// TestSetClient verifies that SetClient overrides the singleton returned by GetClient.
func TestSetClient(t *testing.T) {
	t.Cleanup(ResetClient)

	fakeClient := fake.NewClientset()
	SetClient(fakeClient)
	require.Same(t, fakeClient, GetClient(), "GetClient should return the injected client")
}

// TestResetClient verifies that ResetClient clears the singleton client.
func TestResetClient(t *testing.T) {
	fakeClient := fake.NewClientset()
	SetClient(fakeClient)
	require.NotNil(t, GetClient())

	ResetClient()
	require.Nil(t, kubeClient)
}
