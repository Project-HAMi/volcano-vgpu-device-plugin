# Vulkan vGPU 지원

이 device-plugin 은 CUDA workload 와 동일하게 **Vulkan workload** 도 메모리 partitioning 을 enforce 한다. Volcano scheduler 와 함께 사용한다.

## 작동 원리

1. **libvgpu (HAMi-core) vulkan-layer**: `vkAllocateMemory` 를 후킹하여 `CUDA_DEVICE_MEMORY_LIMIT_0` 를 enforce.
2. **device-plugin Allocate**: host 의 `/usr/local/vgpu/vulkan/implicit_layer.d/hami.json` 이 존재하면 container 의 `/etc/vulkan/implicit_layer.d/hami.json` 으로 bind-mount.
3. **HAMi mutating webhook (별도 install)**: pod annotation `hami.io/vulkan: "true"` 검사 → `HAMI_VULKAN_ENABLE=1` env + `NVIDIA_DRIVER_CAPABILITIES` 에 `graphics` 추가.
4. **enable_environment 가드**: manifest 의 `enable_environment: HAMI_VULKAN_ENABLE=1` 매치 시에만 layer 로드. annotation 없는 pod 은 영향 없음.

## 설치 (한 번만)

### 1. device-plugin 갱신

```bash
kubectl apply -f volcano-vgpu-device-plugin.yml
# 또는 CDI 모드:
# kubectl apply -f volcano-vgpu-device-plugin-cdi.yml
```

device-plugin 의 postStart hook 이 image 안의 hami.json 을 host `/usr/local/vgpu/vulkan/implicit_layer.d/` 로 자동 복사한다.

### 2. HAMi mutating webhook 별도 install

```bash
helm repo add hami https://project-hami.github.io/HAMi
helm install hami-webhook hami/hami \
    --namespace kube-system \
    --set devicePlugin.enabled=false \
    --set scheduler.kubeScheduler.enabled=false \
    --set scheduler.extender.enabled=false \
    --set admissionWebhook.enabled=true
```

webhook 만 활성화 — Volcano scheduler 와 device-plugin 은 그대로 유지.

### 3. (선택) Fallback manifest DaemonSet

device-plugin 이 init 으로 manifest 를 host 에 자동 배치하지 못하는 환경에서:

```bash
kubectl apply -f volcano-vgpu-vulkan-manifest.yml
```

## 사용

pod 에 annotation `hami.io/vulkan: "true"` + `nvidia.com/gpumem` resource limit 추가:

```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    hami.io/vulkan: "true"
spec:
  schedulerName: volcano
  containers:
  - name: vulkan-app
    image: <Vulkan 사용 image>
    resources:
      limits:
        nvidia.com/gpu: 1
        nvidia.com/gpumem: 4000
```

전체 예시: `examples/vulkan-pod.yaml`

## 검증

container 안에서:

```bash
# 1. env 주입 확인
env | grep -E '(HAMI_VULKAN|DRIVER_CAPABILITIES)'
# 기대: HAMI_VULKAN_ENABLE=1, NVIDIA_DRIVER_CAPABILITIES=...,graphics

# 2. manifest 파일 mount 확인
ls /etc/vulkan/implicit_layer.d/hami.json

# 3. CUDA_DEVICE_MEMORY_LIMIT 확인
env | grep CUDA_DEVICE_MEMORY_LIMIT
# 기대: CUDA_DEVICE_MEMORY_LIMIT_0=4000m

# 4. Vulkan tool 로 memory limit 확인 (Vulkan app 실행 시)
# 예: Isaac Sim Kit boot log 의 'GPU Memory: <limit> MB'
```

## 비활성화

annotation `hami.io/vulkan: "true"` 가 없으면 webhook 은 no-op. 즉:
- env `HAMI_VULKAN_ENABLE` 미주입
- manifest 의 `enable_environment` 가드 unmatched
- Vulkan layer 안 로드
- 일반 CUDA pod 동작 그대로

## 트러블슈팅

| 증상 | 원인 | 해결 |
|---|---|---|
| Vulkan app 이 메모리 한계 무시 | webhook annotation 처리 안 됨 | `kubectl get pod ... -o yaml` 로 env 에 HAMI_VULKAN_ENABLE 있는지 확인 |
| `manifest 파일 not found` | host 에 hami.json 미배치 | DaemonSet pod log 또는 `ls /usr/local/vgpu/vulkan/implicit_layer.d/` 확인 |
| `vk_icdNegotiateLoaderICDInterfaceVersion -3` | NVIDIA Vulkan ICD 의존성 부족 | container image 에 libGLX_nvidia, libEGL, X11 라이브러리 포함 |
