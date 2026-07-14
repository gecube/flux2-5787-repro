#!/usr/bin/env bash
# Reproduction for https://github.com/fluxcd/flux2/issues/5787
set -euo pipefail
cd "$(dirname "$0")"

echo "==> 1. plain kustomize: base alone (tag set via images/newTag)"
kustomize build base | grep 'image:'

echo "==> 2. plain kustomize: overlay changing only the image name (tag preserved)"
kustomize build overlay | grep 'image:'

echo "==> 3. flux build: spec.images changing only the image name (tag LOST)"
flux build kustomization myapp -n default \
  --path ./base \
  --kustomization-file flux/flux-kustomization.yaml \
  --dry-run | grep 'image:'
