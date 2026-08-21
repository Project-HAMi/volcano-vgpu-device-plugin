package nvidia

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// TestParseContainerDirName tests the directory name parsing
func TestParseContainerDirName(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		expectedPodUID  string
		expectedCtrName string
	}{
		{
			name:            "Valid format",
			input:           "3d3a88bb-1234-5678_cnt_e8cc7639da00",
			expectedPodUID:  "3d3a88bb-1234-5678",
			expectedCtrName: "cnt",
		},
		{
			name:            "Container with dash in name",
			input:           "uuid-1234_my-container_id",
			expectedPodUID:  "uuid-1234",
			expectedCtrName: "my-container",
		},
		{
			name:            "Missing parts",
			input:           "3d3a88bb-1234-5678",
			expectedPodUID:  "",
			expectedCtrName: "",
		},
		{
			name:            "Empty string",
			input:           "",
			expectedPodUID:  "",
			expectedCtrName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			podUID, ctrName := parseContainerDirName(tt.input)
			if podUID != tt.expectedPodUID {
				t.Errorf("podUID: got %q, want %q", podUID, tt.expectedPodUID)
			}
			if ctrName != tt.expectedCtrName {
				t.Errorf("ctrName: got %q, want %q", ctrName, tt.expectedCtrName)
			}
		})
	}
}

// TestIsValidPod_RunningPod tests that running pods are valid
func TestIsValidPod_RunningPod(t *testing.T) {
	podUID := types.UID("3d3a88bb-1234-5678-abcd-ef0123456789")
	containerName := "cnt"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-0",
			Namespace: "default",
			UID:       podUID,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: containerName,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				},
			},
		},
	}

	pods := &corev1.PodList{Items: []corev1.Pod{*pod}}
	dirName := string(podUID) + "_" + containerName + "_e8cc7639da00"

	result := isValidPod(dirName, pods)
	if !result {
		t.Errorf("Running pod should be valid, got %v", result)
	}
}

// TestIsValidPod_SucceededPod tests that succeeded pods are invalid
func TestIsValidPod_SucceededPod(t *testing.T) {
	podUID := types.UID("2f4b77cc-5678-9012-abcd-ef0123456789")
	containerName := "cnt"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-0",
			Namespace: "default",
			UID:       podUID,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodSucceeded, // ← Pod completed successfully
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: containerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 0,
							Reason:   "Completed",
						},
					},
				},
			},
		},
	}

	pods := &corev1.PodList{Items: []corev1.Pod{*pod}}
	dirName := string(podUID) + "_" + containerName + "_e8cc7639da00"

	result := isValidPod(dirName, pods)
	if result {
		t.Errorf("Succeeded pod should be invalid, got %v", result)
	}
}

// TestIsValidPod_FailedPod tests that failed pods are invalid
func TestIsValidPod_FailedPod(t *testing.T) {
	podUID := types.UID("7e2c33aa-9012-3456-abcd-ef0123456789")
	containerName := "cnt"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-0",
			Namespace: "default",
			UID:       podUID,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed, // ← Pod failed
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: containerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
							Reason:   "Error",
						},
					},
				},
			},
		},
	}

	pods := &corev1.PodList{Items: []corev1.Pod{*pod}}
	dirName := string(podUID) + "_" + containerName + "_e8cc7639da00"

	result := isValidPod(dirName, pods)
	if result {
		t.Errorf("Failed pod should be invalid, got %v", result)
	}
}

// TestIsValidPod_TerminatedContainer tests that pods with terminated containers are invalid
func TestIsValidPod_TerminatedContainer(t *testing.T) {
	podUID := types.UID("a1b2c3d4-e5f6-7890-abcd-ef0123456789")
	containerName := "cnt"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-0",
			Namespace: "default",
			UID:       podUID,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning, // Pod is Running
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: containerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ // But container is terminated!
							ExitCode: 0,
							Reason:   "Completed",
						},
					},
				},
			},
		},
	}

	pods := &corev1.PodList{Items: []corev1.Pod{*pod}}
	dirName := string(podUID) + "_" + containerName + "_e8cc7639da00"

	result := isValidPod(dirName, pods)
	if result {
		t.Errorf("Pod with terminated container should be invalid, got %v", result)
	}
}

// TestIsValidPod_PodNotFound tests that non-existent pods are invalid
func TestIsValidPod_PodNotFound(t *testing.T) {
	nonExistentUID := types.UID("ffffffff-ffff-ffff-ffff-ffffffffffff")
	containerName := "cnt"

	// Pod list doesn't contain the pod we're looking for
	pods := &corev1.PodList{Items: []corev1.Pod{}}

	dirName := string(nonExistentUID) + "_" + containerName + "_e8cc7639da00"
	result := isValidPod(dirName, pods)

	if result {
		t.Errorf("Non-existent pod should be invalid, got %v", result)
	}
}

