#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# CONFIGURATION
# ============================================================
BATCH_SIZE="${BATCH_SIZE:-36}"
NUM_BATCHES="${NUM_BATCHES:-20}"
WARMUP_SIZE="${WARMUP_SIZE:-12}"

TMPDIR="$(mktemp -d)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
UPSTREAM_KCFG="${TMPDIR}/upstream.kubeconfig"
FORK_KCFG="${TMPDIR}/fork.kubeconfig"

UPSTREAM_PORT_LOCAL=${UPSTREAM_PORT_LOCAL:-9098}
FORK_PORT_LOCAL=${FORK_PORT_LOCAL:-9099}
UPSTREAM_PORT_REMOTE=${UPSTREAM_PORT_REMOTE:-9097}
FORK_PORT_REMOTE=${FORK_PORT_REMOTE:-9097}

REQUIRED_CMDS=(kind kubectl jq yq date mktemp)
OPTIONAL_CMDS=(parallel ko)

declare -a BG_PIDS=()

# ============================================================
# LOGGING HELPERS
# ============================================================
log() { echo -e "\033[1;34m[$(date +%H:%M:%S)]\033[0m $*"; }
phase() { echo -e "\n\033[1;33m==== $* ====\033[0m\n"; }

# ============================================================
# CLEANUP FUNCTION
# ============================================================
cleanup() {
  log "Cleaning up temporary files..."
  if [ "${#BG_PIDS[@]}" -gt 0 ]; then
    for pid in "${BG_PIDS[@]}"; do
      if kill -0 "${pid}" >/dev/null 2>&1; then
        log "Killing PID ${pid}"
        kill "${pid}" >/dev/null 2>&1 || true
      fi
    done
    sleep 1
  fi
  rm -rf "${TMPDIR}" || true

  log "Deleting kind clusters..."
  kind delete cluster --name tekton-upstream >/dev/null 2>&1 || true
  kind delete cluster --name tekton-fork >/dev/null 2>&1 || true
}

# ============================================================
# EARLY CLEANUP-ONLY MODE
# ============================================================
if [[ "${1:-}" == "--cleanup-only" || "${1:-}" == "-C" ]]; then
  log "Running cleanup-only mode..."
  cleanup
  exit 0
fi

# ============================================================
# PREFLIGHT CHECKS
# ============================================================
missing=()
for c in "${REQUIRED_CMDS[@]}"; do
  if ! command -v "$c" >/dev/null 2>&1; then
    missing+=("$c")
  fi
done
if [ "${#missing[@]}" -gt 0 ]; then
  log "ERROR: Missing required commands: ${missing[*]}"
  exit 1
fi

if command -v parallel >/dev/null 2>&1; then
  if parallel --version 2>&1 | grep -q "GNU Parallel"; then
    PARALLEL_BIN="parallel"
  else
    log "Warning: Detected 'parallel' from moreutils; will use sequential cluster creation instead."
    PARALLEL_BIN=""
  fi
else
  PARALLEL_BIN=""
fi

# ============================================================
# CLUSTER CREATION
# ============================================================
phase "Creating kind clusters"

create_cluster() {
  local name="$1" kubeconf="$2"
  log "Creating cluster '${name}'..."
  kind create cluster --name "${name}" --wait 120s >/dev/null
  kind get kubeconfig --name "${name}" > "${kubeconf}"
  log "Cluster '${name}' ready."
}


if [[ -n "$PARALLEL_BIN" ]]; then
  export -f create_cluster log
  "$PARALLEL_BIN" -j 2 --link create_cluster ::: "tekton-upstream" "tekton-fork" ::: "${UPSTREAM_KCFG}" "${FORK_KCFG}"
else
  create_cluster "tekton-upstream" "${UPSTREAM_KCFG}" &
  p1=$!
  create_cluster "tekton-fork" "${FORK_KCFG}" &
  p2=$!
  wait "${p1}" "${p2}"
fi

# trap only after successful cluster creation
trap 'read -p "Run cleanup before exit? [y/N] " resp; if [[ "$resp" =~ ^[Yy]$ ]]; then cleanup; fi' EXIT

# ============================================================
# DEPLOY TEKTON + DASHBOARD
# ============================================================
phase "Deploying Tekton and Dashboard"

