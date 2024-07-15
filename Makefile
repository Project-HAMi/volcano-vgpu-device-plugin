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


.PHONY: all build builder test
.DEFAULT_GOAL := all

##### Global variables #####

DOCKER   ?= docker
REGISTRY ?= projecthami
VERSION  ?= latest
TAG_VERSION ?= 1.0.0

##### Public rules #####

all: ubuntu20.04 centos7

push:
	$(DOCKER) push "$(REGISTRY)/volcano-device-plugin:$(VERSION)-ubuntu20.04"
	$(DOCKER) push "$(REGISTRY)/volcano-device-plugin:$(VERSION)-centos7"

push-short:
	$(DOCKER) tag "$(REGISTRY)/volcano-device-plugin:$(VERSION)-ubuntu20.04" "$(REGISTRY)/volcano-device-plugin:$(VERSION)"
	$(DOCKER) push "$(REGISTRY)/volcano-device-plugin:$(VERSION)"

push-latest:
	$(DOCKER) tag "$(REGISTRY)/volcano-device-plugin:$(VERSION)-ubuntu20.04" "$(REGISTRY)/volcano-device-plugin:latest"
	$(DOCKER) push "$(REGISTRY)/volcano-device-plugin:latest"

push-tag:
	$(DOCKER) tag "$(REGISTRY)/volcano-device-plugin:$(VERSION)-ubuntu20.04" "$(REGISTRY)/volcano-device-plugin:$(TAG_VERSION)"
	$(DOCKER) push "$(REGISTRY)/volcano-device-plugin:$(TAG_VERSION)"

push-vgpu-tag:
	$(DOCKER) tag "$(REGISTRY)/volcano-vgpu-device-plugin:$(VERSION)-ubuntu20.04" "$(REGISTRY)/volcano-vgpu-device-plugin:$(TAG_VERSION)"
	$(DOCKER) push "$(REGISTRY)/volcano-vgpu-device-plugin:$(TAG_VERSION)" 

ubuntu20.04:
	$(DOCKER) build --pull \
		--tag $(REGISTRY)/volcano-device-plugin:$(VERSION)-ubuntu20.04 \
		--file docker/amd64/Dockerfile.ubuntu20.04 .

vgpu:
	$(DOCKER) build --pull \
		--tag $(REGISTRY)/volcano-vgpu-device-plugin:$(VERSION)-ubuntu20.04 \
		--file docker/amd64/Dockerfile.vgpu-ubuntu20.04 .

centos7:
	$(DOCKER) build --pull \
		--tag $(REGISTRY)/volcano-device-plugin:$(VERSION)-centos7 \
		--file docker/amd64/Dockerfile.centos7 .

include Makefile.def

BIN_DIR=_output/bin
RELEASE_DIR=_output/release
REL_OSARCH=linux/amd64

init:
	mkdir -p ${BIN_DIR}
	mkdir -p ${RELEASE_DIR}

gen_bin: init
	go get github.com/mitchellh/gox
	CGO_ENABLED=1 gox -osarch=${REL_OSARCH} -ldflags ${LD_FLAGS} -output ${BIN_DIR}/${REL_OSARCH}/volcano-device-plugin cmd/vgpu
