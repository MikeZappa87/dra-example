# DRA Demo Makefile

DRIVER_NAME := dra-file-driver
IMAGE_TAG := latest
KIND_CLUSTER := dra-demo
NAMESPACE := dra-system

# Ensure Go bin is in PATH for kind
export PATH := $(PATH):$(shell go env GOPATH)/bin

.PHONY: all build load deploy undeploy test check clean cluster cluster-delete logs help

all: cluster build load deploy test

help:
	@echo "DRA Demo Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make cluster        - Create kind cluster with DRA enabled"
	@echo "  make cluster-delete - Delete the kind cluster"
	@echo "  make build          - Build the DRA driver Docker image"
	@echo "  make load           - Load the image into kind cluster"
	@echo "  make deploy         - Deploy the DRA driver and resources"
	@echo "  make undeploy       - Remove all deployed resources"
	@echo "  make test           - Deploy test pod"
	@echo "  make check          - Check the resource file content"
	@echo "  make logs           - View DRA driver logs"
	@echo "  make clean          - Full cleanup (undeploy + delete cluster)"
	@echo "  make all            - Run everything (cluster, build, load, deploy, test)"

# Create kind cluster with DRA feature gate enabled
cluster:
	@echo "Creating kind cluster with DRA enabled..."
	@command -v kind >/dev/null 2>&1 || (echo "Installing kind..." && go install sigs.k8s.io/kind@latest)
	@mkdir -p /tmp/dra
	kind create cluster --config kind-config.yaml
	@echo "Waiting for cluster to be ready..."
	kubectl wait --for=condition=Ready nodes --all --timeout=120s
	@echo "Kind cluster '$(KIND_CLUSTER)' created successfully"

# Delete the kind cluster
cluster-delete:
	@echo "Deleting kind cluster..."
	kind delete cluster --name $(KIND_CLUSTER)

# Build the DRA driver Docker image
build:
	@echo "Building DRA driver image..."
	docker build -t $(DRIVER_NAME):$(IMAGE_TAG) .
	@echo "Image $(DRIVER_NAME):$(IMAGE_TAG) built successfully"

# Load the image into kind cluster
load:
	@echo "Loading image into kind cluster..."
	kind load docker-image $(DRIVER_NAME):$(IMAGE_TAG) --name $(KIND_CLUSTER)
	@echo "Image loaded successfully"

# Deploy the DRA driver and related resources
deploy:
	@echo "Deploying DRA driver..."
	kubectl apply -f deploy/namespace.yaml
	kubectl apply -f deploy/driver.yaml
	kubectl apply -f deploy/resourceclass.yaml
	@echo "Waiting for DRA driver to be ready..."
	kubectl wait --for=condition=Ready pod -l app=$(DRIVER_NAME) -n $(NAMESPACE) --timeout=60s || true
	@echo "DRA driver deployed successfully"
	@echo ""
	@echo "To view driver logs: make logs"
	@echo "To deploy test deployment: make test"

# Remove all deployed resources
undeploy:
	@echo "Removing deployed resources..."
	-kubectl delete -f deploy/deployment.yaml --ignore-not-found
	-kubectl delete -f deploy/resourceclass.yaml --ignore-not-found
	-kubectl delete -f deploy/driver.yaml --ignore-not-found
	-kubectl delete -f deploy/namespace.yaml --ignore-not-found
	@echo "Resources removed"

# Deploy test deployment
test:
	@echo "Deploying test deployment..."
	kubectl apply -f deploy/deployment.yaml
	@echo "Waiting for deployment to be ready..."
	kubectl wait --for=condition=Available deployment/dra-test-deployment --timeout=60s || true
	kubectl get pods -l app=dra-test -o wide
	@echo ""
	@echo "Test deployment ready. Use 'make check' to view the resource file"
	@echo "Use 'kubectl logs -l app=dra-test -f' to follow pod logs"

# Check the resource file content
check:
	@echo "Checking resource file content..."
	@echo ""
	@echo "=== DRA Driver Status ==="
	kubectl get pods -n $(NAMESPACE) -l app=$(DRIVER_NAME)
	@echo ""
	@echo "=== ResourceClaims ==="
	kubectl get resourceclaims -o wide || true
	@echo ""
	@echo "=== Test Deployment Status ==="
	kubectl get pods -l app=dra-test -o wide || true
	@echo ""
	@echo "=== Resource File Content (via test pod) ==="
	kubectl exec deploy/dra-test-deployment -- cat /etc/dra/file1 2>/dev/null || echo "File not available or pod not running"
	@echo ""
	@echo "=== Driver Logs (last 20 lines) ==="
	kubectl logs -n $(NAMESPACE) -l app=$(DRIVER_NAME) --tail=20 || true

# View DRA driver logs
logs:
	kubectl logs -n $(NAMESPACE) -l app=$(DRIVER_NAME) -f

# Full cleanup
clean: undeploy cluster-delete
	@echo "Cleanup complete"

# Restart the driver
restart:
	kubectl rollout restart daemonset/$(DRIVER_NAME) -n $(NAMESPACE)
	kubectl wait --for=condition=Ready pod -l app=$(DRIVER_NAME) -n $(NAMESPACE) --timeout=60s

# Debug: get all DRA-related resources
debug:
	@echo "=== Nodes ==="
	kubectl get nodes
	@echo ""
	@echo "=== DRA Driver Pods ==="
	kubectl get pods -n $(NAMESPACE) -o wide
	@echo ""
	@echo "=== DeviceClasses ==="
	kubectl get deviceclasses
	@echo ""
	@echo "=== ResourceSlices ==="
	kubectl get resourceslices
	@echo ""
	@echo "=== ResourceClaims ==="
	kubectl get resourceclaims --all-namespaces
	@echo ""
	@echo "=== Test Deployment ==="
	kubectl get pods -l app=dra-test -o wide 2>/dev/null || echo "Test deployment not found"
	@echo ""
	@echo "=== Events ==="
	kubectl get events --sort-by='.lastTimestamp' | tail -20