set_context_from_kind() {
  local name="$1"
  export KUBECONFIG="$(mktemp)"
  kind get kubeconfig --name "$name" > "$KUBECONFIG"
}

deploy_upstream() {
  log "Deploying upstream Tekton..."
  kubectl --kubeconfig="${UPSTREAM_KCFG}" create namespace "tekton-pipelines"
  kubectl --kubeconfig="${UPSTREAM_KCFG}" apply -f https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml
  kubectl --kubeconfig="${UPSTREAM_KCFG}" apply -f https://storage.googleapis.com/tekton-releases/dashboard/latest/release.yaml
}

deploy_fork() {
  log "Deploying forked Tekton..."
  if command -v ko >/dev/null 2>&1; then
    set_context_from_kind "tekton-fork"
    kubectl create namespace "tekton-pipelines"
    # KO_DOCKER_REPO=kind.local ko apply -f config/
    ko apply -R -f config/
    kubectl patch configmap feature-flags -n tekton-pipelines --type merge -p '{"data":{"enable-api-fields":"alpha"}}'
    unset KUBECONFIG
  else
    log "ko not found; falling back to kubectl apply -f config/"
    kubectl --kubeconfig="${FORK_KCFG}" apply -f config/
  fi
  kubectl --kubeconfig="${FORK_KCFG}" apply -f https://storage.googleapis.com/tekton-releases/dashboard/latest/release.yaml
}

deploy_upstream &
deploy_fork &
wait

# ============================================================
# WAIT FOR NAMESPACES & PODS
# ============================================================
phase "Waiting for Tekton components to become ready"

wait_for_ns() {
  local cfg="$1"
  local ns="tekton-pipelines"
  until kubectl --kubeconfig="$cfg" get ns "$ns" >/dev/null 2>&1; do
    log "$(basename "$cfg"): waiting for namespace ${ns}..."
    sleep 2
  done
}

wait_for_ns "${UPSTREAM_KCFG}"
wait_for_ns "${FORK_KCFG}"

for cfg in "${UPSTREAM_KCFG}" "${FORK_KCFG}"; do
  kubectl --kubeconfig="$cfg" wait pod --for=condition=Ready -l app.kubernetes.io/part-of=tekton-pipelines -A --timeout=300s || {
    log "Warning: Some Tekton pods not ready yet in $(basename "$cfg")"
  }
done

# ============================================================
# PORT-FORWARD DASHBOARDS
# ============================================================
phase "Starting dashboard port-forwards"

port_forward_loop() {
  local kubeconf="$1" ns="$2" svc="$3" local_port="$4" remote_port="$5" label="$6"
  until kubectl --kubeconfig="$kubeconf" -n "$ns" get svc "$svc" >/dev/null 2>&1; do
    log "${label}: waiting for svc/${svc} in ns/${ns}..."
    sleep 2
  done
  while true; do
    # log "${label}: forwarding ${local_port}→${remote_port}"
    kubectl --kubeconfig="$kubeconf" -n "$ns" port-forward svc/"$svc" "${local_port}:${remote_port}" >/dev/null 2>&1 || true
    sleep 3
  done
}

port_forward_loop "${UPSTREAM_KCFG}" "tekton-pipelines" "tekton-dashboard" "${UPSTREAM_PORT_LOCAL}" "${UPSTREAM_PORT_REMOTE}" "UPSTREAM" &
BG_PIDS+=($!)
port_forward_loop "${FORK_KCFG}" "tekton-pipelines" "tekton-dashboard" "${FORK_PORT_LOCAL}" "${FORK_PORT_REMOTE}" "FORK" &
BG_PIDS+=($!)

log "Port-forwards started. Dashboards should be available at:"
log "  Upstream: http://localhost:${UPSTREAM_PORT_LOCAL}"
log "  Fork:     http://localhost:${FORK_PORT_LOCAL}"

# The benchmarking steps would continue here...
phase "Setup complete — ready for benchmark execution"

# ============================================================
# APPLY TASKS
# ============================================================
phase "Applying benchmark tasks"
for f in ./benchmarking-task.yaml ./benchmarking-task-run.yaml ./benchmarking-task-test.yaml ./benchmarking-task-test-run.yaml; do
  [ -f "$f" ] || { log "Missing $f"; exit 3; }
