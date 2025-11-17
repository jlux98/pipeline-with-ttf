#!/usr/bin/env bash
set -eu

# ============================================================
# CONFIGURATION
# ============================================================

while [[ $# -gt 0 ]]; do
  case $1 in
    -C|--cleanup-only)
      CLEANUP_ONLY="YES"
      shift # past argument
      ;;

    -d|--destructive-mode)
      DESTRUCTIVE_MODE="YES"
      shift # past argument
      ;;
    --skip-setup)
      SKIP_SETUP="YES"
      shift # past argument
      ;;
    -s|--batch-size)
      BATCH_SIZE="$2"
      shift # past argument
      shift # past value
      ;;
    -n|--batch-number)
      NUM_BATCHES="$2"
      shift # past argument
      shift # past value
      ;;
    -w|--warmup-size)
      WARMUP_SIZE="$2"
      shift # past argument
      shift # past value
      ;;
    -*|--*)
      echo "Unknown option $1"
      exit 1
      ;;
    *)
      POSITIONAL_ARGS+=("$1") # save positional arg
      shift # past argument
      ;;
  esac
done

set -- "${POSITIONAL_ARGS[@]}" # restore positional parameters


BATCH_SIZE="${BATCH_SIZE:-12}"
NUM_BATCHES="${NUM_BATCHES:-10}"
WARMUP_SIZE="${WARMUP_SIZE:-12}"
DESTRUCTIVE_MODE="${DESTRUCTIVE_MODE:-NO}"
SKIP_SETUP="${SKIP_SETUP:-NO}"
CLEANUP_ONLY="${CLEANUP_ONLY:-NO}"
PROJECT_ROOT=$(pwd | sed 's/ttf\/.*/ttf/g')

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
phase() { echo -e "\n\033[1;33m==== $1 ====\033[0m\n"; $2 ${3:-}; }

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
# PREFLIGHT CHECKS
# ============================================================
execute_preflight_checks(){
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
}

# ============================================================
# CLUSTER CREATION
# ============================================================

create_cluster() {
  local name="$1" kubeconf="$2" destructive_mode="$3"
  if [[ $destructive_mode == "YES" ]]; then
    set +e
      log "Deleting a preexisting cluster '${name}' if it exists."
      kind delete cluster --name "${name}"
    set -e
  fi
  kind create cluster --name "${name}" --wait 120s > /dev/null
  kind get kubeconfig --name "${name}" > "${kubeconf}"
  log "Cluster '${name}' ready."
}

execute_cluster_creation(){
  if [[ -n "$PARALLEL_BIN" ]]; then
    export -f create_cluster log
    log "Creating cluster tekton-upstream and tekton-fork in parallel"
    "$PARALLEL_BIN" -j 2 --link create_cluster ::: "tekton-upstream" "tekton-fork" ::: "${UPSTREAM_KCFG}" "${FORK_KCFG}" ::: "${DESTRUCTIVE_MODE}" "${DESTRUCTIVE_MODE}"
  else
    create_cluster "tekton-upstream" "${UPSTREAM_KCFG}" "${DESTRUCTIVE_MODE}" &
    p1=$!
    create_cluster "tekton-fork" "${FORK_KCFG}" "${DESTRUCTIVE_MODE}" &
    p2=$!
    wait "${p1}" "${p2}"
  fi

}

# ============================================================
# DEPLOY TEKTON + DASHBOARD
# ============================================================

set_context_from_kind() {
  local name="$1"
  export KUBECONFIG="$(mktemp)"
  kind get kubeconfig --name "$name" > "$KUBECONFIG"
}

deploy_tekton_latest() {
  local cfg="$1"
  log "Deploying upstream Tekton..."
  kubectl --kubeconfig="$cfg" create namespace "tekton-pipelines"
  kubectl --kubeconfig="$cfg" apply -f https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml
  kubectl --kubeconfig="$cfg" apply -f https://storage.googleapis.com/tekton-releases/dashboard/latest/release.yaml
  kubectl --kubeconfig="$cfg" patch configmap feature-flags -n tekton-pipelines --type merge -p '{"data":{"enable-api-fields":"alpha"}}'
}

