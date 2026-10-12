package nvidia

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testMapping(t *testing.T, dir string) *ContainerUsage {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0755))
	info, err := os.Stat(dir)
	require.NoError(t, err)
	data, err := syscall.Mmap(-1, 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	require.NoError(t, err)
	return &ContainerUsage{data: data, directoryInfo: info}
}

func TestExactPodAndContainerIdentity(t *testing.T) {
	pods := &corev1.PodList{Items: []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{UID: "pod"}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "gpu"}}}}}}
	require.True(t, isValidPod("pod_gpu", pods))
	for _, name := range []string{"otherpod_gpu", "pod_other", "pod", "_gpu", "pod_", "pod_gpu/child"} {
		require.False(t, isValidPod(name, pods), name)
	}
}

func TestReconcileOnlyUnmapsOrphans(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pod_gpu")
	usage := testMapping(t, dir)
	l := &ContainerLister{containerPath: root, containers: map[string]*ContainerUsage{"pod_gpu": usage}}
	require.NoError(t, l.reconcile(&corev1.PodList{}))
	require.Empty(t, l.containers)
	require.DirExists(t, dir, "monitor must never remove cache directories")
}

func TestReconcileWaitsForReaderAndDropsMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "containers")
	usage := testMapping(t, filepath.Join(root, "pod_gpu"))
	l := &ContainerLister{containerPath: root, containers: map[string]*ContainerUsage{"pod_gpu": usage}}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go l.WithContainers(func(cs map[string]*ContainerUsage) {
		close(entered)
		<-release
		cs["pod_gpu"].data[0] = 42 // Still mapped while a concurrent cleanup waits.
	})
	<-entered
	require.NoError(t, os.RemoveAll(root))
	go func() { finished <- l.reconcile(&corev1.PodList{}) }()
	select {
	case err := <-finished:
		t.Fatalf("cleanup escaped reader lock: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("cleanup did not resume")
	}
	require.Empty(t, l.containers)
}

func TestReconcileDropsReplacedDirectoryMapping(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pod_gpu")
	usage := testMapping(t, dir)
	l := &ContainerLister{containerPath: root, containers: map[string]*ContainerUsage{"pod_gpu": usage}}
	require.NoError(t, os.Rename(dir, dir+".old"))
	require.NoError(t, os.Mkdir(dir, 0755))
	pods := &corev1.PodList{Items: []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{UID: "pod"}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "gpu"}}}}}}
	require.NoError(t, l.reconcile(pods))
	require.Empty(t, l.containers, "old mmap must not survive replacement; new cache not created yet")
	require.DirExists(t, dir)
}
