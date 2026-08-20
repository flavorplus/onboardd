#!/usr/bin/env bash

set -euo pipefail

binary=${ONBOARDD_BINARY:-"${HOME}/onboardd"}
interface_name=${ONBOARDD_INTERFACE:-wlan0}
log_file=${ONBOARDD_PHASE2_LOG:-"${HOME}/onboardd-phase2.log"}
pid_file=${ONBOARDD_PHASE2_PID:-"${HOME}/onboardd-phase2.pid"}

timestamp() {
  date -u '+%Y-%m-%dT%H:%M:%SZ'
}

log_command() {
  {
    printf '\n[%s] $ sudo ' "$(timestamp)"
    printf '%q ' "$binary" "$@"
    printf '\n'
  } >>"$log_file"
}

log_binary_identity() {
  printf '[%s] binary SHA-256: ' "$(timestamp)" >>"$log_file"
  sha256sum "$binary" >>"$log_file"
}

watcher_pid() {
  if [[ -s "$pid_file" ]]; then
    tr -d '[:space:]' <"$pid_file"
  fi
}

watcher_running() {
  local pid
  pid=$(watcher_pid)
  [[ "$pid" =~ ^[0-9]+$ ]] && ps -p "$pid" -o args= 2>/dev/null | grep -q '[o]nboardd debug reconcile'
}

require_binary() {
  if [[ ! -x "$binary" ]]; then
    printf 'onboardd binary is not executable: %s\n' "$binary" >&2
    exit 1
  fi
}

start() {
  local requirement=${1:-local}
  local grace_period=${2:-30s}
  require_binary
  if watcher_running; then
    printf 'Recorder is already running with PID %s\n' "$(watcher_pid)"
    exit 0
  fi

  sudo -v
  touch "$log_file"
  log_binary_identity
  log_command debug reconcile \
    --interface "$interface_name" \
    --requirement "$requirement" \
    --grace-period "$grace_period" \
    --watch
  nohup sudo -n -- "$binary" debug reconcile \
    --interface "$interface_name" \
    --requirement "$requirement" \
    --grace-period "$grace_period" \
    --watch >>"$log_file" 2>&1 </dev/null &
  local pid=$!
  printf '%s\n' "$pid" >"$pid_file"

  # Catch invalid arguments and other immediate startup failures before telling
  # the operator that a background recorder is running.
  sleep 0.2
  if ! kill -0 "$pid" 2>/dev/null; then
    local exit_status=1
    if wait "$pid"; then
      exit_status=0
    else
      exit_status=$?
    fi
    rm -f -- "$pid_file"
    printf '[%s] recorder failed to start (exit %s)\n' \
      "$(timestamp)" "$exit_status" >>"$log_file"
    printf 'Recorder failed to start. Recent output:\n' >&2
    tail -n 15 "$log_file" >&2
    return "$exit_status"
  fi

  printf '[%s] recorder started with PID %s\n' "$(timestamp)" "$pid" >>"$log_file"
  printf 'Recorder started. Log: %s\n' "$log_file"
}

run_command() {
  if (($# == 0)); then
    printf 'Usage: %s run <onboardd arguments...>\n' "$0" >&2
    exit 2
  fi
  require_binary
  sudo -v
  touch "$log_file"
  log_binary_identity
  log_command "$@"
  nohup sudo -n -- "$binary" "$@" >>"$log_file" 2>&1 </dev/null &
  local pid=$!
  printf '[%s] command launched with PID %s\n' "$(timestamp)" "$pid" >>"$log_file"
  printf 'Command launched with PID %s; output will be appended to %s\n' "$pid" "$log_file"
}

stop() {
  if ! watcher_running; then
    printf 'Recorder is not running.\n'
    rm -f -- "$pid_file"
    exit 0
  fi
  local pid
  pid=$(watcher_pid)
  sudo -v
  sudo kill "$pid"
  printf '[%s] recorder stop requested for PID %s\n' "$(timestamp)" "$pid" >>"$log_file"
  rm -f -- "$pid_file"
  printf 'Recorder stopped. Log retained at %s\n' "$log_file"
}

status() {
  if watcher_running; then
    printf 'Recorder is running with PID %s. Log: %s\n' "$(watcher_pid)" "$log_file"
  else
    printf 'Recorder is not running. Log: %s\n' "$log_file"
  fi
}

show() {
  if [[ ! -f "$log_file" ]]; then
    printf 'No log exists yet: %s\n' "$log_file" >&2
    exit 1
  fi
  tail -n 200 "$log_file"
}

follow() {
  touch "$log_file"
  tail -n 50 -f "$log_file"
}

usage() {
  cat <<EOF
Usage:
  $0 start [local|internet] [grace-period]
  $0 run <onboardd arguments...>
  $0 stop
  $0 status
  $0 show
  $0 follow

Environment overrides:
  ONBOARDD_BINARY       onboardd binary (default: ~/onboardd)
  ONBOARDD_INTERFACE    Wi-Fi interface (default: wlan0)
  ONBOARDD_PHASE2_LOG   combined log file (default: ~/onboardd-phase2.log)
  ONBOARDD_PHASE2_PID   recorder PID file (default: ~/onboardd-phase2.pid)
EOF
}

case ${1:-help} in
  start)
    shift
    start "$@"
    ;;
  run)
    shift
    run_command "$@"
    ;;
  stop)
    stop
    ;;
  status)
    status
    ;;
  show)
    show
    ;;
  follow)
    follow
    ;;
  help|-h|--help)
    usage
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