deploy_fork() {
  log "Deploying forked Tekton..."
  deploy_tekton_latest "${FORK_KCFG}"
  kubectl --kubeconfig="${FORK_KCFG}" delete deployments.apps/tekton-events-controller -n tekton-pipelines
  kubectl --kubeconfig="${FORK_KCFG}" delete deployments.apps/tekton-pipelines-controller -n tekton-pipelines
  kubectl --kubeconfig="${FORK_KCFG}" delete deployments.apps/tekton-pipelines-webhook -n tekton-pipelines
  if command -v ko >/dev/null 2>&1; then
    set_context_from_kind "tekton-fork"
    # KO_DOCKER_REPO=kind.local ko apply -f "${PROJECT_ROOT}/config/"
    log "Starting ko now.."
    ko apply -R -f "${PROJECT_ROOT}/config/"
    unset KUBECONFIG
  else
    log "ko not found; falling back to kubectl apply -f config/"
    kubectl --kubeconfig="${FORK_KCFG}" apply -f config/
  fi
  kubectl --kubeconfig="${FORK_KCFG}" apply -f https://storage.googleapis.com/tekton-releases/dashboard/latest/release.yaml
}

execute_deployment_phase(){
  deploy_tekton_latest ${UPSTREAM_KCFG}
  deploy_fork
  wait
}

# ============================================================
# WAIT FOR NAMESPACES & PODS
# ============================================================

wait_for_ns() {
  local cfg="$1"
  local ns="tekton-pipelines"
  until kubectl --kubeconfig="$cfg" get ns "$ns" >/dev/null 2>&1; do
    log "$(basename "$cfg"): waiting for namespace ${ns}..."
    sleep 2
  done
}

execute_waiting_for_readyness_phase() {
  wait_for_ns "${UPSTREAM_KCFG}"
  wait_for_ns "${FORK_KCFG}"

  for cfg in "${UPSTREAM_KCFG}" "${FORK_KCFG}"; do
    kubectl --kubeconfig="$cfg" wait pod --for=condition=Ready -l app.kubernetes.io/part-of=tekton-pipelines -A --timeout=300s || {
      log "Warning: Some Tekton pods not ready yet in $(basename "$cfg")"
    }
  done
}

# ============================================================
# PORT-FORWARD DASHBOARDS
# ============================================================

port_forward_loop() {
  local kubeconf="$1" ns="$2" svc="$3" local_port="$4" remote_port="$5" label="$6"
  until kubectl --kubeconfig="$kubeconf" -n "$ns" get svc "$svc" >/dev/null 2>&1; do
    if [[ -d $TMPDIR ]]; then
      log "${label}: waiting for svc/${svc} in ns/${ns}..."
      sleep 2
    else 
      exit
    fi
  done
  while true; do
    # log "${label}: forwarding ${local_port}→${remote_port}"
    if [[ -d $TMPDIR ]]; then
      kubectl --kubeconfig="$kubeconf" -n "$ns" port-forward svc/"$svc" "${local_port}:${remote_port}" >/dev/null 2>&1 || true
      sleep 10
    else
      exit
    fi
  done
}

execute_start_port_forwards_phase() {
  port_forward_loop "${UPSTREAM_KCFG}" "tekton-pipelines" "tekton-dashboard" "${UPSTREAM_PORT_LOCAL}" "${UPSTREAM_PORT_REMOTE}" "UPSTREAM" &
  BG_PIDS+=($!)
  port_forward_loop "${FORK_KCFG}" "tekton-pipelines" "tekton-dashboard" "${FORK_PORT_LOCAL}" "${FORK_PORT_REMOTE}" "FORK" &
  BG_PIDS+=($!)

  log "Port-forwards started. Dashboards should be available at:"
  log "  Upstream: http://localhost:${UPSTREAM_PORT_LOCAL}"
  log "  Fork:     http://localhost:${FORK_PORT_LOCAL}"
}

# # The benchmarking steps would continue here...
# phase "Setup complete — ready for benchmark execution"

# ============================================================
# APPLY TASKS
# ============================================================
execute_apply_resources_phase() {
  local ns="$1"
  for f in "${PROJECT_ROOT}/performance-testing/benchmarking-task.yaml" "${PROJECT_ROOT}/performance-testing/benchmarking-task-run.yaml" "${PROJECT_ROOT}/performance-testing/benchmarking-task-test.yaml" "${PROJECT_ROOT}/performance-testing/benchmarking-task-test-run.yaml"; do
    [ -f "$f" ] || { log "Missing $f"; exit 3; }
  done
  kubectl --kubeconfig="${UPSTREAM_KCFG}" apply -f "${PROJECT_ROOT}/performance-testing/benchmarking-task.yaml" -n "$ns"
  kubectl --kubeconfig="${FORK_KCFG}"     apply -f "${PROJECT_ROOT}/performance-testing/benchmarking-task.yaml" -n "$ns"
  kubectl --kubeconfig="${FORK_KCFG}"     apply -f "${PROJECT_ROOT}/performance-testing/benchmarking-task-test.yaml" -n "$ns"
}

