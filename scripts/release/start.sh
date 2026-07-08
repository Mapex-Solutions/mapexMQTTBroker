#!/bin/bash
# =============================================================================
# Mapex MQTT Broker — Release Script (build, tag, push)
# =============================================================================
#
# Builds the mapex-broker-mqtt image and optionally pushes it to a Docker
# registry. Mirrors the layout of mapexOS/scripts/release/start.sh so the
# operator workflow is the same across the suite.
#
# CREDENTIAL POLICY — NO SECRETS IN THIS SCRIPT.
#
# This script never reads, prints, or echoes Docker registry credentials.
# Authentication is the operator's responsibility and MUST be done out
# of band, in one of these ways:
#
#   1. `docker login <registry>` (interactive, recommended for laptops)
#   2. `cat ~/.docker-token | docker login --password-stdin -u $USER ...`
#      (CI/CD — keep the token in a secret manager, never on disk in
#       plain text alongside this repo)
#   3. A credential helper (`docker-credential-pass`, ECR helper, etc.)
#
# If the operator runs `--push` without an active session, `docker push`
# fails — that's the intended behaviour. Do NOT add a login flow here.
#
# Usage:
#   ./scripts/release/start.sh <version> [options]
#
# Options:
#   --push               Push image to the configured registry after build
#   --latest             Also tag the image as ':latest' (built locally;
#                        pushed only when --push is also set)
#   --no-cache           Force rebuild without Docker layer cache
#   --multiarch          Build linux/amd64 + linux/arm64 via buildx
#                        (buildx must be initialized; we don't bootstrap it)
#   --registry <url>     Override registry (default: docker.io/thiagoanselmo)
#                        Equivalent to setting DOCKER_REGISTRY env var
#   --image <name>       Override image name (default: mapex-broker-mqtt)
#   --inspect            After build, run `docker inspect` and show labels
#   --list               Show the resolved image coordinates and exit
#
# Examples:
#   ./scripts/release/start.sh 0.1.0
#   ./scripts/release/start.sh 0.1.0 --push --latest
#   ./scripts/release/start.sh 0.1.0 --multiarch --push
#   ./scripts/release/start.sh 0.1.0 --no-cache
#   ./scripts/release/start.sh 0.2.0-rc.1 --push
#   ./scripts/release/start.sh --list
#
# Environment overrides:
#   DOCKER_REGISTRY      default: docker.io/thiagoanselmo
#   DOCKER_IMAGE_NAME    default: mapex-broker-mqtt
#   DOCKERFILE           default: docker/Dockerfile (relative to repo root)
#
# Exit codes:
#   0  success
#   1  invalid arguments / version
#   2  docker build failed
#   3  docker push failed
#   4  docker not available
#
# =============================================================================

set -euo pipefail

# -----------------------------------------------------------------------------
# Configurable knobs (env overrides land here; never bake secrets).
# -----------------------------------------------------------------------------
DOCKER_REGISTRY="${DOCKER_REGISTRY:-docker.io/thiagoanselmo}"
DOCKER_IMAGE_NAME="${DOCKER_IMAGE_NAME:-mapex-broker-mqtt}"
DOCKERFILE="${DOCKERFILE:-docker/Dockerfile}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Colors — simple terminal palette; falls back to plain when no TTY.
if [ -t 1 ]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    CYAN='\033[0;36m'
    NC='\033[0m'
else
    RED=''
    GREEN=''
    YELLOW=''
    CYAN=''
    NC=''
fi

# -----------------------------------------------------------------------------
# Usage / list
# -----------------------------------------------------------------------------
usage() {
    cat <<EOF
Usage: $0 <version> [options]

Arguments:
  version              Semantic version (e.g., 0.1.0, 1.2.3, 0.2.0-rc.1)

Options:
  --push               Push image to the registry after build
  --latest             Also tag the image as ':latest'
  --no-cache           Force rebuild without Docker cache
  --multiarch          Build linux/amd64 + linux/arm64 via buildx
  --registry <url>     Override registry (default: ${DOCKER_REGISTRY})
  --image <name>       Override image name (default: ${DOCKER_IMAGE_NAME})
  --inspect            Show OCI labels of the built image
  --list               Show resolved image coordinates and exit
  -h, --help           Show this help

Environment overrides:
  DOCKER_REGISTRY      default: docker.io/thiagoanselmo
  DOCKER_IMAGE_NAME    default: mapex-broker-mqtt
  DOCKERFILE           default: docker/Dockerfile

Authentication:
  This script does NOT log into the registry. Run 'docker login' (or
  use a credential helper) before invoking with --push. Credentials
  are never read by this script.

Examples:
  $0 0.1.0
  $0 0.1.0 --push --latest
  $0 0.1.0 --multiarch --push
  $0 0.2.0-rc.1 --push --no-cache
EOF
    exit 1
}

