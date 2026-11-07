# Copyright (c) 2023, NVIDIA CORPORATION.  All rights reserved.
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

.DEFAULT_GOAL := all

REGISTRY ?= projecthami
VERSION  ?= dev

# BUILD_PLATFORMS=linux/arm64 make all builds an arm64 image locally.
# BUILD_PLATFORMS=linux/amd64,linux/arm64 make push-latest publishes a multi-arch image.
BUILD_PLATFORMS ?= linux/amd64

IMAGE      := $(REGISTRY)/volcano-vgpu-device-plugin
DOCKERFILE ?= docker/Dockerfile

.PHONY: all build ubuntu20.04 push push-short push-latest

all: build

build:
	docker buildx build --platform $(BUILD_PLATFORMS) --load \
		--tag $(IMAGE):$(VERSION) \
		--file $(DOCKERFILE) .

# Backward-compatible target for legacy ubuntu20.04 tag
ubuntu20.04:
	docker buildx build --platform $(BUILD_PLATFORMS) --load \
		--tag $(IMAGE):$(VERSION)-ubuntu20.04 \
		--file $(DOCKERFILE) .

push:
	docker buildx build --platform $(BUILD_PLATFORMS) --push \
		--tag $(IMAGE):$(VERSION)-ubuntu20.04 \
		--file $(DOCKERFILE) .

push-short:
	docker buildx build --platform $(BUILD_PLATFORMS) --push \
		--tag $(IMAGE):$(VERSION) \
		--file $(DOCKERFILE) .

push-latest:
	docker buildx build --platform $(BUILD_PLATFORMS) --push \
		--tag $(IMAGE):latest \
		--file $(DOCKERFILE) .
