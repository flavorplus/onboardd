#!/usr/bin/env bash

set -euo pipefail

log_file=${ONBOARDD_DBUS_TRACE_LOG:-"${HOME}/onboardd-dbus-trace.log"}
pid_file=${ONBOARDD_DBUS_TRACE_PID:-"${HOME}/onboardd-dbus-trace.pid"}

timestamp() {
  date -u '+%Y-%m-%dT%H:%M:%SZ'
}

monitor_pid() {
  if [[ -s "$pid_file" ]]; then
    tr -d '[:space:]' <"$pid_file"
  fi
}

monitor_running() {
  local pid
  pid=$(monitor_pid)
  [[ "$pid" =~ ^[0-9]+$ ]] && ps -p "$pid" -o args= 2>/dev/null | grep -q '[d]bus-monitor.*--monitor'
}

start() {
  if monitor_running; then
    printf 'D-Bus trace recorder is already running with PID %s\n' "$(monitor_pid)"
    return
  fi
  for required_command in dbus-monitor stdbuf; do
    if ! command -v "$required_command" >/dev/null 2>&1; then
      printf '%s is not installed.\n' "$required_command" >&2
      exit 1
    fi
  done

  sudo -v
  touch "$log_file"
  printf '\n[%s] full D-Bus trace starting\n' "$(timestamp)" >>"$log_file"
  nohup sudo -n -- stdbuf -oL -eL dbus-monitor --system --monitor \
    "type='method_call',destination='org.freedesktop.NetworkManager',interface='org.freedesktop.NetworkManager.Settings.Connection'" \
    "type='method_return',sender='org.freedesktop.NetworkManager'" \
    "type='error',sender='org.freedesktop.NetworkManager'" \
    >>"$log_file" 2>&1 </dev/null &
  local pid=$!
  printf '%s\n' "$pid" >"$pid_file"

  sleep 0.2
  if ! kill -0 "$pid" 2>/dev/null; then
    local exit_status=1
    if wait "$pid"; then
      exit_status=0
    else
      exit_status=$?
    fi
    rm -f -- "$pid_file"
    printf '[%s] D-Bus trace failed to start (exit %s)\n' \
      "$(timestamp)" "$exit_status" >>"$log_file"
    tail -n 15 "$log_file" >&2
    return "$exit_status"
  fi

  printf '[%s] D-Bus trace recorder started with PID %s\n' \
    "$(timestamp)" "$pid" >>"$log_file"
  printf 'D-Bus trace recorder started. Log: %s\n' "$log_file"
}

stop() {
  if ! monitor_running; then
    printf 'D-Bus trace recorder is not running.\n'
    rm -f -- "$pid_file"
    return
  fi

  local pid
  pid=$(monitor_pid)
  sudo -v
  sudo kill "$pid"
  printf '[%s] D-Bus trace recorder stopped (PID %s)\n' \
    "$(timestamp)" "$pid" >>"$log_file"
  rm -f -- "$pid_file"
  printf 'D-Bus trace recorder stopped. Log retained at %s\n' "$log_file"
}

status() {
  if monitor_running; then
    printf 'D-Bus trace recorder is running with PID %s. Log: %s\n' \
      "$(monitor_pid)" "$log_file"
  else
    printf 'D-Bus trace recorder is not running. Log: %s\n' "$log_file"
  fi
}

show() {
  if [[ ! -f "$log_file" ]]; then
    printf 'No D-Bus trace exists yet: %s\n' "$log_file" >&2
    exit 1
  fi
  tail -n 500 "$log_file"
}

usage() {
  cat <<EOF
Usage:
  $0 start
  $0 stop
  $0 status
  $0 show

WARNING: this records complete NetworkManager Settings.Connection D-Bus message
bodies. The log can contain Wi-Fi passwords and must be handled as a secret.

Environment overrides:
  ONBOARDD_DBUS_TRACE_LOG  trace log (default: ~/onboardd-dbus-trace.log)
  ONBOARDD_DBUS_TRACE_PID  recorder PID file (default: ~/onboardd-dbus-trace.pid)
EOF
}

case ${1:-help} in
  start)
    start
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
  help|-h|--help)
    usage
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
