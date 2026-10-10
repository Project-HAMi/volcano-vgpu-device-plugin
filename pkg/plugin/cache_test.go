package plugin

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"volcano.sh/k8s-device-plugin/pkg/vgpucache"
)

func TestPrepareCacheReturnsDirectoryError(t *testing.T) {
	plugin := &nvidiaDevicePlugin{}
	_, err := plugin.prepareCache("pod", "gpu")
	require.ErrorContains(t, err, "not initialized")
	root := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(root, []byte("preserve"), 0600))
	list := func() ([]*corev1.Pod, error) { return nil, nil }
	plugin.cacheManager, err = vgpucache.New(vgpucache.Config{Root: root, ScanInterval: time.Second}, list, list)
	require.NoError(t, err)
	path, err := plugin.prepareCache("pod", "gpu")
	require.ErrorContains(t, err, "not a real directory")
	require.Empty(t, path)
}
