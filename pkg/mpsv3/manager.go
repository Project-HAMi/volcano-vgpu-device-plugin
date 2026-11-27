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
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/klog/v2"
	cdspec "tags.cncf.io/container-device-interface/specs-go"
)

// CommandRunner defines the signature for invoking the MPS control binary.
type CommandRunner func(ctx context.Context, args ...string) (string, error)

// Manager manages host MPS v3 daemon interactions and active client allocations.
type Manager struct {
	mu               sync.Mutex
	root             string
	binary           string
	cdiRoot          string
	lock             *os.File
	run              CommandRunner
	policy           MPSV3NodeConfig
	allocations      map[string]Allocation
	geometry         map[string]MPSV3Geometry
	healthy          atomic.Bool
	incompatibleGPUs map[string]bool
	unhealthyGPUs    atomic.Pointer[map[string]bool]
	generation       uint64

	deviceAllocations atomic.Pointer[map[string]Allocation]
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

func WithRoot(root string) ManagerOption {
	return func(m *Manager) { m.root = root }
}

func WithBinary(binary string) ManagerOption {
	return func(m *Manager) { m.binary = binary }
}

func WithCDIRoot(cdiRoot string) ManagerOption {
	return func(m *Manager) { m.cdiRoot = cdiRoot }
}

func WithCommandRunner(runner CommandRunner) ManagerOption {
	return func(m *Manager) { m.run = runner }
}

func WithPolicy(policy MPSV3NodeConfig) ManagerOption {
	return func(m *Manager) { m.policy = policy }
}

func WithGeometry(geom map[string]MPSV3Geometry) ManagerOption {
	return func(m *Manager) { m.geometry = geom }
}

// Geometry returns the static SM partitioning geometry for a given GPU if configured.
func (m *Manager) Geometry(gpu string) (MPSV3Geometry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	geom, ok := m.geometry[gpu]
	return geom, ok
}

// NewManager creates a new Manager instance.
func NewManager(opts ...ManagerOption) *Manager {
	root := os.Getenv("HAMI_MPS_V3_ROOT")
	if root == "" {
		root = "/var/run/hami/mps-v3"
	}
	binary := os.Getenv("HAMI_MPS_V3_BINARY")
	if binary == "" {
		binary = "nvidia-cuda-mps-control"
	}
	cdiRoot := os.Getenv("HAMI_CDI_ROOT")
	if cdiRoot == "" {
		cdiRoot = "/var/run/cdi"
	}

	m := &Manager{
		root:             root,
		binary:           binary,
		cdiRoot:          cdiRoot,
		allocations:      make(map[string]Allocation),
		geometry:         make(map[string]MPSV3Geometry),
		incompatibleGPUs: make(map[string]bool),
		policy:           MPSV3NodeConfig{ComputeMode: ComputeModeNamespaceCap},
	}
	for _, opt := range opts {
		opt(m)
	}

	if m.run == nil {
		m.run = func(ctx context.Context, args ...string) (string, error) {
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(cctx, m.binary, append([]string{"-p", "3"}, args...)...)
			env := os.Environ()
			// Strip any user/runtime overrides of MPS control variables
			for _, key := range []string{
				"CUDA_MPS_PROTOCOL_VERSION", "CUDA_MPS_PIPE_DIRECTORY", "CUDA_MPS_LOG_DIRECTORY",
				"CUDA_VISIBLE_DEVICES", "CUDA_MPS_ACTIVE_THREAD_PERCENTAGE",
				"CUDA_MPS_SM_PARTITION", "CUDA_MPS_PINNED_DEVICE_MEM_LIMIT",
			} {
				env = slices.DeleteFunc(env, func(e string) bool { return strings.HasPrefix(e, key+"=") })
			}
			cmd.Env = append(env, "CUDA_MPS_PROTOCOL_VERSION=3", "CUDA_MPS_PIPE_DIRECTORY="+filepath.Join(m.root, "pipe"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("MPS v3 %v: %w: %s", args, err, strings.TrimSpace(string(out)))
			}
			return strings.TrimSpace(string(out)), nil
		}
	}
	return m
}

func (m *Manager) statePath() string { return filepath.Join(m.root, "allocations.json") }
func (m *Manager) cdiPath(ns string) string {
	return filepath.Join(m.cdiRoot, "hami-mps-v3-"+ns+".json")
}

func (m *Manager) save() error {
	data, err := json.Marshal(m.allocations)
	if err != nil {
		return err
	}
	if err := atomicMPSFile(m.statePath(), data, 0600); err != nil {
		return err
	}
	snapshot := maps.Clone(m.allocations)
	m.deviceAllocations.Store(&snapshot)
	return nil
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.statePath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved map[string]Allocation
	if err = json.Unmarshal(data, &saved); err != nil {
		return fmt.Errorf("read MPS allocation state: %w", err)
	}
	for key, a := range saved {
		if a.Version != StateVersion {
			return fmt.Errorf("unrecognized MPS allocation state version %d", a.Version)
		}
		if (a.ComputeMode != ComputeModeNamespaceCap && a.ComputeMode != ComputeModeServerTimeslice) ||
			(a.SMCount > 0 && a.ComputeMode != ComputeModeNamespaceCap) || (a.SMCount == 0 && a.SMPolicy.Elastic()) {
			return fmt.Errorf("invalid MPS compute policy in allocation state for %q", key)
		}
		if key != a.Namespace || a.PodUID == "" || a.Container == "" || a.Namespace != HashName("ns", a.PodUID+"/"+a.Container) ||
			len(a.GPUs) == 0 || !slices.IsSorted(a.GPUs) || a.Server != a.ServerName() || a.Memory <= 0 || a.Percent < 1 || a.Percent > 100 || a.SMCount < 0 {
			return fmt.Errorf("invalid MPS allocation state for %q", key)
		}
	}
	if saved != nil {
		m.allocations = saved
	}
	snapshot := maps.Clone(m.allocations)
	m.deviceAllocations.Store(&snapshot)
	return nil
}

// Close releases the lock file and closes the manager.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lock != nil {
		_ = m.lock.Close()
		m.lock = nil
	}
}

