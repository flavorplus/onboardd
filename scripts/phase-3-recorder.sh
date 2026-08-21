#!/usr/bin/env bash

set -euo pipefail

binary=${ONBOARDD_BINARY:-"${HOME}/onboardd"}
interface_name=${ONBOARDD_INTERFACE:-wlan0}
log_file=${ONBOARDD_PHASE3_LOG:-"${HOME}/onboardd-phase3.log"}
pid_file=${ONBOARDD_PHASE3_PID:-"${HOME}/onboardd-phase3.pid"}

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

captive_pid() {
  if [[ -s "$pid_file" ]]; then
    tr -d '[:space:]' <"$pid_file"
  fi
}

captive_running() {
  local pid
  pid=$(captive_pid)
  [[ "$pid" =~ ^[0-9]+$ ]] && ps -p "$pid" -o args= 2>/dev/null | grep -q '[o]nboardd debug captive-start'
}

require_binary() {
  if [[ ! -x "$binary" ]]; then
    printf 'onboardd binary is not executable: %s\n' "$binary" >&2
    exit 1
  fi
}

start() {
  if (($# == 0)); then
    printf 'Usage: %s start --ssid NAME --password-file FILE --yes [options]\n' "$0" >&2
    exit 2
  fi
  require_binary
  if captive_running; then
    printf 'Captive provisioning is already running with PID %s\n' "$(captive_pid)"
    exit 0
  fi

  sudo -v
  touch "$log_file"
  local first_new_line
  first_new_line=$(( $(wc -l <"$log_file") + 1 ))
  log_binary_identity
  log_command debug captive-start --interface "$interface_name" "$@"
  nohup sudo -n -- "$binary" debug captive-start \
    --interface "$interface_name" "$@" >>"$log_file" 2>&1 </dev/null &
  local pid=$!
  printf '%s\n' "$pid" >"$pid_file"

  local attempt
  for attempt in {1..225}; do
    if tail -n +"$first_new_line" "$log_file" | grep -q '^captive provisioning is ready$'; then
      printf '[%s] captive portal ready with PID %s\n' "$(timestamp)" "$pid" >>"$log_file"
      printf 'Captive provisioning is ready. Log: %s\n' "$log_file"
      return 0
    fi
    if ! captive_running; then
      rm -f -- "$pid_file"
      printf '[%s] captive provisioning failed to start\n' "$(timestamp)" >>"$log_file"
      printf 'Captive provisioning failed to start. Recent output:\n' >&2
      tail -n 20 "$log_file" >&2
      return 1
    fi
    sleep 0.2
  done

  printf 'Captive process %s did not become ready within 45 seconds; inspect %s\n' \
    "$pid" "$log_file" >&2
  return 1
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
  if ! captive_running; then
    printf 'Captive provisioning is not running.\n'
    rm -f -- "$pid_file"
    exit 0
  fi
  local pid
  pid=$(captive_pid)
  sudo -v
  sudo kill "$pid"
  printf '[%s] captive stop requested for PID %s\n' "$(timestamp)" "$pid" >>"$log_file"

  local attempt
  for attempt in {1..75}; do
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f -- "$pid_file"
      printf 'Captive provisioning stopped. Log retained at %s\n' "$log_file"
      return 0
    fi
    sleep 0.2
  done
  printf 'Captive process %s did not stop within 15 seconds; inspect %s\n' "$pid" "$log_file" >&2
  return 1
}

status() {
  if captive_running; then
    printf 'Captive provisioning is running with PID %s. Log: %s\n' "$(captive_pid)" "$log_file"
  else
    printf 'Captive provisioning is not running. Log: %s\n' "$log_file"
  fi
}

show() {
  if [[ ! -f "$log_file" ]]; then
    printf 'No log exists yet: %s\n' "$log_file" >&2
    exit 1
  fi
  tail -n 250 "$log_file"
}

follow() {
  touch "$log_file"
  tail -n 50 -f "$log_file"
}

usage() {
  cat <<EOF
Usage:
  $0 start --ssid NAME --password-file FILE --yes [captive-start options]
  $0 run <onboardd arguments...>
  $0 stop
  $0 status
  $0 show
  $0 follow

Environment overrides:
  ONBOARDD_BINARY       onboardd binary (default: ~/onboardd)
  ONBOARDD_INTERFACE    Wi-Fi interface (default: wlan0)
  ONBOARDD_PHASE3_LOG   combined log file (default: ~/onboardd-phase3.log)
  ONBOARDD_PHASE3_PID   captive process PID file (default: ~/onboardd-phase3.pid)
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
