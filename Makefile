###############################################################################
# Mapex MQTT Broker — build + publish workflow
#
# Common entry points for the image lifecycle. Run from this repo root:
#
#   make build           local single-arch image
#   make test            Go-pure unit tests
#   make build-multiarch linux/amd64 + linux/arm64 (no push)
#   make push            push current image to the registry
#   make tag-latest      tag current image as :latest and push both
#   make release         build-multiarch + push (used by CI on git tags)
#   make inspect         show OCI labels of the built image
#   make clean           remove local image artifacts
#
###############################################################################

# Image coordinates. Override REGISTRY at the command line for a private
# registry; the default targets the thiagoanselmo Docker Hub namespace.
REGISTRY  ?= docker.io/thiagoanselmo
IMAGE_NAME ?= mapex-broker-mqtt
VERSION   ?= dev

# Build context = repo root (this Makefile lives at the root).
CONTEXT    := .
DOCKERFILE := docker/Dockerfile

# VCS metadata baked into image labels for traceability.
VCS_REF    := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

IMAGE        := $(REGISTRY)/$(IMAGE_NAME):$(VERSION)
IMAGE_LATEST := $(REGISTRY)/$(IMAGE_NAME):latest

.PHONY: help
help:
	@echo "Mapex MQTT Broker — image targets"
	@echo ""
	@echo "  make vendor          Regenerate the offline vendor tree (runs before every build)"
	@echo "  make build           Build single-arch image $(IMAGE) for the host platform"
	@echo "  make build-multiarch Build linux/amd64+arm64 via buildx (no push)"
	@echo "  make test            Run Go-pure unit tests (no broker required)"
	@echo "  make push            Push $(IMAGE) to the registry"
	@echo "  make tag-latest      Tag the current image as :latest and push both"
	@echo "  make release         build-multiarch + push (used by CI on git tags)"
	@echo "  make inspect         Show OCI labels of the built image"
	@echo "  make clean           Remove local image artifacts"
	@echo ""
	@echo "Image: $(IMAGE)"
	@echo "VCS:   $(VCS_REF)"

# The image build is fully offline via -mod=vendor. Regenerate the vendor tree
# from the sibling mapexGoKit checkout before every image build so a gokit
# change is always picked up (the tree itself is gitignored).
.PHONY: vendor
vendor:
	GOWORK=off go mod vendor

.PHONY: build
build: vendor
	docker build \
		--build-arg IMAGE_VERSION=$(VERSION) \
		--build-arg VCS_REF=$(VCS_REF) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(IMAGE) \
		-f $(DOCKERFILE) \
		$(CONTEXT)

.PHONY: build-multiarch
build-multiarch: vendor
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg IMAGE_VERSION=$(VERSION) \
		--build-arg VCS_REF=$(VCS_REF) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(IMAGE) \
		-f $(DOCKERFILE) \
		$(CONTEXT)

.PHONY: test
test:
	go test -race ./src/...

.PHONY: push
push:
	docker push $(IMAGE)

.PHONY: tag-latest
tag-latest:
	docker tag $(IMAGE) $(IMAGE_LATEST)
	docker push $(IMAGE_LATEST)

.PHONY: release
release: vendor
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg IMAGE_VERSION=$(VERSION) \
		--build-arg VCS_REF=$(VCS_REF) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(IMAGE) \
		-t $(IMAGE_LATEST) \
		-f $(DOCKERFILE) \
		--push \
		$(CONTEXT)

.PHONY: inspect
inspect:
	docker inspect $(IMAGE) --format '{{json .Config.Labels}}' | jq .

.PHONY: clean
clean:
	-docker rmi $(IMAGE) 2>/dev/null
	-docker rmi $(IMAGE_LATEST) 2>/dev/null
