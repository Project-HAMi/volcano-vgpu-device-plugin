package util

import (
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestGetPendingPodCanPickWrongOlderPod(t *testing.T) {
	const nodeName = "gpu-node-1"
	olderPod := podWithAllocatedDevice("mpi-pod", "default", "older-uid", nodeName, "100", "mpi", "GPU-old")
	rayPod := podWithAllocatedDevice("ray-pod", "default", "ray-uid", nodeName, "200", "ray-head", "GPU-ray")

	got := getOldestPod([]v1.Pod{*olderPod, *rayPod}, nodeName, 1)
	if got == nil {
		t.Fatal("expected getOldestPod to return a pod")
	}
	if got.Name != olderPod.Name {
		t.Fatalf("expected current heuristic to pick older pod %q, got %q", olderPod.Name, got.Name)
	}

	container, devices, err := GetNextDeviceRequest(NvidiaGPUDevice, *got)
	if err != nil {
		t.Fatalf("GetNextDeviceRequest returned error: %v", err)
	}
	if container.Name != "mpi" {
		t.Fatalf("expected wrong older pod container mpi, got %q", container.Name)
	}
	if len(devices) != 1 || devices[0].UUID != "GPU-old" {
		t.Fatalf("expected wrong older pod device GPU-old, got %#v", devices)
	}
}

func TestGetMatchingDeviceRequestUsesPodIdentityAndDeviceID(t *testing.T) {
	const nodeName = "gpu-node-1"
	olderPod := podWithAllocatedDevice("mpi-pod", "default", "older-uid", nodeName, "100", "mpi", "GPU-old")
	rayPod := podWithAllocatedDevice("ray-pod", "default", "ray-uid", nodeName, "200", "ray-head", "GPU-ray")

	pod, container, devices, err := GetMatchingDeviceRequest(NvidiaGPUDevice, nodeName, []string{"GPU-ray"}, []v1.Pod{*olderPod, *rayPod})
	if err != nil {
		t.Fatalf("GetMatchingDeviceRequest returned error: %v", err)
	}
	if pod == nil || pod.Name != rayPod.Name || pod.UID != rayPod.UID {
		t.Fatalf("expected ray pod %s/%s, got %#v", rayPod.Namespace, rayPod.Name, pod)
	}
	if container.Name != "ray-head" {
		t.Fatalf("expected ray-head container, got %q", container.Name)
	}
	if len(devices) != 1 || devices[0].UUID != "GPU-ray" {
		t.Fatalf("expected GPU-ray device, got %#v", devices)
	}

	t.Logf("FIXED: kubelet Allocate requested device IDs [GPU-ray] for the Ray container")
	t.Logf("FIXED: matcher selected pod %s/%s uid=%s", pod.Namespace, pod.Name, pod.UID)
	t.Logf("FIXED: selected container=%s selected device IDs=[%s]", container.Name, devices[0].UUID)
	t.Logf("FIXED: HostPath would be /tmp/vgpu/containers/%s_%s", pod.UID, container.Name)
}

func TestGetMatchingDeviceRequestRejectsAmbiguousDeviceID(t *testing.T) {
	const nodeName = "gpu-node-1"
	podA := podWithAllocatedDevice("pod-a", "default", "uid-a", nodeName, "100", "worker", "GPU-shared")
	podB := podWithAllocatedDevice("pod-b", "default", "uid-b", nodeName, "200", "worker", "GPU-shared")

	_, _, _, err := GetMatchingDeviceRequest(NvidiaGPUDevice, nodeName, []string{"GPU-shared"}, []v1.Pod{*podA, *podB})
	if err == nil {
		t.Fatal("expected ambiguous device request error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
}

func TestGetMatchingDeviceRequestRejectsIdentityMismatch(t *testing.T) {
	const nodeName = "gpu-node-1"
	pod := podWithAllocatedDevice("ray-pod", "default", "ray-uid", nodeName, "100", "ray-head", "GPU-ray")
	pod.Annotations[AssignedPodUIDAnnotations] = "stale-uid"

	_, _, _, err := GetMatchingDeviceRequest(NvidiaGPUDevice, nodeName, []string{"GPU-ray"}, []v1.Pod{*pod})
	if err == nil {
		t.Fatal("expected no match when pod identity annotations do not match the pod")
	}
	if !strings.Contains(err.Error(), "device request not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestGetMatchingDeviceRequestRejectsStaleVNodeAnnotation(t *testing.T) {
	const nodeName = "gpu-node-1"
	pod := podWithAllocatedDevice("stale-pod", "default", "stale-uid", "gpu-node-2", "100", "worker", "GPU-ray")
	pod.Annotations[AssignedNodeAnnotations] = nodeName

	_, _, _, err := GetMatchingDeviceRequest(NvidiaGPUDevice, nodeName, []string{"GPU-ray"}, []v1.Pod{*pod})
	if err == nil {
		t.Fatal("expected no match when spec.nodeName and vgpu-node annotation disagree")
	}
	if !strings.Contains(err.Error(), "device request not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestGetMatchingDeviceRequestSkipsMalformedCandidate(t *testing.T) {
	const nodeName = "gpu-node-1"
	malformedPod := podWithAllocatedDevice("bad-pod", "default", "bad-uid", nodeName, "100", "worker", "GPU-ray")
	malformedPod.Spec.Containers = nil
	rayPod := podWithAllocatedDevice("ray-pod", "default", "ray-uid", nodeName, "200", "ray-head", "GPU-ray")

	pod, container, devices, err := GetMatchingDeviceRequest(NvidiaGPUDevice, nodeName, []string{"GPU-ray"}, []v1.Pod{*malformedPod, *rayPod})
	if err != nil {
		t.Fatalf("GetMatchingDeviceRequest returned error: %v", err)
	}
	if pod == nil || pod.Name != rayPod.Name || pod.UID != rayPod.UID {
		t.Fatalf("expected ray pod %s/%s, got %#v", rayPod.Namespace, rayPod.Name, pod)
	}
	if container.Name != "ray-head" {
		t.Fatalf("expected ray-head container, got %q", container.Name)
	}
	if len(devices) != 1 || devices[0].UUID != "GPU-ray" {
		t.Fatalf("expected GPU-ray device, got %#v", devices)
	}
}

func podWithAllocatedDevice(name, namespace, uid, nodeName, vgpuTime, containerName, deviceID string) *v1.Pod {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(uid),
			Annotations: map[string]string{
				AssignedNodeAnnotations:         nodeName,
				AssignedPodNameAnnotations:      name,
				AssignedPodNamespaceAnnotations: namespace,
				AssignedPodUIDAnnotations:       uid,
				AssignedTimeAnnotations:         vgpuTime,
				AssignedIDsToAllocateAnnotations: EncodePodDevices(PodDevices{
					ContainerDevices{{
						UUID:      deviceID,
						Type:      NvidiaGPUDevice,
						Usedmem:   40000,
						Usedcores: 0,
					}},
				}),
			},
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
			Containers: []v1.Container{{
				Name: containerName,
			}},
		},
	}
	return pod
}
