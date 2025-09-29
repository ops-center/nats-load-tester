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
DOCKER_REGISTRY ?= sami7786
IMAGE_NAME ?= nats-load-tester
VERSION ?= latest
K8S_NAMESPACE ?= ace
NATS_SERVICE_NAME ?= ace-nats
NATS_SERVICE_NAMESPACE ?= ace
NATS_PORT ?= 4222
NATS_CREDS_SECRET_NAME ?= ace-nats-cred
NATS_CREDS_MOUNT_PATH ?= /etc/nats/creds

GO_VERSION       ?= 1.25
BUILD_IMAGE      ?= ghcr.io/appscode/golang-dev:$(GO_VERSION)
REPO     := $(notdir $(shell pwd))
DOCKER_REPO_ROOT := /go/src/$(GO_PKG)/$(REPO)

# Full image name
FULL_IMAGE_NAME = $(DOCKER_REGISTRY)/$(IMAGE_NAME):$(VERSION)

# Dynamic NATS URL generation
NATS_URL = nats://$(NATS_SERVICE_NAME).$(K8S_NAMESPACE).svc.cluster.local:$(NATS_PORT)


.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build        - Build Docker image"
	@echo "  push         - Build and push Docker image to registry"
	@echo "  clean        - Remove deployment and service from Kubernetes"
	@echo "  deploy       - Build, push, and deploy to Kubernetes"
	@echo "  config       - Show current configuration and preview generated configmap"
	@echo ""
	@echo "Configuration:"
	@echo "  DOCKER_REGISTRY        = $(DOCKER_REGISTRY)"
	@echo "  IMAGE_NAME             = $(IMAGE_NAME)"
	@echo "  VERSION                = $(VERSION)"
	@echo "  K8S_NAMESPACE          = $(K8S_NAMESPACE)"
	@echo "  NATS_SERVICE_NAME      = $(NATS_SERVICE_NAME)"
	@echo "  NATS_PORT              = $(NATS_PORT)"
	@echo "  NATS_URL               = $(NATS_URL)"
	@echo "  NATS_CREDS_SECRET_NAME = $(NATS_CREDS_SECRET_NAME)"
	@echo "  NATS_CREDS_MOUNT_PATH  = $(NATS_CREDS_MOUNT_PATH)"

# Build targets
.PHONY: build
build:
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

.PHONY: test
test:
	go test ./...

.PHONY: run-local
run-local:
	go run cmd/load-tester/main.go

.PHONY: add-license
add-license:
	@echo "Adding license header"
	@docker run --rm 	                                 \
		-u $$(id -u):$$(id -g)                           \
		-v /tmp:/.cache                                  \
		-v $$(pwd):$(DOCKER_REPO_ROOT)                   \
		-w $(DOCKER_REPO_ROOT)                           \
		--env HTTP_PROXY=$(HTTP_PROXY)                   \
		--env HTTPS_PROXY=$(HTTPS_PROXY)                 \
		$(BUILD_IMAGE)                                   \
		ltag -t "./hack/license" --excludes "vendor contrib bin" -v

.PHONY: config
config:
	@echo "Current configuration:"
	@echo "  NATS_URL: $(NATS_URL)"
	@echo ""
	@echo "Generated configmap preview:"
	@sed -e "s|\$${NATS_URL}|$(NATS_URL)|g" \
		-e "s|\$${NATS_CREDS_MOUNT_PATH}|$(NATS_CREDS_MOUNT_PATH)|g" \
		k8s/configmap.yaml | head -20
