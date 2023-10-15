#!/usr/bin/env bash

set -euo pipefail

cd $(dirname $0)/..

version="${1:-}"
version=${version#"v"}

if [ -z "$version" ]; then
  echo "Usage: $0 VERSION"
  echo "Hint: Version prefix 'v' is automatically trimmed."
  exit 1
fi

if git tag | grep "v$version" >/dev/null; then
  echo "Version is already tagged."
  exit 1
fi

set_version() {
  yq -i contrib/kubernetes/deployment.yaml 'spec.template.spec.containers[0].image' "ghcr.io/xrstf/prow_exporter:$1"
}

set_version "$version"
git commit -am "version $version"
git tag -m "version $version" "v$version"

set_version "latest"
git commit -am "back to dev"
