#!/usr/bin/env bash

set -euo pipefail

binary=${ONBOARDD_BINARY:-"${HOME}/onboardd"}
default_config=${ONBOARDD_CONFIG:-/etc/onboardd/config.toml}
log_file=${ONBOARDD_LOG:-"${HOME}/onboardd-setup.log"}
pid_file=${ONBOARDD_PID_FILE:-"${HOME}/onboardd-setup.pid"}

timestamp() {
  date -u '+%Y-%m-%dT%H:%M:%SZ'
}

setup_pid() {
  if [[ -s "$pid_file" ]]; then
    tr -d '[:space:]' <"$pid_file"
  fi
}

setup_running() {
  local pid
  pid=$(setup_pid)
  [[ "$pid" =~ ^[0-9]+$ ]] &&
    ps -p "$pid" -o args= 2>/dev/null | grep -q '[o]nboardd setup'
}

require_binary() {
  if [[ ! -x "$binary" ]]; then
    printf 'onboardd binary is not executable: %s\n' "$binary" >&2
    exit 1
  fi
}

log_identity() {
  printf '[%s] binary SHA-256: ' "$(timestamp)" >>"$log_file"
  sha256sum "$binary" >>"$log_file"
}

log_command() {
  {
    printf '\n[%s] $ sudo ' "$(timestamp)"
    printf '%q ' "$binary" "$@"
    printf '\n'
  } >>"$log_file"
}

start() {
  require_binary
  if setup_running; then
    printf 'Setup is already running with PID %s.\n' "$(setup_pid)"
    exit 0
  fi

  local config=$default_config
  if (($# > 0)) && [[ $1 != -* ]]; then
    config=$1
    shift
  fi

  sudo -v
  if ! sudo test -r "$config"; then
    printf 'Configuration is not readable by root: %s\n' "$config" >&2
    exit 1
  fi

  touch "$log_file"
  chmod 0600 "$log_file"
  local first_new_line
  first_new_line=$(( $(wc -l <"$log_file") + 1 ))
  log_identity
  log_command setup --config "$config" "$@"

  nohup sudo -n -- "$binary" setup --config "$config" "$@" \
    >>"$log_file" 2>&1 </dev/null &
  local pid=$!
  printf '%s\n' "$pid" >"$pid_file"

  local attempt
  for attempt in {1..225}; do
    if tail -n +"$first_new_line" "$log_file" | grep -q ' setup is ready$'; then
      printf '[%s] setup ready with PID %s\n' "$(timestamp)" "$pid" >>"$log_file"
      printf 'Setup is ready with PID %s. Log: %s\n' "$pid" "$log_file"
      return 0
    fi
    if ! setup_running; then
      rm -f -- "$pid_file"
      printf '[%s] setup failed to start\n' "$(timestamp)" >>"$log_file"
      printf 'Setup failed to start. Recent output:\n' >&2
      tail -n 25 "$log_file" >&2
      return 1
    fi
    sleep 0.2
  done

  printf 'Setup process %s did not become ready within 45 seconds; inspect %s\n' \
    "$pid" "$log_file" >&2
  return 1
}

stop() {
  if ! setup_running; then
    printf 'Setup is not running.\n'
    rm -f -- "$pid_file"
    return 0
  fi

  local pid
  pid=$(setup_pid)
  sudo -v
  sudo kill -TERM "$pid"
  printf '[%s] graceful stop requested for PID %s\n' "$(timestamp)" "$pid" >>"$log_file"

  local attempt
  for attempt in {1..100}; do
    if ! setup_running; then
      rm -f -- "$pid_file"
      printf 'Setup stopped cleanly. Log retained at %s\n' "$log_file"
      return 0
    fi
    sleep 0.2
  done

  printf 'Setup process %s did not stop within 20 seconds; inspect %s\n' \
    "$pid" "$log_file" >&2
  printf 'No forced kill was sent, so cleanup can still complete.\n' >&2
  return 1
}

status() {
  if setup_running; then
    printf 'Setup is running with PID %s. Log: %s\n' "$(setup_pid)" "$log_file"
  else
    printf 'Setup is not running. Log: %s\n' "$log_file"
  fi
}

show() {
  if [[ ! -f "$log_file" ]]; then
    printf 'No setup log exists yet: %s\n' "$log_file" >&2
    exit 1
  fi
  tail -n 300 "$log_file"
}

follow() {
  touch "$log_file"
  chmod 0600 "$log_file"
  tail -n 50 -f "$log_file"
}

usage() {
  cat <<EOF
Usage:
  $0 start [CONFIG_FILE] [operational setup options]
  $0 stop
  $0 status
  $0 show
  $0 follow

Examples:
  $0 start
  $0 start /etc/onboardd/anthias.toml
  $0 start /etc/onboardd/inkypi.toml --listener-port 19000

Environment overrides:
  ONBOARDD_BINARY    onboardd binary (default: ~/onboardd)
  ONBOARDD_CONFIG    default TOML file (default: /etc/onboardd/config.toml)
  ONBOARDD_LOG       combined log file (default: ~/onboardd-setup.log)
  ONBOARDD_PID_FILE  PID file (default: ~/onboardd-setup.pid)
EOF
}

case ${1:-help} in
  start)
    shift
    start "$@"
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
