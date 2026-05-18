# Copyright AppsCode Inc. and Contributors.
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

# Configurable variables
DOCKER_REGISTRY ?= $(REGISTRY)
IMAGE_NAME ?= nats-load-tester
VERSION ?= latest
K8S_NAMESPACE ?= ace

NATS_SERVICE_NAME ?= ace-nats
NATS_SERVICE_NAMESPACE ?= ace
NATS_PORT ?= 4222
NATS_CREDS_SECRET_NAME ?= ace-nats-cred
NATS_CREDS_MOUNT_PATH ?= /etc/nats/creds

GO_VERSION           ?= 1.25.1
BUILD_IMAGE          ?= ghcr.io/appscode/golang-dev:$(GO_VERSION)
GOLANGCI_LINT_IMAGE  ?= golangci/golangci-lint:latest
ADDTL_LINTERS        := sqlclosecheck,unparam,govet
REPO                 := $(notdir $(shell pwd))
DOCKER_REPO_ROOT     := /go/src/$(GO_PKG)/$(REPO)

GO_PKG               := go.opscenter.dev
SRC_DIRS             := cmd internal

OS   := $(if $(GOOS),$(GOOS),$(shell go env GOOS))
ARCH := $(if $(GOARCH),$(GOARCH),$(shell go env GOARCH))

BUILD_DIRS  := bin/$(OS)_$(ARCH) .go/bin/$(OS)_$(ARCH) .go/cache

# Full image name
FULL_IMAGE_NAME = $(DOCKER_REGISTRY)/$(IMAGE_NAME):$(VERSION)

# Dynamic NATS URL generation
NATS_URL = nats://$(NATS_SERVICE_NAME).$(K8S_NAMESPACE).svc.cluster.local:$(NATS_PORT)


.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build          - Build Docker image"
	@echo "  push           - Build and push Docker image"
	@echo "  deploy         - Deploy to Kubernetes"
	@echo "  clean          - Remove Kubernetes resources"
	@echo "  test           - Run tests"
	@echo "  lint           - Run linter"
	@echo "  verify-fmt     - Verify code formatting"
	@echo "  check-license  - Verify license headers"
	@echo "  add-license    - Add license headers"
	@echo "  verify         - Verify modules are up to date"
	@echo "  ci             - Run all CI checks"
	@echo "  run-local      - Run locally"
.PHONY: build
build:
ifeq ($(DOCKER_REGISTRY),)
	$(error DOCKER_REGISTRY must be set via DOCKER_REGISTRY variable or REGISTRY environment variable)
endif
	@echo "Building Docker image..."
	docker build -t $(FULL_IMAGE_NAME) .

.PHONY: push
push: build
	@echo "Pushing Docker image to registry..."
	docker push $(FULL_IMAGE_NAME)

.PHONY: clean
clean:
	@echo "Removing deployment and service from Kubernetes..."
	kubectl delete deployment nats-load-tester -n $(K8S_NAMESPACE) --ignore-not-found=true
	kubectl delete service nats-load-tester -n $(K8S_NAMESPACE) --ignore-not-found=true
	kubectl delete configmap nats-load-tester-config -n $(K8S_NAMESPACE) --ignore-not-found=true

.PHONY: deploy
deploy: clean push
	@echo "Deploying to Kubernetes..."
	@echo "Using NATS URL: $(NATS_URL)"
	# Apply configmap with dynamic NATS URL and creds path
	sed -e "s|\$${NATS_URL}|$(NATS_URL)|g" \
		-e "s|\$${NATS_CREDS_MOUNT_PATH}|$(NATS_CREDS_MOUNT_PATH)|g" \
		k8s/configmap.yaml | kubectl apply -n $(K8S_NAMESPACE) -f -
	# Apply Kubernetes manifests with substituted values
	sed -e "s|\$${DOCKER_REGISTRY}|$(DOCKER_REGISTRY)|g" \
		-e "s|\$${VERSION}|$(VERSION)|g" \
		-e "s|\$${NATS_CREDS_SECRET_NAME}|$(NATS_CREDS_SECRET_NAME)|g" \
		-e "s|\$${NATS_CREDS_MOUNT_PATH}|$(NATS_CREDS_MOUNT_PATH)|g" \
		k8s/deployment.yaml | kubectl apply -n $(K8S_NAMESPACE) -f -
	kubectl apply -f k8s/service.yaml -n $(K8S_NAMESPACE)

.PHONY: verify-fmt
verify-fmt: $(BUILD_DIRS)
	@docker run --rm -u $$(id -u):$$(id -g) -v $$(pwd):/src -w /src \
		-v $$(pwd)/.go/bin/$(OS)_$(ARCH):/go/bin \
		-v $$(pwd)/.go/bin/$(OS)_$(ARCH):/go/bin/$(OS)_$(ARCH) \
		-v $$(pwd)/.go/cache:/.cache \
		$(BUILD_IMAGE) \
		/bin/bash -c "goimports -w $(SRC_DIRS) 2>/dev/null || true; gofmt -w $(SRC_DIRS) && git diff --exit-code"

.PHONY: lint
lint: $(BUILD_DIRS)
	@docker run --rm -u $$(id -u):$$(id -g) -v $$(pwd):/src -w /src \
		-v $$(pwd)/.go/bin/$(OS)_$(ARCH):/go/bin \
		-v $$(pwd)/.go/bin/$(OS)_$(ARCH):/go/bin/$(OS)_$(ARCH) \
		-v $$(pwd)/.go/cache:/.cache \
		$(GOLANGCI_LINT_IMAGE) \
		golangci-lint run --enable $(ADDTL_LINTERS) --max-same-issues=100 --timeout=10m

.PHONY: test
test:
	@docker run --rm -u $$(id -u):$$(id -g) -v $$(pwd):/src -w /src -v /tmp:/.cache $(BUILD_IMAGE) \
		go test -v -race ./...

.PHONY: check-license
check-license:
	@docker run --rm -u $$(id -u):$$(id -g) -v /tmp:/.cache -v $$(pwd):$(DOCKER_REPO_ROOT) -w $(DOCKER_REPO_ROOT) $(BUILD_IMAGE) \
		ltag -t "./hack/license" --excludes "vendor contrib bin .github" --check -v

.PHONY: add-license
add-license:
	@docker run --rm -u $$(id -u):$$(id -g) -v /tmp:/.cache -v $$(pwd):$(DOCKER_REPO_ROOT) -w $(DOCKER_REPO_ROOT) $(BUILD_IMAGE) \
		ltag -t "./hack/license" --excludes "vendor contrib bin .github" -v

.PHONY: ci
ci: check-license verify-fmt lint test

$(BUILD_DIRS):
	@mkdir -p $@
