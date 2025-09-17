# Configurable variables
DOCKER_REGISTRY ?= sami7786
IMAGE_NAME ?= nats-load-tester
VERSION ?= latest
K8S_NAMESPACE ?= default
NATS_SERVICE_NAME ?= ace-nats
NATS_SERVICE_NAMESPACE ?= ace
NATS_PORT ?= 4222

# Full image name
FULL_IMAGE_NAME = $(DOCKER_REGISTRY)/$(IMAGE_NAME):$(VERSION)

# Dynamic NATS URL generation
NATS_URL = nats://$(NATS_SERVICE_NAME).$(NATS_SERVICE_NAMESPACE).svc.cluster.local:$(NATS_PORT)

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
deploy: push clean
	@echo "Deploying to Kubernetes..."
	@echo "Using NATS URL: $(NATS_URL)"
	# Apply configmap with dynamic NATS URL
	sed -e "s|\$${NATS_URL}|$(NATS_URL)|g" \
		k8s/configmap.yaml | kubectl apply -n $(K8S_NAMESPACE) -f -
	# Apply Kubernetes manifests with substituted values
	sed -e "s|\$${DOCKER_REGISTRY}|$(DOCKER_REGISTRY)|g" \
		-e "s|\$${VERSION}|$(VERSION)|g" \
		k8s/deployment.yaml | kubectl apply -n $(K8S_NAMESPACE) -f -
	kubectl apply -f k8s/service.yaml -n $(K8S_NAMESPACE)

.PHONY: logs
logs:
	kubectl logs -f deployment/nats-load-tester -n $(K8S_NAMESPACE)

.PHONY: port-forward
port-forward:
	kubectl port-forward service/nats-load-tester 9481:9481 -n $(K8S_NAMESPACE)

.PHONY: test-local
test-local:
	go test ./...

.PHONY: run-local
run-local:
	go run cmd/load-tester/main.go

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: config
config:
	@echo "Current configuration:"
	@echo "  NATS_URL: $(NATS_URL)"
	@echo ""
	@echo "Generated configmap preview:"
	@sed -e "s|\$${NATS_URL}|$(NATS_URL)|g" k8s/configmap.yaml | head -20

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build        - Build Docker image"
	@echo "  push         - Build and push Docker image to registry"
	@echo "  clean        - Remove deployment and service from Kubernetes"
	@echo "  deploy       - Build, push, and deploy to Kubernetes"
	@echo "  config       - Show current configuration and preview generated configmap"
	@echo "  logs         - Follow logs from the deployment"
	@echo "  port-forward - Forward local port 8080 to the service"
	@echo "  test-local   - Run tests locally"
	@echo "  run-local    - Run the application locally"
	@echo "  fmt          - Format Go code"
	@echo "  vet          - Run Go vet"
	@echo ""
	@echo "Configuration:"
	@echo "  DOCKER_REGISTRY        = $(DOCKER_REGISTRY)"
	@echo "  IMAGE_NAME             = $(IMAGE_NAME)"
	@echo "  VERSION                = $(VERSION)"
	@echo "  K8S_NAMESPACE          = $(K8S_NAMESPACE)"
	@echo "  NATS_SERVICE_NAME      = $(NATS_SERVICE_NAME)"
	@echo "  NATS_SERVICE_NAMESPACE = $(NATS_SERVICE_NAMESPACE)"
	@echo "  NATS_PORT              = $(NATS_PORT)"
	@echo "  NATS_URL               = $(NATS_URL)"
