#!/usr/bin/env bash
set -euo pipefail

: "${KWOK_REPORT_DIR:?KWOK_REPORT_DIR is required}"
: "${KWOK_CLUSTER_NAME:=karpenter-kwok-e2e}"
: "${KWOK_REPORT_PRESERVE:=false}"
mkdir -p "${KWOK_REPORT_DIR}"

capture() {
  local path="${KWOK_REPORT_DIR}/$1"
  shift
  if [[ "${KWOK_REPORT_PRESERVE}" == "true" && -s "${path}" ]]; then
    return
  fi
  kubectl --context="kind-${KWOK_CLUSTER_NAME}" --request-timeout=10s "$@" >"${path}" 2>&1 || true
}

capture nodes.txt get nodes -o wide
capture nodeclaims.txt get nodeclaims -A -o wide
capture pods.txt get pods -A -o wide
capture events.txt get events -A --sort-by=.lastTimestamp
capture karpenter.log logs -n kube-system deployment/karpenter --all-containers --tail=-1
