#!/usr/bin/env bash
# shellcheck disable=SC2016
set -euo pipefail

#
# Benchmark for the agent-permissions claude-hook.
#
# Measures processing time for realistic stress-test
# commands targeting specific features of the breakdown,
# rules, and permissions pipeline. The hook runs on every
# Bash tool call, so its latency is user-facing.
#
# Each case: warmup + timed runs, median reported.
#
# Usage: test/benchmark.sh
#
# Results:
#   2026-03-23  mean 4.01 ms  (as claude-code-permissions)
#   2026-08-04  mean 5.28 ms
#

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"

HOOK="$REPO_DIR/bin/agent-permissions"

if [[ ! -x "$HOOK" ]]; then
    echo "Hook binary not found at $HOOK" >&2
    echo "Run ./build.sh first." >&2
    exit 1
fi

# --- Setup ---

_bm_tmpdir=$(mktemp -d)
trap 'rm -rf "$_bm_tmpdir"' EXIT

WARMUP=2
RUNS=5
BATCH=100
MEDIAN_IDX=$(( (RUNS + 1) / 2 ))  # 3rd of 5

mkdir -p "$_bm_tmpdir/config" "$_bm_tmpdir/home" \
    "$_bm_tmpdir/project" "$_bm_tmpdir/inputs"
echo '{}' > "$_bm_tmpdir/config/settings.json"
export CLAUDE_CONFIG_DIR="$_bm_tmpdir/config"
# Policy comes from the embedded presets. Pin HOME and the
# preset-dir environment variables so the runner's real
# ~/.agents config or site policy can't skew the timings.
export HOME="$_bm_tmpdir/home"
export AGENT_PERMISSIONS_PRESET_DIRS=""
export AGENT_PERMISSIONS_ENFORCED_PRESET_DIRS=""

# Pre-generate hook input JSON for a command and write
# it to a temp file. Returns the file path. jq runs
# once per test case during setup, not during timing.
_prepare() {
    local name="$1"
    local cmd="$2"
    local path="$_bm_tmpdir/inputs/$name.json"
    jq -n --arg cmd "$cmd" \
        --arg cwd "$_bm_tmpdir/project" \
        '{"tool_name":"Bash","tool_input":{"command":$cmd},"cwd":$cwd}' \
        > "$path"
    echo "$path"
}

# --- Benchmark runner ---
#
# Each timed "run" executes the hook BATCH times in a
# tight loop between a single pair of date(1) calls,
# then divides to get per-invocation time in
# microseconds. This amortizes the ~1ms date overhead
# across BATCH iterations, giving sub-millisecond
# resolution. RUNS batches are collected and the
# median reported.

# Collected medians (microseconds) for average.
_all_medians=()

_report_times() {
    local label="$1"
    local collect="$2"
    shift 2
    local times=("$@")
    local median_us
    median_us=$(printf '%s\n' "${times[@]}" \
        | sort -n | sed -n "${MEDIAN_IDX}p")
    local ms_whole=$(( median_us / 1000 ))
    local ms_frac=$(( (median_us % 1000) / 10 ))
    printf "  %-44s %d.%02d ms\n" \
        "$label" "$ms_whole" "$ms_frac"
    if [[ "$collect" == "yes" ]]; then
        _all_medians+=("$median_us")
    fi
}

_bench() {
    local label="$1"
    local input_file="$2"
    local collect="${3:-yes}"
    local i j

    for ((i = 0; i < WARMUP; i++)); do
        "$HOOK" claude-hook < "$input_file" >/dev/null 2>&1 \
            || true
    done

    if ! "$HOOK" claude-hook < "$input_file" >/dev/null 2>&1; then
        printf "  %-44s FAIL (hook returned %d)\n" \
            "$label" "$?"
        return
    fi

    local times=()
    for ((i = 0; i < RUNS; i++)); do
        local start end
        start=$(date +%s%N)
        for ((j = 0; j < BATCH; j++)); do
            "$HOOK" claude-hook < "$input_file" \
                >/dev/null 2>&1 || true
        done
        end=$(date +%s%N)
        times+=("$(( (end - start) / 1000 / BATCH ))")
    done

    _report_times "$label" "$collect" "${times[@]}"
}

