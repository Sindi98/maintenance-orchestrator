# Image URL to use for building/pushing image targets
IMG ?= maintenance-orchestrator:latest

# controller-gen used for code/manifest generation
CONTROLLER_GEN_VERSION ?= v0.15.0
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

# Output directories for generated manifests
CRD_DIR ?= deploy/crd
RBAC_DIR ?= deploy/rbac

.PHONY: all
all: build

.PHONY: help
help: ## Display this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: fmt
fmt: ## Run go fmt against code
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code
	go vet ./...

.PHONY: tidy
tidy: ## Tidy modules (generates go.sum and indirect requires)
	go mod tidy

.PHONY: test
test: fmt vet ## Run unit tests with coverage
	go test ./... -coverprofile cover.out -count=1

.PHONY: build
build: fmt vet ## Build the manager binary into bin/
	go build -o bin/manager ./cmd/manager

.PHONY: run
run: fmt vet ## Run the manager locally against the current kubeconfig
	go run ./cmd/manager --config hack/config.local.yaml

.PHONY: generate
generate: ## Regenerate DeepCopy methods (zz_generated.deepcopy.go)
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

.PHONY: manifests
manifests: ## Regenerate CRDs and ClusterRole from kubebuilder markers
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:artifacts:config=$(CRD_DIR)
	$(CONTROLLER_GEN) rbac:roleName=maintenance-orchestrator paths="./internal/controller/..." output:rbac:artifacts:config=$(RBAC_DIR)

.PHONY: docker-build
docker-build: ## Build the container image ($(IMG))
	docker build -t $(IMG) .

.PHONY: docker-push
docker-push: ## Push the container image ($(IMG))
	docker push $(IMG)

.PHONY: install
install: ## Install CRDs into the current cluster
	kubectl apply -f $(CRD_DIR)

.PHONY: uninstall
uninstall: ## Remove CRDs from the current cluster
	kubectl delete -f $(CRD_DIR) --ignore-not-found

.PHONY: deploy
deploy: ## Deploy namespace, CRDs, RBAC, config and the manager
	kubectl apply -f deploy/manager/namespace.yaml
	kubectl apply -f $(CRD_DIR)
	kubectl apply -f $(RBAC_DIR)
	kubectl apply -f deploy/manager/configmap.yaml
	kubectl apply -f deploy/manager/deployment.yaml
	kubectl apply -f deploy/manager/service.yaml

.PHONY: undeploy
undeploy: ## Remove the controller deployment from the cluster
	kubectl delete -f deploy/manager/service.yaml --ignore-not-found
	kubectl delete -f deploy/manager/deployment.yaml --ignore-not-found
	kubectl delete -f deploy/manager/configmap.yaml --ignore-not-found
	kubectl delete -f $(RBAC_DIR) --ignore-not-found