// Start acquires the manager lock, loads state, and verifies daemon connectivity.
func (m *Manager) Start(ctx context.Context, livePods map[string]bool) (retErr error) {
	if !filepath.IsAbs(m.root) || filepath.Clean(m.root) != m.root || m.root == "/" {
		return fmt.Errorf("MPS root must be an absolute clean dedicated host path: %q", m.root)
	}
	resolved, err := m.policy.Resolve()
	if err != nil {
		return fmt.Errorf("invalid MPS v3 policy: %w", err)
	}
	m.policy = resolved
	for _, p := range []string{m.root, m.cdiRoot} {
		if err := os.MkdirAll(p, 0755); err != nil {
			return err
		}
	}
	m.lock, err = os.OpenFile(filepath.Join(m.root, "manager.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			m.Close()
		}
	}()
	if err = flock(m.lock); err != nil {
		return fmt.Errorf("another MPS manager owns %s: %w", m.root, err)
	}
	if err = m.load(); err != nil {
		return err
	}
	if err = m.Recover(ctx, livePods); err != nil {
		return err
	}
	m.healthy.Store(true)
	return nil
}

// Recover synchronizes manager state against live pods on the node and external daemon state.
func (m *Manager) Recover(ctx context.Context, livePods map[string]bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, a := range m.allocations {
		if livePods[a.PodUID] && a.ComputeMode != m.policy.ComputeMode {
			return fmt.Errorf("MPS allocation %s uses a different compute mode; drain the node first", a.Namespace)
		}
	}
	if _, err := m.run(ctx, "compute-policy", "get", "timeslice"); err != nil {
		return fmt.Errorf("cannot connect to externally managed r620 MPS daemon at %s: %w", filepath.Join(m.root, "pipe"), err)
	}
	if m.policy.ComputeMode == ComputeModeServerTimeslice {
		if _, err := m.run(ctx, "compute-policy", "set", "timeslice", m.policy.Timeslice); err != nil {
			return fmt.Errorf("set MPS timeslice %s: %w", m.policy.Timeslice, err)
		}
		if m.policy.TimesliceYieldLevel != nil {
			if _, err := m.run(ctx, "compute-policy", "set", "timeslice-yield-level", strconv.Itoa(*m.policy.TimesliceYieldLevel)); err != nil {
				return fmt.Errorf("set MPS timeslice yield level %d: %w", *m.policy.TimesliceYieldLevel, err)
			}
		}
	}
	names, err := m.serverNamesLocked(ctx)
	if err != nil {
		return err
	}
	owned := make(map[string]bool, len(m.allocations))
	for _, a := range m.allocations {
		owned[a.Server] = true
	}
	for _, name := range names {
		if !owned[name] {
			return fmt.Errorf("unowned MPS server %s in daemon; drain the node first", name)
		}
	}
	unhealthy := make(map[string]bool)
	for key, a := range m.allocations {
		var allocErr error
		if !livePods[a.PodUID] || !a.Prepared || a.CleanupPending {
			allocErr = m.releaseLocked(ctx, key)
		} else if allocErr = m.checkLocked(ctx, a); allocErr == nil {
			allocErr = m.publishCDILocked(a)
		}
		if allocErr != nil {
			klog.ErrorS(allocErr, "Failed to recover MPS allocation", "namespace", key)
			for _, gpu := range a.GPUs {
				unhealthy[gpu] = true
			}
		}
	}
	m.unhealthyGPUs.Store(&unhealthy)
	return nil
}