# Measure per-iteration overhead of the batch loop
# itself (bash for-loop + redirections, no hook).
_bench_overhead() {
    local i j
    for ((i = 0; i < WARMUP; i++)); do
        date +%s%N >/dev/null
    done

    local times=()
    for ((i = 0; i < RUNS; i++)); do
        local start end
        start=$(date +%s%N)
        for ((j = 0; j < BATCH; j++)); do
            true >/dev/null 2>&1 || true
        done
        end=$(date +%s%N)
        times+=("$(( (end - start) / 1000 / BATCH ))")
    done

    _report_times "measurement overhead (per iteration)" \
        no "${times[@]}"
}

# --- Generate script files for file-scanning test ---
#
# Creates a tree of bash scripts that invoke each other
# via `bash lib/foo.sh`, exercising the hook's file
# scanning path (read, parse, recurse). 8 files,
# ~270 lines, 3 levels of nesting.

_generate_scripts() {
    local dir="$_bm_tmpdir/project"
    mkdir -p "$dir/lib"

    cat > "$dir/deploy.sh" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
bash lib/logging.sh
bash lib/config.sh
bash lib/validate.sh
bash lib/build.sh
bash lib/deploy.sh
bash lib/healthcheck.sh
echo "Pipeline complete"
SCRIPT

    cat > "$dir/lib/logging.sh" <<'SCRIPT'
log_info() { echo "[INFO] $(date +%H:%M:%S) $1"; }
log_warn() { echo "[WARN] $(date +%H:%M:%S) $1"; }
log_error() { echo "[ERROR] $(date +%H:%M:%S) $1"; }

setup_logging() {
    local log_dir="/var/log/deploy"
    mkdir -p "$log_dir"
    touch "$log_dir/deploy.log"
    touch "$log_dir/error.log"
    chmod 644 "$log_dir/deploy.log"
    chmod 644 "$log_dir/error.log"
}

rotate_logs() {
    local log_dir="/var/log/deploy"
    for logfile in "$log_dir/deploy.log" "$log_dir/error.log"; do
        local size
        size=$(stat --format='%s' "$logfile")
        cp "$logfile" "${logfile}.bak"
        log_info "Backed up $logfile"
    done
}

setup_logging
rotate_logs
SCRIPT

    cat > "$dir/lib/config.sh" <<'SCRIPT'
parse_yaml() {
    local file="$1"
    local prefix="$2"
    grep -v '^#' "$file" | grep -v '^$' | while read -r line; do
        local key val
        key=$(echo "$line" | cut -d: -f1 | tr -d ' ')
        val=$(echo "$line" | cut -d: -f2- | sed 's/^ *//')
        echo "${prefix}_${key}=$val"
    done
}

load_environment() {
    local env_name="$1"
    local config_file="config/${env_name}.yml"
    if [[ -f "$config_file" ]]; then
        parse_yaml "$config_file" "APP"
        echo "Loaded config for $env_name"
    else
        echo "Config not found: $config_file" >&2
        return 1
    fi
}

merge_overrides() {
    local override_file="$1"
    if [[ -f "$override_file" ]]; then
        while read -r line; do
            case "$line" in
                \#*) echo "Skipping comment" ;;
                *) echo "Override: $line" ;;
            esac
        done < "$override_file"
    fi
}

load_environment "${DEPLOY_ENV:-staging}"
merge_overrides "config/overrides.env"
SCRIPT

    cat > "$dir/lib/validate.sh" <<'SCRIPT'
check_dependencies() {
    for cmd in docker kubectl jq curl git; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            echo "Missing: $cmd" >&2
            return 1
        fi
    done
    echo "All dependencies present"
}

validate_docker() {
    docker info >/dev/null 2>&1
    docker ps >/dev/null 2>&1
    echo "Docker is running"
}

validate_cluster() {
    kubectl cluster-info >/dev/null 2>&1
    kubectl get nodes >/dev/null 2>&1
    local count
    count=$(kubectl get nodes --no-headers | wc -l)
    echo "Cluster has $count nodes"
}

validate_registry() {
    local registry="$1"
    curl -sf "https://$registry/v2/" >/dev/null 2>&1
    echo "Registry $registry is reachable"
}