// TestIsValidPod_ContainerNotFound tests that pods without matching container are invalid
func TestIsValidPod_ContainerNotFound(t *testing.T) {
	podUID := types.UID("3d3a88bb-1234-5678-abcd-ef0123456789")
	targetContainerName := "cnt"
	actualContainerName := "different-container"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-0",
			Namespace: "default",
			UID:       podUID,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: actualContainerName, // Different container name
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				},
			},
		},
	}

	pods := &corev1.PodList{Items: []corev1.Pod{*pod}}
	dirName := string(podUID) + "_" + targetContainerName + "_e8cc7639da00"

	result := isValidPod(dirName, pods)
	if result {
		t.Errorf("Pod without matching container should be invalid, got %v", result)
	}
}

// TestIsValidPod_PendingPod tests that pending pods are invalid
func TestIsValidPod_PendingPod(t *testing.T) {
	podUID := types.UID("4e4c44bb-2345-6789-bcde-f01234567890")
	containerName := "cnt"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-0",
			Namespace: "default",
			UID:       podUID,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending, // Pod is still pending
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: containerName,
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{},
					},
				},
			},
		},
	}

	pods := &corev1.PodList{Items: []corev1.Pod{*pod}}
	dirName := string(podUID) + "_" + containerName + "_e8cc7639da00"

	result := isValidPod(dirName, pods)
	if result {
		t.Errorf("Pending pod should be invalid, got %v", result)
	}
}

// TestIsValidPod_WaitingContainer tests that pods with waiting containers are invalid
func TestIsValidPod_WaitingContainer(t *testing.T) {
	podUID := types.UID("5f5d55cc-3456-7890-cdef-012345678901")
	containerName := "cnt"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-0",
			Namespace: "default",
			UID:       podUID,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: containerName,
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason: "CrashLoopBackOff",
						},
					},
				},
			},
		},
	}

	pods := &corev1.PodList{Items: []corev1.Pod{*pod}}
	dirName := string(podUID) + "_" + containerName + "_e8cc7639da00"

	result := isValidPod(dirName, pods)
	if result {
		t.Errorf("Pod with waiting container should be invalid, got %v", result)
	}
}

// TestIsValidPod_InvalidCacheDirName tests that invalid directory names are handled gracefully
func TestIsValidPod_InvalidCacheDirName(t *testing.T) {
	pods := &corev1.PodList{Items: []corev1.Pod{}}

	tests := []string{
		"invalid",
		"no_underscore",
		"",
		"_",
	}

	for _, dirName := range tests {
		result := isValidPod(dirName, pods)
		if result {
			t.Errorf("Invalid dir name %q should return false, got %v", dirName, result)
		}
	}
}

// TestIsValidPod_MultipleContainers tests behavior with multi-container pods
func TestIsValidPod_MultipleContainers(t *testing.T) {
	podUID := types.UID("6a6e66dd-4567-8901-def0-123456789012")
	targetContainerName := "app"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multi-container-pod",
			Namespace: "default",
			UID:       podUID,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "sidecar",
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				},
				{
					Name: targetContainerName,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				},
				{
					Name: "init",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 0,
						},
					},
				},
			},
		},
	}

	pods := &corev1.PodList{Items: []corev1.Pod{*pod}}
	dirName := string(podUID) + "_" + targetContainerName + "_containerid"

	result := isValidPod(dirName, pods)
	if !result {
		t.Errorf("Should find running container in multi-container pod, got %v", result)
	}
}

// TestIsValidPod_MultipleContainers_AllTerminated tests multi-container pod with all containers terminated
func TestIsValidPod_MultipleContainers_AllTerminated(t *testing.T) {
	podUID := types.UID("7b7f77ee-5678-9012-ef01-234567890123")
	targetContainerName := "app"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multi-container-pod",
			Namespace: "default",
			UID:       podUID,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodSucceeded,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "sidecar",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ExitCode: 0},
					},
				},
				{
					Name: targetContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ExitCode: 0},
					},
				},
			},
		},
	}

	pods := &corev1.PodList{Items: []corev1.Pod{*pod}}
	dirName := string(podUID) + "_" + targetContainerName + "_containerid"

	result := isValidPod(dirName, pods)
	if result {
		t.Errorf("All-terminated multi-container pod should be invalid, got %v", result)
	}
}