func (m *Manager) serverNamesLocked(ctx context.Context) ([]string, error) {
	out, err := m.run(ctx, "server", "list", "--format=csv,noheader")
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(strings.NewReader(out))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, row := range rows {
		if len(row) > 0 && row[0] != "" {
			names = append(names, row[0])
		}
	}
	return names, nil
}

// Ensure provisions or reconfirms an MPS v3 allocation.
func (m *Manager) Ensure(ctx context.Context, a Allocation) (created bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, gpu := range a.GPUs {
		if !m.gpuHealthyLocked(gpu) {
			return false, fmt.Errorf("MPS v3 is unhealthy on %s", gpu)
		}
	}
	if a.ComputeMode != m.policy.ComputeMode {
		return false, fmt.Errorf("pod MPS mode %q does not match node mode %q", a.ComputeMode, m.policy.ComputeMode)
	}

	// Idempotency: reconfirm if matching prepared allocation already exists
	if old, ok := m.allocations[a.Namespace]; ok {
		if old.Prepared && !old.CleanupPending &&
			reflect.DeepEqual(old.GPUs, a.GPUs) &&
			old.Percent == a.Percent &&
			old.Memory == a.Memory &&
			old.SMCount == a.SMCount &&
			reflect.DeepEqual(old.SMPolicy, a.SMPolicy) {
			return false, m.publishCDILocked(old)
		}
		if !old.Prepared || old.CleanupPending {
			return false, fmt.Errorf("previous MPS allocation %s is incomplete/pending cleanup", a.Namespace)
		}
		return false, fmt.Errorf("MPS allocation %s already exists with different limits", a.Namespace)
	}

	// Validate socket path length: Linux Unix domain socket limit is 108 bytes
	socketPath := filepath.Join(m.root, "pipe", a.Server, a.Namespace, "control")
	if len(socketPath) >= 104 {
		return false, fmt.Errorf("MPS namespace socket path too long (%d chars): %s", len(socketPath), socketPath)
	}

	// Validate SM policy against known GPU geometries before persisting intent
	if a.SMCount > 0 {
		for _, gpu := range a.GPUs {
			geom, ok := m.geometry[gpu]
			if ok {
				if err := a.SMPolicy.Validate(geom, a.SMCount); err != nil {
					return false, fmt.Errorf("invalid MPS v3 SM policy on %s: %w", gpu, err)
				}
			}
		}
	}

	// Persist intent before executing daemon mutations
	m.generation++
	a.Version = StateVersion
	m.allocations[a.Namespace] = a
	if err := m.save(); err != nil {
		delete(m.allocations, a.Namespace)
		return false, err
	}

	servers, err := m.serverNamesLocked(ctx)
	if err != nil {
		delete(m.allocations, a.Namespace)
		_ = m.save()
		return false, err
	}
	serverExists := slices.Contains(servers, a.Server)

	rollback := func(origErr error) (bool, error) {
		_, _ = m.run(ctx, "namespace", "delete", a.Namespace, "--server="+a.Server)
		if a.SMCount > 0 {
			for _, gpu := range a.GPUs {
				_, _ = m.run(ctx, "sm-partition", "delete", a.PartitionName(gpu), "--server="+a.Server, "--device="+gpu)
			}
		}
		if !serverExists {
			_, _ = m.run(ctx, "server", "delete", a.Server)
		}
		delete(m.allocations, a.Namespace)
		_ = m.save()
		return false, origErr
	}

	if !serverExists {
		if _, err := m.run(ctx, "server", "create", a.Server, "--uid=0", "--locality-domains=false", "--allowed-devices="+strings.Join(a.GPUs, ",")); err != nil {
			return rollback(err)
		}
	}

	if a.ComputeMode == ComputeModeServerTimeslice {
		if _, err := m.run(ctx, "server", "set", a.Server, "--timeslice-pct="+strconv.FormatInt(a.Percent, 10)); err != nil {
			return rollback(err)
		}
	}

	// Create namespace
	if _, err := m.run(ctx, "namespace", "create", a.Namespace, "--server="+a.Server); err != nil {
		return rollback(err)
	}

	activePercent := a.Percent
	if a.SMCount > 0 || a.ComputeMode == ComputeModeServerTimeslice {
		activePercent = 100
	}
	if _, err := m.run(ctx, "namespace", "set", a.Namespace, "--server="+a.Server, "--active-thread-percentage="+strconv.FormatInt(activePercent, 10)); err != nil {
		return rollback(err)
	}

	// Configure static SM partitions if requested
	if a.SMCount > 0 {
		for _, gpu := range a.GPUs {
			geom := m.geometry[gpu]
			chunks, remainder, pErr := geom.Partition(a.SMCount)
			if pErr != nil {
				return rollback(pErr)
			}
			partName := a.PartitionName(gpu)
			args := []string{
				"sm-partition", "create", partName, "--server=" + a.Server,
				"--device=" + gpu, "--chunks=" + strconv.FormatInt(chunks, 10),
				"--include-remainder=" + strconv.FormatBool(remainder),
				"--shareable=" + strconv.FormatBool(a.SMPolicy.Shareable),
			}
			if a.SMPolicy.MaxChunks != "" {
				args = append(args, "--max-chunks="+a.SMPolicy.MaxChunks)
			}
			if _, err := m.run(ctx, args...); err != nil {
				return rollback(err)
			}
			if _, err := m.run(ctx, "namespace", "set", a.Namespace, "--server="+a.Server, "--device="+gpu, "--sm-partition="+partName); err != nil {
				return rollback(err)
			}
		}
	}

	a.Prepared = true
	m.allocations[a.Namespace] = a
	if err := m.publishCDILocked(a); err != nil {
		a.Prepared = false
		m.allocations[a.Namespace] = a
		return rollback(err)
	}
	if err := m.save(); err != nil {
		a.Prepared = false
		m.allocations[a.Namespace] = a
		return rollback(err)
	}
	return true, nil
}

