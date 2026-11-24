# Copyright (c) 2019, NVIDIA CORPORATION.  All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c AS builder
ARG TARGETARCH
ARG GO_VERSION=1.27.4
ARG GO_SHA256_AMD64=7226736c2339719cda28fbf3a54d71aa63bcefe376e98cef00034268dab24840
ARG GO_SHA256_ARM64=3392582988a273a87ab5043ee1b2c9c17a2c3c00580bf9326c47a2e0aaaeac92
RUN apt-get update
RUN apt-get -y install ca-certificates g++ wget
RUN case "${TARGETARCH}" in \
        "amd64") GO_SHA256="${GO_SHA256_AMD64}" ;; \
        "arm64") GO_SHA256="${GO_SHA256_ARM64}" ;; \
        *) echo "Unsupported architecture: ${TARGETARCH}" && exit 1 ;; \
    esac && \
    wget -qO go.tar.gz "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" && \
    echo "${GO_SHA256}  go.tar.gz" | sha256sum -c - && \
    tar -C /usr/local -xzf go.tar.gz && \
    rm go.tar.gz
ENV GOPATH=/go
ENV PATH=$GOPATH/bin:/usr/local/go/bin:$PATH
WORKDIR /go/src/volcano.sh/devices

COPY . .
RUN go env -w GOARCH=${TARGETARCH}
RUN go env -w CGO_LDFLAGS_ALLOW='-Wl,--unresolved-symbols=ignore-in-object-files'
RUN go build -ldflags="-s -w" -o volcano-vgpu-device-plugin ./cmd/vgpu
RUN go build -ldflags="-s -w" -o volcano-vgpu-monitor ./cmd/vgpu-monitor
RUN go build -ldflags="-s -w" -o /go/bin/nvidia-mig-parted github.com/NVIDIA/mig-parted/cmd/nvidia-mig-parted

FROM nvidia/cuda:13.5.0-cudnn-devel-ubi8@sha256:25ec0bcb3b5bfaf567a105e07268ef685ef81e0a02c79142f4fea3e32d11a7f3 AS nvidia_builder
RUN dnf --disablerepo=cuda install -y cmake git && dnf clean all
COPY ./libvgpu /libvgpu
WORKDIR /libvgpu
RUN rm -rf /libvgpu/build
RUN bash ./build.sh

FROM ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c

ENV NVIDIA_VISIBLE_DEVICES=all
ENV NVIDIA_DRIVER_CAPABILITIES=utility

COPY --from=builder /go/src/volcano.sh/devices/volcano-vgpu-device-plugin /usr/bin/volcano-vgpu-device-plugin
COPY --from=builder /go/src/volcano.sh/devices/volcano-vgpu-monitor /usr/bin/volcano-vgpu-monitor
COPY --from=builder /go/bin/nvidia-mig-parted /usr/bin/nvidia-mig-parted
COPY --from=builder /go/src/volcano.sh/devices/lib/nvidia/ld.so.preload /k8s-vgpu/lib/nvidia/
COPY --from=nvidia_builder /libvgpu/build/libvgpu.so /k8s-vgpu/lib/nvidia/

ENTRYPOINT ["volcano-vgpu-device-plugin"]
