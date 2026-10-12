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

package nvidia

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func writeLifecycleCache(t *testing.T, path string) {
	t.Helper()
	// Four MiB covers the current v1 shared region, including all process slots.
	data := make([]byte, 4<<20)
	binary.LittleEndian.PutUint32(data, SharedRegionMagicFlag)
	binary.LittleEndian.PutUint32(data[4:], 1)
	require.NoError(t, os.WriteFile(path, data, 0600))
}

func newLifecycleLister(t *testing.T) (*ContainerLister, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "uid_main")
	require.NoError(t, os.Mkdir(dir, 0755))
	path := filepath.Join(dir, "x.cache")
	writeLifecycleCache(t, path)
	l := &ContainerLister{containerPath: root, containers: map[string]*ContainerUsage{}, clientset: fake.NewSimpleClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default", UID: "uid"}})}
	t.Cleanup(l.Close)
	require.NoError(t, l.Update())
	require.Len(t, l.containers, 1)
	return l, path
}

func TestCacheMappingReloadsChangedFile(t *testing.T) {
	for _, change := range []string{"replace", "resize"} {
		t.Run(change, func(t *testing.T) {
			l, path := newLifecycleLister(t)
			old := l.containers["uid_main"]
			if change == "replace" {
				require.NoError(t, os.Rename(path, filepath.Join(t.TempDir(), "old.cache")))
				writeLifecycleCache(t, path)
			} else {
				require.NoError(t, os.Truncate(path, 5<<20))
			}
			require.NoError(t, l.Update())
			current := l.containers["uid_main"]
			require.NotSame(t, old, current)
			require.True(t, cacheMappingIsCurrent(current))
		})
	}
}

func TestCacheMappingReleasedWhenDirectoryDisappears(t *testing.T) {
	for _, rootMissing := range []bool{false, true} {
		t.Run(map[bool]string{false: "container", true: "root"}[rootMissing], func(t *testing.T) {
			l, path := newLifecycleLister(t)
			old := l.containers["uid_main"]
			dir := filepath.Dir(path)
			if rootMissing {
				dir = l.containerPath
			}
			require.NoError(t, os.RemoveAll(dir))
			err := l.Update()
			if rootMissing {
				require.ErrorIs(t, err, os.ErrNotExist)
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, l.containers)
			require.Error(t, syscall.Munmap(old.data))
		})
	}
}

func TestMappedUsageHeldUntilUnlock(t *testing.T) {
	l, _ := newLifecycleLister(t)
	l.Lock()
	old := l.ListContainers()["uid_main"]
	// The same mutex excludes both mapping changes and other mapped-data users.
	require.False(t, l.mutex.TryLock())
	old.Info.SetRecentKernel(7)
	require.EqualValues(t, 7, old.Info.GetRecentKernel())
	l.UnLock()
	done := make(chan struct{})
	go func() { l.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after reader released lock")
	}
	l.Close()
	require.Empty(t, l.containers)
	require.Error(t, syscall.Munmap(old.data))
}

func TestMonitorLeavesOrphanDirectoryForPlugin(t *testing.T) {
	l, path := newLifecycleLister(t)
	require.NoError(t, l.clientset.CoreV1().Pods("default").Delete(context.Background(), "pod", metav1.DeleteOptions{}))
	old := time.Now().Add(-10 * time.Minute)
	require.NoError(t, os.Chtimes(filepath.Dir(path), old, old))
	require.NoError(t, l.Update())
	require.Empty(t, l.containers)
	require.FileExists(t, path)
}