done
kubectl --kubeconfig="${UPSTREAM_KCFG}" apply -f ./benchmarking-task.yaml
kubectl --kubeconfig="${FORK_KCFG}" apply -f ./benchmarking-task.yaml
kubectl --kubeconfig="${FORK_KCFG}" apply -f ./benchmarking-task-test.yaml

# ============================================================
# BATCH HELPERS
# ============================================================
wait_for_runs() {
  local kubeconf="$1" kind="$2"
  while true; do
    local total succeeded
    total=$(kubectl --kubeconfig="$kubeconf" get "$kind" -o json | jq '.items | length')
    (( total == 0 )) && sleep 3 && continue
    succeeded=$(kubectl --kubeconfig="$kubeconf" get "$kind" -o json | jq '[.items[] | select(any(.status.conditions[]?; .type=="Succeeded" and .status=="True"))] | length')
    (( succeeded == total )) && break
    log "$kind: $succeeded/$total completed"
    sleep 5
  done
}

launch_batch() {
  local kubeconf="$1" manifest="$2" batch_id="$3" size="$4"
  for i in $(seq 1 "$size"); do
  yq eval ".metadata.labels.\"bench.batchID\" = \"${batch_id}\"" "$manifest" \
      | kubectl --kubeconfig="$kubeconf" create -f - >/dev/null
    sleep 0.05
  done
}

# ============================================================
# WARMUP PHASE
# ============================================================
phase "Warmup"
launch_batch "${UPSTREAM_KCFG}" ./benchmarking-task-run.yaml "warmup-upstream" "${WARMUP_SIZE}"
wait_for_runs "${UPSTREAM_KCFG}" taskrun
launch_batch "${FORK_KCFG}" ./benchmarking-task-test-run.yaml "warmup-fork" "${WARMUP_SIZE}"
wait_for_runs "${FORK_KCFG}" tasktestrun

# ============================================================
# EXPERIMENT PHASE
# ============================================================
phase "Experiment runs"
UPSTREAM_METRICS="${TIMESTAMP}-metrics-upstream.txt"
FORK_METRICS="${TIMESTAMP}-metrics-fork.txt"

for batch_num in $(seq 1 "${NUM_BATCHES}"); do
  if (( batch_num % 2 == 1 )); then
    BATCH_ID="c-${TIMESTAMP}-$(printf '%02d' "$batch_num")"
    launch_batch "${UPSTREAM_KCFG}" ./benchmarking-task-run.yaml "$BATCH_ID" "$BATCH_SIZE"
    wait_for_runs "${UPSTREAM_KCFG}" taskrun
    kubectl --kubeconfig="${UPSTREAM_KCFG}" get taskruns -o json >"${BATCH_ID}-taskruns.json"
  else
    BATCH_ID="t-${TIMESTAMP}-$(printf '%02d' "$batch_num")"
    launch_batch "${FORK_KCFG}" ./benchmarking-task-test-run.yaml "$BATCH_ID" "$BATCH_SIZE"
    wait_for_runs "${FORK_KCFG}" tasktestrun
    kubectl --kubeconfig="${FORK_KCFG}" get tasktestruns -o json >"${BATCH_ID}-tasktestruns.json"
  fi
done

# ============================================================
# METRICS AGGREGATION
# ============================================================
phase "Collecting results"
kubectl --kubeconfig="${UPSTREAM_KCFG}" get taskruns -o json >"${TIMESTAMP}-upstream-taskruns-full.json"
kubectl --kubeconfig="${FORK_KCFG}" get tasktestruns -o json >"${TIMESTAMP}-fork-tasktestruns-full.json"

for f in upstream-taskruns fork-tasktestruns; do
  jq -r '
    .items[] |
    [
      (.metadata.labels.bench.batchID // ""),
      (.metadata.name // ""),
      (.status.startTime // ""),
      ((.status.completionTime // "") as $ct | (.status.startTime // "") as $st |
       (if ($ct == "" or $st == "") then "" else ( ($ct | fromdate) - ($st | fromdate) ) end))
    ] | @csv
  ' "${TIMESTAMP}-${f}-full.json" >"${TIMESTAMP}-${f}-summary.csv"
done

phase "Done"
log "Results written to ${TIMESTAMP}-* files"
$CLEANUP_AFTER && log "Clusters deleted as requested"