list_coords() {
    local image="${DOCKER_REGISTRY}/${DOCKER_IMAGE_NAME}"
    echo ""
    echo "Image coordinates:"
    echo ""
    echo -e "  ${CYAN}registry:${NC}   ${DOCKER_REGISTRY}"
    echo -e "  ${CYAN}name:${NC}       ${DOCKER_IMAGE_NAME}"
    echo -e "  ${CYAN}dockerfile:${NC} ${DOCKERFILE}"
    echo -e "  ${CYAN}context:${NC}    ${ROOT_DIR}"
    echo ""
    echo "Resolved image: ${image}:<version>"
    echo ""
    exit 0
}

# -----------------------------------------------------------------------------
# Validation
# -----------------------------------------------------------------------------
validate_version() {
    if [[ ! "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
        echo -e "${RED}ERROR: Invalid version '$1'. Must be semver (e.g., 0.1.0, 1.2.3-rc.1)${NC}" >&2
        exit 1
    fi
}

ensure_docker() {
    if ! command -v docker >/dev/null 2>&1; then
        echo -e "${RED}ERROR: docker command not found in PATH.${NC}" >&2
        exit 4
    fi
}

ensure_buildx() {
    if ! docker buildx version >/dev/null 2>&1; then
        echo -e "${RED}ERROR: docker buildx not available. Install buildx or drop --multiarch.${NC}" >&2
        exit 4
    fi
}

# -----------------------------------------------------------------------------
# Build
# -----------------------------------------------------------------------------
build_single() {
    local image="$1"
    local cache_flag=""
    [ "$NO_CACHE" = true ] && cache_flag="--no-cache"

    echo -e "${CYAN}  Building ${image} (single-arch host platform)...${NC}"
    echo "    context:    ${ROOT_DIR}"
    echo "    dockerfile: ${DOCKERFILE}"
    [ "$NO_CACHE" = true ] && echo "    cache:      disabled"

    docker build \
        $cache_flag \
        --build-arg IMAGE_VERSION="${VERSION}" \
        --build-arg VCS_REF="${VCS_REF}" \
        --build-arg BUILD_DATE="${BUILD_DATE}" \
        -t "${image}" \
        -f "${ROOT_DIR}/${DOCKERFILE}" \
        "${ROOT_DIR}"

    if [ "$TAG_LATEST" = true ]; then
        local image_latest="${DOCKER_REGISTRY}/${DOCKER_IMAGE_NAME}:latest"
        docker tag "${image}" "${image_latest}"
        echo -e "${GREEN}  also tagged as ${image_latest}${NC}"
    fi

    echo -e "${GREEN}  ✓ ${image}${NC}"
    echo ""
}

build_multiarch() {
    local image="$1"
    local cache_flag=""
    local push_flag="--load"
    [ "$NO_CACHE" = true ] && cache_flag="--no-cache"

    # buildx multi-arch images cannot be --load'd into the local Docker
    # daemon (it only stores one platform). When multiarch is requested
    # without --push, we warn and produce an arch-specific image instead.
    if [ "$DO_PUSH" = true ]; then
        push_flag="--push"
    else
        echo -e "${YELLOW}WARNING: --multiarch without --push falls back to single-arch (host platform)${NC}"
        echo -e "${YELLOW}         to keep the image loadable. Use --push for a true multi-arch manifest.${NC}"
        build_single "${image}"
        return
    fi

    echo -e "${CYAN}  Building ${image} (linux/amd64 + linux/arm64) and pushing manifest...${NC}"
    echo "    context:    ${ROOT_DIR}"
    echo "    dockerfile: ${DOCKERFILE}"

    docker buildx build \
        $cache_flag \
        --platform linux/amd64,linux/arm64 \
        --build-arg IMAGE_VERSION="${VERSION}" \
        --build-arg VCS_REF="${VCS_REF}" \
        --build-arg BUILD_DATE="${BUILD_DATE}" \
        -t "${image}" \
        $([ "$TAG_LATEST" = true ] && echo "-t ${DOCKER_REGISTRY}/${DOCKER_IMAGE_NAME}:latest") \
        ${push_flag} \
        -f "${ROOT_DIR}/${DOCKERFILE}" \
        "${ROOT_DIR}"

    echo -e "${GREEN}  ✓ multi-arch manifest ${image} pushed${NC}"
    if [ "$TAG_LATEST" = true ]; then
        echo -e "${GREEN}  ✓ multi-arch manifest ${DOCKER_REGISTRY}/${DOCKER_IMAGE_NAME}:latest pushed${NC}"
    fi
    echo ""
}

push_single() {
    local image="$1"
    echo -e "${CYAN}  Pushing ${image}...${NC}"
    docker push "${image}"

    if [ "$TAG_LATEST" = true ]; then
        local image_latest="${DOCKER_REGISTRY}/${DOCKER_IMAGE_NAME}:latest"
        echo -e "${CYAN}  Pushing ${image_latest}...${NC}"
        docker push "${image_latest}"
    fi

    echo -e "${GREEN}  ✓ ${image} pushed${NC}"
}

inspect_image() {
    local image="$1"
    echo ""
    echo -e "${CYAN}  Image labels:${NC}"
    docker image inspect "${image}" --format='{{json .Config.Labels}}' | python3 -m json.tool 2>/dev/null \
        || docker image inspect "${image}" --format='{{json .Config.Labels}}'
    echo ""
}

# -----------------------------------------------------------------------------
# Argument parsing
# -----------------------------------------------------------------------------
if [ $# -lt 1 ]; then
    usage
fi

# Handle commands that don't need a version first.
case "$1" in
    --list)        list_coords ;;
    -h|--help)     usage ;;
esac

VERSION="$1"
DO_PUSH=false
TAG_LATEST=false
NO_CACHE=false
MULTIARCH=false
DO_INSPECT=false

shift
while [ $# -gt 0 ]; do
    case "$1" in
        --push)        DO_PUSH=true ;;
        --latest)      TAG_LATEST=true ;;
        --no-cache)    NO_CACHE=true ;;
        --multiarch)   MULTIARCH=true ;;
        --inspect)     DO_INSPECT=true ;;
        --registry)
            shift
            [ $# -eq 0 ] && { echo -e "${RED}ERROR: --registry requires a URL${NC}" >&2; exit 1; }
            DOCKER_REGISTRY="$1"
            ;;
        --image)
            shift
            [ $# -eq 0 ] && { echo -e "${RED}ERROR: --image requires a name${NC}" >&2; exit 1; }
            DOCKER_IMAGE_NAME="$1"
            ;;
        --list)        list_coords ;;
        -h|--help)     usage ;;
        *)             echo -e "${RED}Unknown option: $1${NC}" >&2; usage ;;
    esac
    shift