// Release cleans up an allocation's daemon resources and CDI files.
func (m *Manager) Release(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.releaseLocked(ctx, key)
}

func (m *Manager) releaseLocked(ctx context.Context, key string) error {
	a, ok := m.allocations[key]
	if !ok {
		return nil
	}
	m.generation++
	servers, err := m.serverNamesLocked(ctx)
	if err != nil {
		a.CleanupPending = true
		m.allocations[key] = a
		_ = m.save()
		return fmt.Errorf("query servers for release: %w", err)
	}
	var cleanupErr error
	if slices.Contains(servers, a.Server) {
		// Non-forced deletion: daemon protects active clients
		if _, err := m.run(ctx, "namespace", "delete", a.Namespace, "--server="+a.Server); err != nil && !strings.Contains(err.Error(), "not found") {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete namespace %s: %w", a.Namespace, err))
		}
		if a.SMCount > 0 {
			for _, gpu := range a.GPUs {
				if _, err := m.run(ctx, "sm-partition", "delete", a.PartitionName(gpu), "--server="+a.Server, "--device="+gpu); err != nil && !strings.Contains(err.Error(), "not found") {
					cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete partition %s on %s: %w", a.PartitionName(gpu), gpu, err))
				}
			}
		}
		// If last allocation on this server, delete server
		last := true
		for otherKey, other := range m.allocations {
			if otherKey != key && other.Server == a.Server {
				last = false
				break
			}
		}
		if last {
			if _, err := m.run(ctx, "server", "delete", a.Server); err != nil && !strings.Contains(err.Error(), "not found") {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete server %s: %w", a.Server, err))
			}
		}
	}
	if cleanupErr != nil {
		a.CleanupPending = true
		m.allocations[key] = a
		_ = m.save()
		return cleanupErr
	}
	_ = m.removeCDILocked(a.Namespace)
	delete(m.allocations, key)
	return m.save()
}

func (m *Manager) publishCDILocked(a Allocation) error {
	specData, err := a.CDISpec(m.root)
	if err != nil {
		return err
	}
	return atomicMPSFile(m.cdiPath(a.Namespace), specData, 0644)
}

func (m *Manager) removeCDILocked(ns string) error {
	path := m.cdiPath(ns)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var spec cdspec.Spec
	if json.Unmarshal(data, &spec) != nil || spec.Kind != CDIKind || len(spec.Devices) != 1 || spec.Devices[0].Name != ns {
		return fmt.Errorf("refusing to remove unowned CDI file %s", ns)
	}
	return os.Remove(path)
}

func (m *Manager) checkLocked(ctx context.Context, a Allocation) error {
	out, err := m.run(ctx, "namespace", "get", a.Namespace, a.Server, "active-thread-percentage")
	if err != nil {
		return err
	}
	want := a.Percent
	if a.SMCount > 0 || a.ComputeMode == ComputeModeServerTimeslice {
		want = 100
	}
	p, err := strconv.ParseFloat(out, 64)
	if err != nil || p != float64(want) {
		return fmt.Errorf("namespace %s has limit %q, want %d", a.Namespace, out, want)
	}
	return nil
}

func (m *Manager) gpuHealthyLocked(gpu string) bool {
	unhealthy := m.unhealthyGPUs.Load()
	return m.healthy.Load() && !m.incompatibleGPUs[gpu] && (unhealthy == nil || !(*unhealthy)[gpu])
}

// GPUHealthy returns true if the specified GPU is reported healthy by MPS v3.
func (m *Manager) GPUHealthy(gpu string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gpuHealthyLocked(gpu)
}