# ============================================================
# BATCH HELPERS
# ============================================================
wait_for_runs() {
  local kubeconf="$1" kind="$2" currentBatch="$3" count lastLog
  # total=$(kubectl --kubeconfig="$kubeconf" get "$kind" -o json | jq '.items | length')
  # succeeded=$(kubectl --kubeconfig="$kubeconf" get "$kind" -o json | jq '[.items[] | select(any(.status.conditions[]?; .type=="Succeeded" and ( .status=="True" or .status=="False" )))] | length')
  # lastLog=$(printf "batch $currentBatch/$NUM_BATCHES ($kind): $succeeded/$total completed")
  # log $lastLog
  lastLog="_"
  count="1"
  while true; do
    local total succeeded
    total=$(kubectl --kubeconfig="$kubeconf" get "$kind" -o json | jq '.items | length')
    (( total == 0 )) && sleep 3 && continue
    succeeded=$(kubectl --kubeconfig="$kubeconf" get "$kind" -o json | jq '[.items[] | select(any(.status.conditions[]?; .type=="Succeeded" and ( .status=="True" or .status=="False" )))] | length')
    (( succeeded == total )) && break
    newLog=$(printf "batch $currentBatch/$NUM_BATCHES ($kind): $succeeded/$total completed")
    if [[ $newLog == $lastLog ]]; then
      charToPrint="."
      if [[ $count == "6" ]]; then
        charToPrint="|"
        count=$((count-6))
      fi
      printf "$charToPrint"
      count=$((count + 1))
    else
      printf "\n"
      log "$newLog"
      lastLog=$newLog
      count="1"
    fi
    sleep 5
  done
}

launch_batch() {
  local kubeconf="$1" manifest="$2" batch_id="$3" batch_type="$4" size="$5" ns="$6"
  log "Launching batch $batch_id ($batch_type) with $size runs"
  for i in $(seq 1 "$size"); do
  yq eval ".metadata.labels.\"bench.ttf/batchId\" = \"${batch_id}\"" "$manifest" \
      | yq eval ".metadata.labels.\"bench.ttf/batchType\" = \"${batch_type}\"" \
      | kubectl --kubeconfig="$kubeconf" create -n "$ns" -f -
    sleep 0.05
  done
}

# ============================================================
# WARMUP PHASE
# ============================================================
execute_warmup_phase() {
  if [[  $WARMUP_SIZE -gt 0 ]]; then
    kubectl config set-context --kubeconfig="${UPSTREAM_KCFG}" --namespace=default kind-tekton-upstream
    kubectl config set-context --kubeconfig="${FORK_KCFG}" --namespace=default kind-tekton-fork
    launch_batch "${UPSTREAM_KCFG}" "${PROJECT_ROOT}/performance-testing/benchmarking-task-run.yaml" "warmup-upstream" "W" "${WARMUP_SIZE}" "default"
    wait_for_runs "${UPSTREAM_KCFG}" taskrun "warmup-upstream 0"
    launch_batch "${FORK_KCFG}" "${PROJECT_ROOT}/performance-testing/benchmarking-task-test-run.yaml" "warmup-fork" "W" "${WARMUP_SIZE}" "default"
    wait_for_runs "${FORK_KCFG}" tasktestrun "warmup-fork 0"
  fi
}

# ============================================================
# EXPERIMENT PHASE
# ============================================================

execute_experiment_runs() {
    mkdir "$PROJECT_ROOT/performance-testing/$TIMESTAMP"
    kubectl config set-context --kubeconfig="${UPSTREAM_KCFG}" --namespace=${TIMESTAMP} kind-tekton-upstream
    kubectl config set-context --kubeconfig="${FORK_KCFG}" --namespace=${TIMESTAMP} kind-tekton-fork
    UPSTREAM_METRICS="${TIMESTAMP}/metrics-upstream.txt"
    FORK_METRICS="${TIMESTAMP}/metrics-fork.txt"
    NUM_ITERATIONS=$((NUM_BATCHES * 2))
    batch_num=1
    for iter_num in $(seq 1 "${NUM_ITERATIONS}"); do
      BATCH_ID="${TIMESTAMP}-$(printf '%02d' $batch_num)"
      if (( iter_num % 2 == 1 )); then
        BATCH_ID="${BATCH_ID}-c"
        launch_batch "${UPSTREAM_KCFG}" "${PROJECT_ROOT}/performance-testing/benchmarking-task-run.yaml" "$BATCH_ID" "C" "$BATCH_SIZE" "$TIMESTAMP"
        wait_for_runs "${UPSTREAM_KCFG}" taskrun "$batch_num"
        kubectl --kubeconfig="${UPSTREAM_KCFG}" get taskruns -o json >"${PROJECT_ROOT}/performance-testing/${TIMESTAMP}/${BATCH_ID}-taskruns.json"
      else
        BATCH_ID="${BATCH_ID}-t"
        launch_batch "${FORK_KCFG}" "${PROJECT_ROOT}/performance-testing/benchmarking-task-test-run.yaml" "$BATCH_ID" "T" "$BATCH_SIZE" "$TIMESTAMP"
        wait_for_runs "${FORK_KCFG}" tasktestrun "$batch_num"
        kubectl --kubeconfig="${FORK_KCFG}" get tasktestruns -o json >"${PROJECT_ROOT}/performance-testing/${TIMESTAMP}/${BATCH_ID}-tasktestruns.json"
        batch_num=$((batch_num + 1))
      fi
    done
}