check_disk_space() {
    local required="$1"
    local available
    available=$(df -BG /tmp | tail -1 | awk '{print $4}')
    echo "Available disk: $available"
}

check_dependencies
validate_docker
validate_cluster
validate_registry "${REGISTRY:-registry.example.com}"
check_disk_space "10G"
SCRIPT

    cat > "$dir/lib/build.sh" <<'SCRIPT'
prepare_workspace() {
    mkdir -p build/artifacts build/logs
    cp -r src/ build/src/
    cp -r config/ build/config/
    echo "Workspace prepared"
}

run_linters() {
    echo "Running linters"
    flake8 src/ 2>&1 | tee build/logs/flake8.log
    mypy src/ 2>&1 | tee build/logs/mypy.log
    pylint src/ 2>&1 | tee build/logs/pylint.log
    echo "Linting complete"
}

run_tests() {
    echo "Running tests"
    pytest tests/ -v --tb=short 2>&1 \
        | tee build/logs/test.log
    echo "Tests complete"
}

build_image() {
    local tag="$1"
    echo "Building image: $tag"
    docker build -t "$tag" -f Dockerfile .
    docker tag "$tag" "${tag}-latest"
    echo "Image built: $tag"
}

push_image() {
    local tag="$1"
    local registry="${REGISTRY:-registry.example.com}"
    docker tag "$tag" "$registry/$tag"
    docker push "$registry/$tag"
    docker tag "${tag}-latest" "$registry/${tag}-latest"
    docker push "$registry/${tag}-latest"
    echo "Pushed: $registry/$tag"
}

prepare_workspace
run_linters
run_tests
build_image "app:${VERSION:-dev}"
push_image "app:${VERSION:-dev}"
SCRIPT

    cat > "$dir/lib/deploy.sh" <<'SCRIPT'
bash lib/deploy-api.sh
bash lib/deploy-worker.sh

