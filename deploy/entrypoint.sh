#!/bin/bash
# Runs OpenResty and openresty-agent side by side in one container.
#
# OpenResty starts first; the agent starts once the nginx master has
# written its PID file, so commands queued while the node was down are not
# applied (and reloaded) before OpenResty is running. If either process
# exits, the other is stopped and the container exits with that status, so
# the orchestrator's restart policy takes over.
set -u

pidfile=/usr/local/openresty/nginx/logs/nginx.pid

openresty -g 'daemon off;' &
openresty_pid=$!

for _ in $(seq 1 100); do
  [ -s "$pidfile" ] && break
  if ! kill -0 "$openresty_pid" 2>/dev/null; then
    wait "$openresty_pid"
    exit $?
  fi
  sleep 0.1
done

openresty-agent &
agent_pid=$!

stop_all() {
  kill -QUIT "$openresty_pid" 2>/dev/null # graceful nginx shutdown
  kill -TERM "$agent_pid" 2>/dev/null
}
requested=0
trap 'requested=1; stop_all' TERM INT QUIT

wait -n "$openresty_pid" "$agent_pid"
status=$?
stop_all
wait
# A stop we were asked for is a clean exit; otherwise report the crash.
[ "$requested" = 1 ] && exit 0
exit "$status"