# ============================================================
# METRICS AGGREGATION
# ============================================================
execute_collecting_results() {
  kubectl --kubeconfig="${UPSTREAM_KCFG}" get taskruns -o json >"${PROJECT_ROOT}/performance-testing/${TIMESTAMP}/${TIMESTAMP}-upstream-taskruns-full.json"
  kubectl --kubeconfig="${FORK_KCFG}" get tasktestruns -o json >"${PROJECT_ROOT}/performance-testing/${TIMESTAMP}/${TIMESTAMP}-fork-tasktestruns-full.json"

  for f in upstream-taskruns fork-tasktestruns; do
  printf '"batchID","batchType","runName","startTime","durationSeconds"%s' "\n" > "${PROJECT_ROOT}/performance-testing/${TIMESTAMP}/${TIMESTAMP}-${f}-summary.csv"
    jq -r '
      .items[] |
      select(.metadata.labels."bench.ttf/batchType" != "W") |
      [
        (.metadata.labels."bench.ttf/batchId" // ""),
        (.metadata.labels."bench.ttf/batchType" // ""),
        (.metadata.name // ""),
        (.status.startTime // ""),
        ((.status.completionTime // "") as $ct | (.status.startTime // "") as $st |
        (if ($ct == "" or $st == "") then "" else ( ($ct | fromdate) - ($st | fromdate) ) end))
      ] | @csv
    ' "${PROJECT_ROOT}/performance-testing/${TIMESTAMP}/${TIMESTAMP}-${f}-full.json" >> "${PROJECT_ROOT}/performance-testing/${TIMESTAMP}/${TIMESTAMP}-${f}-summary.csv"
  done

  log "Results written to ${TIMESTAMP}-* files"
  # $CLEANUP_AFTER && log "Clusters deleted as requested"
}

if [[ $CLEANUP_ONLY == "YES" ]]; then
  log "Running cleanup-only mode..."
  cleanup
  exit 0
fi

qualifier="with"
if [[ $DESTRUCTIVE_MODE == "YES" ]]; then
  qualifier="in destructive mode with"
fi

log "$(printf 'Running experiment %s
- warmup batch size = %s
- batch size = %s
- number of batches = %s
- timestamp = %s
' "$qualifier" "$WARMUP_SIZE" "$BATCH_SIZE" "$NUM_BATCHES" "$TIMESTAMP"
)"


# ============================================================
# EXECUTE PHASES
# ============================================================
phase "Preflight Checks"                  execute_preflight_checks

if [[ $SKIP_SETUP == "YES" ]]; then
  log "Skipping Setup"
  kind get kubeconfig -n tekton-upstream > ${UPSTREAM_KCFG}
  kind get kubeconfig -n tekton-fork > ${FORK_KCFG}
else
  phase "Creating kind clusters"            execute_cluster_creation
  phase "Deploying Tekton and Dashboard"    execute_deployment_phase
  phase "Waiting for Tekton components"     execute_waiting_for_readyness_phase
fi

kubectl create namespace --kubeconfig="${UPSTREAM_KCFG}" ${TIMESTAMP}
kubectl create namespace --kubeconfig="${FORK_KCFG}" ${TIMESTAMP}

# trap only after successful cluster creation
trap 'read -p "Run cleanup before exit? [y/N] " resp; if [[ "$resp" =~ ^[Yy]$ ]]; then cleanup; fi' EXIT

phase "Starting dashboard port-forwards"  execute_start_port_forwards_phase
phase "Applying benchmark tasks"          execute_apply_resources_phase "default"
phase "Warmup"                            execute_warmup_phase
phase "Applying benchmark tasks"          execute_apply_resources_phase "${TIMESTAMP}"
phase "Experiment runs"                   execute_experiment_runs
phase "Collecting results"                execute_collecting_results