done

validate_version "${VERSION}"
ensure_docker
[ "$MULTIARCH" = true ] && ensure_buildx

if [ "$TAG_LATEST" = true ] && [ "$DO_PUSH" = false ] && [ "$MULTIARCH" = false ]; then
    echo -e "${YELLOW}WARNING: --latest has no effect without --push. Will tag locally anyway.${NC}"
fi

# VCS metadata baked into image labels for traceability. These are
# optional — `unknown` is acceptable when running outside a git checkout.
VCS_REF="$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_DATE="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"

IMAGE="${DOCKER_REGISTRY}/${DOCKER_IMAGE_NAME}:${VERSION}"

# -----------------------------------------------------------------------------
# Main
# -----------------------------------------------------------------------------
echo ""
echo "========================================================="
echo -e "  Mapex MQTT Broker Release ${CYAN}v${VERSION}${NC}"
echo "========================================================="
echo ""
echo "  Registry:     ${DOCKER_REGISTRY}"
echo "  Image:        ${DOCKER_IMAGE_NAME}"
echo "  Tag:          ${VERSION}"
echo "  VCS ref:      ${VCS_REF}"
echo "  Build date:   ${BUILD_DATE}"
echo "  Push:         ${DO_PUSH}"
echo "  Tag latest:   ${TAG_LATEST}"
echo "  No cache:     ${NO_CACHE}"
echo "  Multi-arch:   ${MULTIARCH}"
echo ""

# --- Build ---
echo "=== Building ==="
echo ""

if [ "$MULTIARCH" = true ]; then
    build_multiarch "${IMAGE}"
else
    build_single "${IMAGE}"
fi

# --- Push (only relevant for single-arch; multiarch path already pushes via buildx) ---
if [ "$DO_PUSH" = true ] && [ "$MULTIARCH" = false ]; then
    echo "========================================================="
    echo "  Pushing to ${DOCKER_REGISTRY}"
    echo "========================================================="
    echo ""
    push_single "${IMAGE}"
    echo ""
fi

# --- Inspect ---
if [ "$DO_INSPECT" = true ] && [ "$MULTIARCH" = false ]; then
    inspect_image "${IMAGE}"
fi

# --- Summary ---
echo "========================================================="
echo "  Summary"
echo "========================================================="
echo ""

if [ "$MULTIARCH" = false ]; then
    SIZE=$(docker image inspect "${IMAGE}" --format='{{.Size}}' 2>/dev/null || echo "0")
    SIZE_MB=$((SIZE / 1048576))
    echo -e "  ${GREEN}${IMAGE}${NC}  (${SIZE_MB} MB, host platform)"
    if [ "$TAG_LATEST" = true ]; then
        echo -e "  ${GREEN}${DOCKER_REGISTRY}/${DOCKER_IMAGE_NAME}:latest${NC}  (alias)"
    fi
else
    echo -e "  ${GREEN}${IMAGE}${NC}  (multi-arch manifest: linux/amd64, linux/arm64)"
    if [ "$TAG_LATEST" = true ]; then
        echo -e "  ${GREEN}${DOCKER_REGISTRY}/${DOCKER_IMAGE_NAME}:latest${NC}  (multi-arch alias)"
    fi
fi

echo ""
echo -e "${GREEN}  Release v${VERSION} complete.${NC}"
echo ""

if [ "$DO_PUSH" = false ] && [ "$MULTIARCH" = false ]; then
    echo -e "  ${YELLOW}Image built locally. Use --push to upload to the registry.${NC}"
    echo -e "  ${YELLOW}Authenticate first via 'docker login ${DOCKER_REGISTRY%%/*}'.${NC}"
    echo ""
fi
