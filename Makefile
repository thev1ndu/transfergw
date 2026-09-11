.PHONY: help build run test test-integration test-e2e deploy clean generate manifests docker-build docker-push

# Variables
IMG ?= transfergw-controller:latest
REGISTRY ?= docker.io/example
CONTROLLER_GEN ?= $(ENVTEST)/controller-gen
ENVTEST ?= $(shell pwd)/bin/envtest

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=transfergw-controller crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use 1.26 -p path)" go test ./... -coverprofile=cover.out

.PHONY: test-integration
test-integration: test ## Run integration tests.
	go test ./controllers -v -tags=integration

.PHONY: test-e2e
test-e2e: ## Run end-to-end tests (requires cluster).
	go test ./tests/e2e -v -count=1

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./main.go --leader-elect=false

.PHONY: docker-build
docker-build: test ## Build docker image with the manager.
	docker build -t ${REGISTRY}/${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${REGISTRY}/${IMG}

.PHONY: docker-build-push
docker-build-push: docker-build docker-push ## Build and push docker image.

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${REGISTRY}/${IMG}
	$(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/default | kubectl delete -f -

.PHONY: install-crd
install-crd: manifests kustomize ## Install CRD into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: uninstall-crd
uninstall-crd: manifests kustomize ## Uninstall CRD from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl delete -f -

.PHONY: controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	test -s $(CONTROLLER_GEN) || { curl -Ss "https://raw.githubusercontent.com/kubernetes-sigs/controller-tools/master/hack/install.sh" | sh -s -- $(subst controller-gen,,$(CONTROLLER_GEN)); }

.PHONY: kustomize
kustomize: ## Download kustomize locally if necessary.
	test -s $(KUSTOMIZE) || { curl -Ss "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" | bash -s -- $(subst kustomize,,$KUSTOMIZE); }

.PHONY: envtest
envtest: ## Download envtest-setup locally if necessary.
	test -d $(ENVTEST) || mkdir -p $(ENVTEST)
	test -f $(ENVTEST)/setup-envtest.sh || curl -Ss https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/master/hack/setup-envtest.sh | bash -s -- latest

.PHONY: clean
clean: ## Clean build artifacts.
	rm -rf bin/
	rm -f cover.out

.PHONY: kind-create
kind-create: ## Create local kind cluster for testing.
	kind create cluster --name transfergw --image kindest/node:v1.26.0

.PHONY: kind-delete
kind-delete: ## Delete local kind cluster.
	kind delete cluster --name transfergw

.PHONY: kind-load
kind-load: docker-build ## Load docker image into kind cluster.
	kind load docker-image ${REGISTRY}/${IMG} --name transfergw

.PHONY: lint
lint: ## Run golangci-lint.
	golangci-lint run ./...

.PHONY: verify
verify: fmt vet lint test ## Run all verification tests.

.PHONY: docs
docs: ## Generate API documentation.
	go run github.com/ahmetb/gen-crd-api-reference-docs@v0.3.0 \
		-api-dir ./api/v1beta1 \
		-config ./hack/api-docs-config.json \
		-template-dir ./hack/api-docs-templates \
		-out-file docs/api.md
