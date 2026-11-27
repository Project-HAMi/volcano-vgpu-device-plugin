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

package mpsv3

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type mockDaemonRunner struct {
	mu       sync.Mutex
	commands [][]string
	servers  []string
}

func newMockDaemonRunner() *mockDaemonRunner {
	return &mockDaemonRunner{
		servers: []string{},
	}
}

func (m *mockDaemonRunner) Run(ctx context.Context, args ...string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commands = append(m.commands, args)

	if len(args) == 0 {
		return "", nil
	}

	cmd := args[0]
	switch cmd {
	case "compute-policy":
		if len(args) >= 3 && args[1] == "get" && args[2] == "timeslice" {
			return "default", nil
		}
	case "server":
		if len(args) >= 2 {
			sub := args[1]
			switch sub {
			case "list":
				var b strings.Builder
				for _, s := range m.servers {
					b.WriteString(s + "\n")
				}
				return b.String(), nil
			case "create":
				srv := args[2]
				if !slices.Contains(m.servers, srv) {
					m.servers = append(m.servers, srv)
				}
				return "", nil
			case "delete":
				srv := args[2]
				m.servers = slices.DeleteFunc(m.servers, func(v string) bool { return v == srv })
				return "", nil
			case "set":
				return "", nil
			}
		}
	case "namespace":
		if len(args) >= 2 {
			sub := args[1]
			switch sub {
			case "create", "set", "delete":
				return "", nil
			case "get":
				// args: namespace get <ns> <srv> active-thread-percentage
				return "50", nil
			}
		}
	case "sm-partition":
		return "", nil
	}

	return "", nil
}

func TestManagerEnsureAndRelease(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mp")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	root := filepath.Join(tmpDir, "mps-v3")
	cdiRoot := filepath.Join(tmpDir, "cdi")

	mock := newMockDaemonRunner()
	mgr := NewManager(
		WithRoot(root),
		WithCDIRoot(cdiRoot),
		WithCommandRunner(mock.Run),
		WithPolicy(MPSV3NodeConfig{ComputeMode: ComputeModeNamespaceCap}),
		WithGeometry(map[string]MPSV3Geometry{
			"GPU-1": {SMs: 108, Chunk: 2},
		}),
	)
	ctx := context.Background()
	if err := mgr.Start(ctx, map[string]bool{}); err != nil {
		t.Fatalf("failed to start manager: %v", err)
	}
	defer mgr.Close()

	alloc := Allocation{
		ComputeMode: ComputeModeNamespaceCap,
		PodUID:      "pod-uid-1",
		Container:   "ctr-1",
		GPUs:        []string{"GPU-1"},
		Memory:      4096,
		Percent:     50,
	}
	alloc.Server = alloc.ServerName()
	alloc.Namespace = HashName("ns", alloc.PodUID+"/"+alloc.Container)

	// Phase 1: Ensure allocation
	created, err := mgr.Ensure(ctx, alloc)
	if err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}
	if !created {
		t.Fatalf("expected created to be true")
	}

	// Verify CDI spec was created
	cdiFile := filepath.Join(cdiRoot, "hami-mps-v3-"+alloc.Namespace+".json")
	if _, err := os.Stat(cdiFile); err != nil {
		t.Fatalf("expected CDI file %s to exist: %v", cdiFile, err)
	}

	// Verify state file
	if _, err := os.Stat(filepath.Join(root, "allocations.json")); err != nil {
		t.Fatalf("expected allocations.json to exist: %v", err)
	}

	// Phase 2: Idempotency (re-ensure identical request)
	created2, err := mgr.Ensure(ctx, alloc)
	if err != nil {
		t.Fatalf("second Ensure failed: %v", err)
	}
	if created2 {
		t.Fatalf("expected created to be false for duplicate Ensure")
	}

	// Phase 3: Release allocation
	if err := mgr.Release(ctx, alloc.Namespace); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	// Verify CDI file removed
	if _, err := os.Stat(cdiFile); !os.IsNotExist(err) {
		t.Fatalf("expected CDI file to be removed after release")
	}
}

func TestManagerStaticSMPartitionEnsure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mp")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	root := filepath.Join(tmpDir, "mps-v3")
	cdiRoot := filepath.Join(tmpDir, "cdi")

	mock := newMockDaemonRunner()
	mgr := NewManager(
		WithRoot(root),
		WithCDIRoot(cdiRoot),
		WithCommandRunner(mock.Run),
		WithPolicy(MPSV3NodeConfig{ComputeMode: ComputeModeNamespaceCap}),
		WithGeometry(map[string]MPSV3Geometry{
			"GPU-1": {SMs: 108, Chunk: 2},
		}),
	)
	ctx := context.Background()
	if err := mgr.Start(ctx, map[string]bool{}); err != nil {
		t.Fatalf("failed to start manager: %v", err)
	}
	defer mgr.Close()

	alloc := Allocation{
		ComputeMode: ComputeModeNamespaceCap,
		PodUID:      "pod-uid-2",
		Container:   "ctr-sm",
		GPUs:        []string{"GPU-1"},
		Memory:      8192,
		Percent:     19, // ~20 SMs
		SMCount:     20,
		SMPolicy:    MPSV3SMPolicy{Shareable: true, MaxChunks: "25"},
	}
	alloc.Server = alloc.ServerName()
	alloc.Namespace = HashName("ns", alloc.PodUID+"/"+alloc.Container)

	created, err := mgr.Ensure(ctx, alloc)
	if err != nil {
		t.Fatalf("Ensure failed for static SM: %v", err)
	}
	if !created {
		t.Fatalf("expected created = true")
	}

	// Verify sm-partition create command was issued
	foundSmPart := false
	for _, cmd := range mock.commands {
		if len(cmd) > 1 && cmd[0] == "sm-partition" && cmd[1] == "create" {
			foundSmPart = true
			if !slices.Contains(cmd, "--chunks=10") {
				t.Errorf("expected --chunks=10 in sm-partition command: %v", cmd)
			}
			if !slices.Contains(cmd, "--shareable=true") {
				t.Errorf("expected --shareable=true in sm-partition command: %v", cmd)
			}
			if !slices.Contains(cmd, "--max-chunks=25") {
				t.Errorf("expected --max-chunks=25 in sm-partition command: %v", cmd)
			}
		}
	}
	if !foundSmPart {
		t.Fatalf("sm-partition create command not found in executed commands")
	}
}

func TestManagerSocketPathLimit(t *testing.T) {
	// Create an artificially deep path to trigger the 104-char limit
	longRoot := "/var/run/this/is/a/very/long/nested/path/that/exceeds/the/unix/domain/socket/path/limit/for/mps/v3"
	mgr := NewManager(WithRoot(longRoot))
	mgr.healthy.Store(true)

	alloc := Allocation{
		ComputeMode: ComputeModeNamespaceCap,
		PodUID:      "pod-long-uid",
		Container:   "container-long-name",
		GPUs:        []string{"GPU-1"},
		Memory:      1024,
		Percent:     10,
	}
	alloc.Server = alloc.ServerName()
	alloc.Namespace = HashName("ns", alloc.PodUID+"/"+alloc.Container)

	_, err := mgr.Ensure(context.Background(), alloc)
	if err == nil || !strings.Contains(err.Error(), "socket path too long") {
		t.Fatalf("expected socket path too long error, got: %v", err)
	}
}
