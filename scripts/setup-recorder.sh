#!/usr/bin/env bash

set -euo pipefail

binary=${ONBOARDD_BINARY:-"${HOME}/onboardd"}
default_config=${ONBOARDD_CONFIG:-/etc/onboardd/config.toml}
log_file=${ONBOARDD_LOG:-"${HOME}/onboardd-setup.log"}
pid_file=${ONBOARDD_PID_FILE:-"${HOME}/onboardd-setup.pid"}
health_url=${ONBOARDD_HEALTH_URL:-http://127.0.0.1:18080/healthz}
network_interface=${ONBOARDD_INTERFACE:-wlan0}
dns_config_file=/etc/NetworkManager/dnsmasq-shared.d/onboardd.conf

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
    ps -p "$pid" -o args= 2>/dev/null | grep -Eq '[o]nboardd (setup|run)'
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

capture_command() {
  {
    printf '[%s] $ ' "$(timestamp)"
    printf '%q ' "$@"
    printf '\n'
    local exit_status=0
    "$@" || exit_status=$?
    if ((exit_status != 0)); then
      printf '[exit %s]\n' "$exit_status"
    fi
  } >>"$log_file" 2>&1
}

start() {
  local mode=$1
  shift
  require_binary
  if setup_running; then
    printf 'Onboardd is already running with PID %s.\n' "$(setup_pid)"
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
  log_command "$mode" --config "$config" "$@"

  nohup sudo -n -- "$binary" "$mode" --config "$config" "$@" \
    >>"$log_file" 2>&1 </dev/null &
  local pid=$!
  printf '%s\n' "$pid" >"$pid_file"

  local attempt
  for attempt in {1..225}; do
    if tail -n +"$first_new_line" "$log_file" | grep -Eq ' setup is (ready|running)$'; then
      printf '[%s] %s ready with PID %s\n' "$(timestamp)" "$mode" "$pid" >>"$log_file"
      printf 'Onboardd %s is ready with PID %s. Log: %s\n' "$mode" "$pid" "$log_file"
      return 0
    fi
    if ! setup_running; then
      rm -f -- "$pid_file"
      printf '[%s] %s failed to start\n' "$(timestamp)" "$mode" >>"$log_file"
      printf 'Onboardd %s failed to start. Recent output:\n' "$mode" >&2
      tail -n 25 "$log_file" >&2
      return 1
    fi
    sleep 0.2
  done

  printf 'Onboardd %s process %s did not become ready within 45 seconds; inspect %s\n' \
    "$mode" "$pid" "$log_file" >&2
  return 1
}

stop() {
  if ! setup_running; then
    printf 'Onboardd is not running.\n'
    rm -f -- "$pid_file"
    return 0
  fi

  local pid
  pid=$(setup_pid)
  sudo -v
  sudo kill -TERM "$pid"
  printf '[%s] graceful stop requested for PID %s\n' "$(timestamp)" "$pid" >>"$log_file"

  local attempt
  for attempt in {1..375}; do
    if ! setup_running; then
      rm -f -- "$pid_file"
      printf 'Onboardd stopped cleanly. Log retained at %s\n' "$log_file"
      return 0
    fi
    sleep 0.2
  done

  printf 'Onboardd process %s did not stop within 75 seconds; inspect %s\n' \
    "$pid" "$log_file" >&2
  printf 'No forced kill was sent, so cleanup can still complete.\n' >&2
  return 1
}

snapshot() {
  if (($# > 1)); then
    usage >&2
    exit 2
  fi
  local label=${1:-snapshot}
  if [[ ! $label =~ ^[A-Za-z0-9_.-]{1,64}$ ]]; then
    printf 'Snapshot label must use 1-64 letters, digits, dots, underscores, or dashes.\n' >&2
    exit 2
  fi
  require_binary
  sudo -v
  touch "$log_file"
  chmod 0600 "$log_file"

  printf '\n[%s] evidence snapshot: %s\n' "$(timestamp)" "$label" >>"$log_file"
  log_identity
  capture_command cat /proc/sys/kernel/random/boot_id
  capture_command uptime -s
  capture_command systemctl is-active NetworkManager avahi-daemon
  if setup_running; then
    capture_command ps -p "$(setup_pid)" -o pid=,etimes=,stat=,args=
  else
    printf '[%s] onboardd process: not running\n' "$(timestamp)" >>"$log_file"
  fi
  capture_command curl --fail --silent --show-error --max-time 5 "$health_url"
  capture_command sudo -n -- stat -c '%a %U:%G %n' /run/onboardd/control.sock
  capture_command sudo -n -- "$binary" debug status --interface "$network_interface"
  capture_command sudo -n -- "$binary" debug profiles
  capture_command ip -brief address show dev "$network_interface"
  capture_command sudo -n -- nft list table inet onboardd_captive
  capture_command sudo -n -- stat -c '%a %U:%G %n' "$dns_config_file"
  sync
  printf 'Evidence snapshot %q appended to %s\n' "$label" "$log_file"
}

status() {
  if setup_running; then
    printf 'Onboardd is running with PID %s. Log: %s\n' "$(setup_pid)" "$log_file"
  else
    printf 'Onboardd is not running. Log: %s\n' "$log_file"
  fi
}

recover() {
  require_binary
  sudo -v
  touch "$log_file"
  chmod 0600 "$log_file"
  log_identity
  log_command recover
  sudo -n -- "$binary" recover | tee -a "$log_file"
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
  $0 run [CONFIG_FILE] [operational run options]
  $0 recover
  $0 stop
  $0 status
  $0 snapshot [LABEL]
  $0 show
  $0 follow

Examples:
  $0 start
  $0 run /etc/onboardd/config.toml
  $0 recover
  $0 snapshot before-reboot
  $0 start /etc/onboardd/anthias.toml
  $0 start /etc/onboardd/inkypi.toml --listener-port 19000

Environment overrides:
  ONBOARDD_BINARY    onboardd binary (default: ~/onboardd)
  ONBOARDD_CONFIG    default TOML file (default: /etc/onboardd/config.toml)
  ONBOARDD_LOG       combined log file (default: ~/onboardd-setup.log)
  ONBOARDD_PID_FILE  PID file (default: ~/onboardd-setup.pid)
  ONBOARDD_HEALTH_URL health endpoint (default: http://127.0.0.1:18080/healthz)
  ONBOARDD_INTERFACE NetworkManager interface used by snapshots (default: wlan0)
EOF
}

case ${1:-help} in
  start)
    shift
    start setup "$@"
    ;;
  run)
    shift
    start run "$@"
    ;;
  recover)
    shift
    if (($# != 0)); then
      usage >&2
      exit 2
    fi
    recover
    ;;
  stop)
    stop
    ;;
  status)
    status
    ;;
  snapshot)
    shift
    snapshot "$@"
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