apply_manifests() {
    local dir="$1"
    for manifest in "$dir"/*.yml; do
        kubectl apply -f "$manifest"
        echo "Applied: $manifest"
    done
}

wait_for_rollout() {
    local deployment="$1"
    local ns="${2:-default}"
    kubectl rollout status "deployment/$deployment" \
        -n "$ns" --timeout=300s
    echo "Rollout complete: $deployment"
}

scale_deployment() {
    local deployment="$1"
    local replicas="$2"
    kubectl scale "deployment/$deployment" \
        --replicas="$replicas"
    echo "Scaled $deployment to $replicas"
}

apply_manifests "k8s/base"
wait_for_rollout "api-server"
wait_for_rollout "worker"
scale_deployment "worker" "${WORKER_REPLICAS:-3}"
SCRIPT

    cat > "$dir/lib/deploy-api.sh" <<'SCRIPT'
configure_api() {
    kubectl create configmap api-config \
        --from-file=config/api.yml \
        --dry-run=client -o yaml \
        | kubectl apply -f -
    echo "API config applied"
}

deploy_api() {
    kubectl apply -f k8s/api/deployment.yml
    kubectl apply -f k8s/api/service.yml
    kubectl apply -f k8s/api/ingress.yml
    kubectl set image deployment/api-server \
        "api=app:${VERSION:-dev}"
    echo "API deployed"
}

verify_api() {
    kubectl get pods -l app=api-server
    kubectl logs -l app=api-server --tail=5
    echo "API verified"
}

configure_api
deploy_api
verify_api
SCRIPT

    cat > "$dir/lib/deploy-worker.sh" <<'SCRIPT'
configure_worker() {
    kubectl create configmap worker-config \
        --from-file=config/worker.yml \
        --dry-run=client -o yaml \
        | kubectl apply -f -
    echo "Worker config applied"
}

deploy_worker() {
    kubectl apply -f k8s/worker/deployment.yml
    kubectl apply -f k8s/worker/service.yml
    kubectl set image deployment/worker \
        "worker=app:${VERSION:-dev}"
    kubectl apply -f k8s/worker/hpa.yml
    echo "Worker deployed"
}

verify_worker() {
    kubectl get pods -l app=worker
    kubectl logs -l app=worker --tail=5
    echo "Worker verified"
}

configure_worker
deploy_worker
verify_worker
SCRIPT

    cat > "$dir/lib/healthcheck.sh" <<'SCRIPT'
check_endpoint() {
    local name="$1"
    local url="$2"
    local max_retries="${3:-30}"
    for attempt in $(seq 1 "$max_retries"); do
        if curl -sf "$url" >/dev/null 2>&1; then
            echo "Health OK: $name"
            return 0
        fi
        sleep 2
    done
    echo "Health FAIL: $name" >&2
    return 1
}

check_metrics() {
    local url="$1"
    local response
    response=$(curl -sf "$url")
    if echo "$response" | grep -q '"status":"healthy"'; then
        echo "Metrics healthy"
    else
        echo "Metrics unhealthy" >&2
        return 1
    fi
}

run_smoke_tests() {
    echo "Running smoke tests"
    curl -sf "http://localhost:8080/api/v1/ping" >/dev/null
    curl -sf "http://localhost:8080/api/v1/version" >/dev/null
    curl -sf "http://localhost:8080/api/v1/ready" >/dev/null
    curl -sf "http://localhost:8080/api/v1/status" >/dev/null
    echo "Smoke tests passed"
}

check_endpoint "api" "http://localhost:8080/health"
check_endpoint "worker" "http://localhost:8081/health"
check_metrics "http://localhost:9090/metrics"
run_smoke_tests
SCRIPT
}

_generate_scripts

# =============================================================
# Pre-generate all inputs
# =============================================================

f_baseline=$(_prepare baseline "echo")

# 1. Deep pipeline (20 pipe stages)
# Targets: BinaryCmd pipe handling (each pipe creates
# subshell scoping), many extracted commands (~21
# after xargs unwrap), permissions pattern matching
# for each extracted command.
read -r -d '' cmd <<'BENCH' || true
cat data.csv | grep -v '^#' | cut -d',' -f1,3,5 | sort -t',' -k2 | uniq -c | sort -rn | head -50 | awk '{print $2}' | tr ',' '\t' | column -t | sed 's/\t/  /g' | grep -i pattern | wc -l | tee /tmp/count.txt | xargs echo "Total:" | cat -n | tail -5 | rev | cut -c1-20 | tr '[:lower:]' '[:upper:]'
BENCH
f_pipeline=$(_prepare pipeline "$cmd")

# 2. Nested control flow
# Targets: IfClause/ForClause/CaseClause recursion,
# conditional depth tracking, CmdSubst extraction
# from for-loop iteration words, TestClause walking
# for command substitutions in [[ ]] conditions.
read -r -d '' cmd <<'BENCH' || true
if [[ -f config.yml ]]; then
  for env in dev staging prod; do
    if [[ "$env" != "prod" ]]; then
      for key in $(grep "$env" config.yml | cut -d= -f1); do
        case "$key" in
          db_*) echo "Database config: $key" ;;
          api_*) echo "API config: $key" ;;
          cache_*) echo "Cache config: $key" ;;
          queue_*) echo "Queue config: $key" ;;
          log_*) echo "Logging config: $key" ;;
          auth_*) echo "Auth config: $key" ;;
          *) echo "Other config: $key" ;;
        esac
      done
    elif [[ -f "prod-override.yml" ]]; then
      for key in $(cat "prod-override.yml" | cut -d= -f1); do
        if [[ "$key" == *_secret ]]; then
          echo "Secret: $key (redacted)"
        else
          echo "Override: $key"
        fi
      done
    else
      echo "No config for $env"
    fi
  done
else
  echo "config.yml not found"
fi
BENCH
f_nested=$(_prepare nested "$cmd")

# 3. Many find -exec clauses
# Targets: find breakdown hook (each -exec extracts
# an inner command), KeepOuter (find itself also
# checked by rules for -ok/-okdir), permission
# matching for all extracted + outer commands.
read -r -d '' cmd <<'BENCH' || true
find /project/src -type f -name '*.py' -exec grep -l 'import os' {} \; -exec grep -c 'def ' {} \; -exec wc -l {} \; -exec head -5 {} \; -exec md5sum {} \; -exec stat --format='%s %n' {} \; -exec file --mime-type {} \; -exec basename {} \;
BENCH
f_find=$(_prepare find "$cmd")

# 4. Stacked wrappers
# Targets: recursive breakdown through transparent
# wrapper layers (timeout -> stdbuf -> xargs each
# peel a layer and re-enter breakdown), chained
# with && so five independent wrapper stacks are
# each unwrapped.
read -r -d '' cmd <<'BENCH' || true
timeout 300 stdbuf -oL xargs -n1 grep -r 'TODO' /src && timeout 60 stdbuf -oL xargs -n1 grep -r 'FIXME' /src && timeout 30 stdbuf -oL xargs -n1 grep -r 'HACK' /src && timeout 10 stdbuf -oL xargs -n1 grep -r 'XXX' /src && timeout 120 stdbuf -oL xargs -n1 grep -r 'OPTIMIZE' /src
BENCH
f_wrappers=$(_prepare wrappers "$cmd")

# 5. Many git operations
# Targets: rules layer (flag parsing, subcommand
# matching, classifyGitBranch/classifyGitTag hooks)
# across many git invocations chained with &&.
read -r -d '' cmd <<'BENCH' || true
git fetch --all && git checkout main && git pull && git log --oneline -10 && git diff --stat HEAD~1 && git branch --list && git tag -l 'v*' && git stash list && git remote -v && git status --short && git rev-parse HEAD && git describe --tags --always && git log --format='%H %s' -3 && git diff --name-only HEAD~1 && git show --stat HEAD && git shortlog -sn --no-merges HEAD~20..HEAD
BENCH
f_git=$(_prepare git "$cmd")

# 6. Complex awk program
# Targets: hookCheckAwk text scanning across a large
# inline program. The hook scans every awk argument
# for system(), shell pipes, and backtick execution.
read -r -d '' cmd <<'BENCH' || true
awk 'BEGIN{FS="|"; OFS=","; errors=0; warns=0; total=0} /^[0-9]{4}-[0-9]{2}-[0-9]{2}/{split($1,d," "); date=d[1]; time=d[2]; level=$2; msg=$3; gsub(/^ +| +$/,"",level); gsub(/^ +| +$/,"",msg); total++; if(level=="ERROR"){errors++; err_by_date[date]++; last_err[date]=msg; err_times[date]=err_times[date] " " time} else if(level=="WARN"){warns++; warn_by_date[date]++} else if(level=="INFO"){info_by_date[date]++} else{other_by_date[date]++}} /Connection refused/{refused++; refused_by_date[date]++} /timeout|timed out/{timeouts++; timeout_by_date[date]++} /out of memory|OOM/{oom++} /disk full|no space/{diskfull++} END{print "=== Summary ==="; printf "Total: %d lines, %d errors, %d warnings\n",total,errors,warns; print ""; print "=== Errors by Date ==="; n=asorti(err_by_date,sorted); for(i=1;i<=n;i++){d=sorted[i]; printf "%s: %d errors, %d warns, %d info | Last: %s\n",d,err_by_date[d],warn_by_date[d]+0,info_by_date[d]+0,last_err[d]}}' /var/log/app/application.log /var/log/app/error.log /var/log/app/access.log | sort -t',' -k2 -rn | head -30
BENCH
f_awk=$(_prepare awk "$cmd")

# 7. Many command substitutions
# Targets: CmdSubst extraction from argument word
# parts — each $() spawns a sub-parse through
# extractSubsFromWord/extractSubsFromPart and its
# inner commands are extracted independently.
read -r -d '' cmd <<'BENCH' || true
curl -s -H "Authorization: Bearer $(cat ~/.config/token)" -H "X-Request-ID: $(uuidgen)" -H "X-Timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)" -H "X-Host: $(hostname -f)" -H "X-User: $(whoami)" -H "X-Commit: $(git rev-parse HEAD)" -H "X-Branch: $(git branch --show-current)" -H "X-Tag: $(git describe --tags --always)" -H "X-Uptime: $(uptime -s)" -d "{\"version\":\"$(cat VERSION)\",\"build\":\"$(date +%s)\",\"os\":\"$(uname -r)\"}" "https://api.example.com/v1/deploy"
BENCH
f_subs=$(_prepare subs "$cmd")

# 8. Large case statement
# Targets: CaseClause with many items — each item's
# patterns are walked for CmdSubst, each body is
# processed at conditional scope, many extracted
# commands from all branches.
read -r -d '' cmd <<'BENCH' || true
case "$1" in
  start) echo "Starting" && systemctl start app ;;
  stop) echo "Stopping" && systemctl stop app ;;
  restart) systemctl stop app && sleep 2 && systemctl start app ;;
  status) systemctl status app && journalctl -u app --no-pager -n 5 ;;
  logs) journalctl -u app --no-pager -n 100 ;;
  config) cat /etc/app/config.yml ;;
  check) python manage.py check && python manage.py validate ;;
  test) pytest tests/ -v --tb=short ;;
  lint) flake8 src/ && mypy src/ && pylint src/ ;;
  format) black src/ && isort src/ ;;
  build) docker build -t app:latest . ;;
  push) docker tag app:latest registry/app:latest && docker push registry/app:latest ;;
  deploy) kubectl apply -f k8s/ && kubectl rollout status deployment/app ;;
  rollback) kubectl rollout undo deployment/app ;;
  migrate) python manage.py migrate && python manage.py check ;;
  seed) python manage.py loaddata fixtures/seed.json ;;
  backup) pg_dump -Fc mydb > backup.dump && gzip backup.dump ;;
  restore) gunzip backup.dump.gz && pg_restore -d mydb backup.dump ;;
  health) curl -sf http://localhost:8080/health && echo "OK" ;;
  version) cat VERSION && git describe --tags --always ;;
  env) env | sort | grep -i app ;;
  clean) find /tmp -name 'app_*' -mtime +7 -exec rm -f {} \; ;;
  *) echo "Unknown: $1" ;;
esac
BENCH
f_case=$(_prepare case "$cmd")

# 9. Function definitions + calls
# Targets: FuncDecl tracking at unconditional scope,
# CouldBeFuncCall resolution in permissions, CmdSubst
# extraction from function bodies, conditional depth
# management for function body processing.
read -r -d '' cmd <<'BENCH' || true
log_msg() { echo "[$(date +%H:%M:%S)] $1: $2"; }
check_svc() { grep -q "$1" /tmp/status.txt; }
deploy_svc() {
  log_msg INFO "Deploying $1"
  cp "k8s/$1.yml" /tmp/deploy/
  kubectl apply -f "k8s/$1.yml"
  check_svc "$1"
  log_msg INFO "$1 deployed"
}
log_msg INFO "Starting deployment"
deploy_svc api
deploy_svc worker
deploy_svc scheduler
deploy_svc monitor
deploy_svc gateway
log_msg INFO "All services deployed"
BENCH
f_funcs=$(_prepare funcs "$cmd")

# 10. File scanning (8 scripts, ~270 lines)
# Targets: scanFile path — the hook reads, parses,
# and recursively extracts commands from real files
# on disk. deploy.sh invokes 6 scripts, one of
# which invokes 2 more (3 levels of nesting).
f_scripts=$(_prepare scripts "bash deploy.sh")

# =============================================================
# Run benchmarks
# =============================================================

echo "=== Hook Benchmark ==="
echo ""
echo "$WARMUP warmup, ${RUNS}x$BATCH batched runs, median reported"
echo ""

_bench_overhead
_bench "baseline (single command)" "$f_baseline"
_bench "deep pipeline (20 stages)" "$f_pipeline"
_bench "nested control flow" "$f_nested"
_bench "find with 8 -exec clauses" "$f_find"
_bench "stacked wrappers (5 chains)" "$f_wrappers"
_bench "many git operations (16 cmds)" "$f_git"
_bench "complex awk program" "$f_awk"
_bench "many cmd substitutions (12)" "$f_subs"
_bench "large case statement (23 branches)" "$f_case"
_bench "function defs + calls (9 calls)" "$f_funcs"
_bench "file scanning (8 scripts, ~270 lines)" \
    "$f_scripts"

echo ""
_sum=0
for _m in "${_all_medians[@]}"; do
    _sum=$(( _sum + _m ))
done
_avg=$(( _sum / ${#_all_medians[@]} ))
_avg_whole=$(( _avg / 1000 ))
_avg_frac=$(( (_avg % 1000) / 10 ))
printf "  %-44s %d.%02d ms\n" \
    "mean (all cases)" "$_avg_whole" "$_avg_frac"
