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
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
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

func TestParseKubeAPIQPS(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want float32
	}{
		{name: "empty uses client-go default", raw: "", want: defaultKubeAPIQPS},
		{name: "whitespace uses default", raw: "  ", want: defaultKubeAPIQPS},
		{name: "historical default", raw: "5", want: 5},
		{name: "grey rollout override", raw: "100", want: 100},
		{name: "fractional override", raw: "2.5", want: 2.5},
		{name: "padded override", raw: " 200 ", want: 200},
		{name: "malformed string falls back", raw: "fast", want: defaultKubeAPIQPS},
		{name: "partial number falls back", raw: "12qps", want: defaultKubeAPIQPS},
		{name: "zero falls back", raw: "0", want: defaultKubeAPIQPS},
		{name: "negative falls back", raw: "-1", want: defaultKubeAPIQPS},
		{name: "NaN falls back", raw: "NaN", want: defaultKubeAPIQPS},
		{name: "Inf falls back", raw: "+Inf", want: defaultKubeAPIQPS},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseKubeAPIQPS(tt.raw); got != tt.want {
				t.Fatalf("parseKubeAPIQPS(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseKubeAPIBurst(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{name: "empty uses client-go default", raw: "", want: defaultKubeAPIBurst},
		{name: "whitespace uses default", raw: "  ", want: defaultKubeAPIBurst},
		{name: "historical default", raw: "10", want: 10},
		{name: "grey rollout override", raw: "200", want: 200},
		{name: "padded override", raw: " 300 ", want: 300},
		{name: "large integer override", raw: "1000000", want: 1000000},
		{name: "scientific notation falls back", raw: "1e+06", want: defaultKubeAPIBurst},
		{name: "malformed string falls back", raw: "many", want: defaultKubeAPIBurst},
		{name: "float string falls back", raw: "10.5", want: defaultKubeAPIBurst},
		{name: "zero falls back", raw: "0", want: defaultKubeAPIBurst},
		{name: "negative falls back", raw: "-10", want: defaultKubeAPIBurst},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseKubeAPIBurst(tt.raw); got != tt.want {
				t.Fatalf("parseKubeAPIBurst(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestKubeAPIRateLimits(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantQPS   float32
		wantBurst int
	}{
		{
			name:      "both unset keep client-go defaults",
			env:       map[string]string{},
			wantQPS:   defaultKubeAPIQPS,
			wantBurst: defaultKubeAPIBurst,
		},
		{
			name:      "both overridden",
			env:       map[string]string{"KUBE_API_QPS": "100", "KUBE_API_BURST": "200"},
			wantQPS:   100,
			wantBurst: 200,
		},
		{
			name:      "invalid values fall back per variable",
			env:       map[string]string{"KUBE_API_QPS": "nope", "KUBE_API_BURST": "-1"},
			wantQPS:   defaultKubeAPIQPS,
			wantBurst: defaultKubeAPIBurst,
		},
		{
			name:      "only qps overridden",
			env:       map[string]string{"KUBE_API_QPS": "20"},
			wantQPS:   20,
			wantBurst: defaultKubeAPIBurst,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }
			qps, burst := kubeAPIRateLimits(getenv)
			if qps != tt.wantQPS || burst != tt.wantBurst {
				t.Fatalf("kubeAPIRateLimits() = (%v, %d), want (%v, %d)", qps, burst, tt.wantQPS, tt.wantBurst)
			}
		})
	}
}

// newTestClient uses a real clientset configured solely against the local test server.
func newTestClient(t *testing.T, serverURL string) kubernetes.Interface {
	t.Helper()
	config := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %q
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user: {}
`, serverURL)
	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(path, []byte(config), 0600))
	t.Setenv("KUBECONFIG", path)
	client, err := NewClient()
	require.NoError(t, err)
	require.NotNil(t, client)
	return client
}

func TestNewClientRateLimitConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tests := []struct {
		name, qps, burst string
		wantQPS          float32
		wantBurst        int
	}{
		{name: "unset defaults", wantQPS: 5, wantBurst: 10},
		{name: "explicit defaults", qps: "5", burst: "10", wantQPS: 5, wantBurst: 10},
		{name: "invalid values fall back to defaults", qps: "NaN", burst: "-1", wantQPS: 5, wantBurst: 10},
		{name: "both overridden", qps: "0.01", burst: "3", wantQPS: 0.01, wantBurst: 3},
		{name: "qps only", qps: "0.01", wantQPS: 0.01, wantBurst: 10},
		{name: "burst only", burst: "4", wantQPS: 5, wantBurst: 4},
		{name: "invalid qps falls back independently", qps: "NaN", burst: "4", wantQPS: 5, wantBurst: 4},
		{name: "invalid burst falls back independently", qps: "0.01", burst: "-1", wantQPS: 0.01, wantBurst: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("KUBE_API_QPS", tt.qps)
			t.Setenv("KUBE_API_BURST", tt.burst)
			if tt.qps == "" {
				require.NoError(t, os.Unsetenv("KUBE_API_QPS"))
			}
			if tt.burst == "" {
				require.NoError(t, os.Unsetenv("KUBE_API_BURST"))
			}
			config := &rest.Config{}
			configureRateLimits(config, os.Getenv)
			require.Equal(t, tt.wantQPS, config.QPS)
			require.Equal(t, tt.wantBurst, config.Burst)
			client := newTestClient(t, server.URL)
			limiter := client.CoreV1().RESTClient().GetRateLimiter()
			appsLimiter := client.AppsV1().RESTClient().GetRateLimiter()
			discoveryLimiter := client.Discovery().RESTClient().GetRateLimiter()
			require.NotNil(t, limiter)
			require.NotNil(t, appsLimiter)
			require.NotNil(t, discoveryLimiter)
			require.Same(t, limiter, appsLimiter)
			require.Same(t, limiter, discoveryLimiter)
			require.Equal(t, tt.wantQPS, limiter.QPS())
			require.Equal(t, tt.wantQPS, appsLimiter.QPS())
			require.Equal(t, tt.wantQPS, discoveryLimiter.QPS())
		})
	}
}

func TestNewClientTypedClientsShareRateLimiter(t *testing.T) {
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		var body string
		switch r.URL.Path {
		case "/api/v1/namespaces/default/pods":
			body = `{"apiVersion":"v1","kind":"PodList","items":[]}`
		case "/apis/apps/v1/namespaces/default/deployments":
			body = `{"apiVersion":"apps/v1","kind":"DeploymentList","items":[]}`
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Errorf("write test response: %v", err)
		}
	}))
	defer server.Close()
	t.Setenv("KUBE_API_QPS", "0.01")
	t.Setenv("KUBE_API_BURST", "2")
	client := newTestClient(t, server.URL)
	coreLimiter := client.CoreV1().RESTClient().GetRateLimiter()
	appsLimiter := client.AppsV1().RESTClient().GetRateLimiter()
	discoveryLimiter := client.Discovery().RESTClient().GetRateLimiter()
	require.NotNil(t, coreLimiter)
	require.Same(t, coreLimiter, appsLimiter)
	require.Same(t, coreLimiter, discoveryLimiter)
	require.Equal(t, float32(0.01), coreLimiter.QPS())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.CoreV1().Pods("default").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	_, err = client.AppsV1().Deployments("default").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Equal(t, "/api/v1/namespaces/default/pods", <-requests)
	require.Equal(t, "/apis/apps/v1/namespaces/default/deployments", <-requests)
}
