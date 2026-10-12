package plugin

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func TestStartFailurePreservesExistingSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugin.sock")
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	defer listener.Close()
	plugin := &nvidiaDevicePlugin{socket: path, nvml: countFailureNVML{}}
	require.ErrorContains(t, plugin.Start(""), "get NVML device count")
	connection, err := net.Dial("unix", path)
	require.NoError(t, err, "failed new instance must not unlink the old listener")
	require.NoError(t, connection.Close())
	require.Nil(t, plugin.server)
	require.Nil(t, plugin.stop)
}

type countFailureNVML struct{ nvml.Interface }

func (countFailureNVML) DeviceGetCount() (int, nvml.Return) { return 0, nvml.ERROR_UNINITIALIZED }
