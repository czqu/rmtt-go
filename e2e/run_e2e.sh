#!/usr/bin/env bash
#
# Cross-language E2E harness for the rmtt-go language matrix.
#
# Usage: run_e2e.sh <server> <client>
#   server: go | java
#   client: go | java | rust
#
# Starts the requested server, waits for its READY line, runs the requested
# client against it and verifies the exit code plus the PASS marker.
#
# Build artifacts (produced by the CI / remote build step) are expected at:
#   e2e/bin/go-server                (go server, port arg)
#   e2e/bin/go-client                (go client, port arg)
#   e2e/java-server/target/rmtt-e2e-java-server.jar   (java server, port arg)
#   e2e/java-client/target/rmtt-e2e-java-client.jar   (java client, host port)
#   e2e/rust-client/target/release/rmtt-rust-e2e      (rust client, port arg)
set -u

E2E=$(cd "$(dirname "$0")" && pwd)
BIN="$E2E/bin"
JAVA_SERVER_JAR="$E2E/java-server/target/rmtt-e2e-java-server.jar"
JAVA_CLIENT_JAR="$E2E/java-client/target/rmtt-e2e-java-client.jar"
RUST_CLIENT="$E2E/rust-client/target/release/rmtt-rust-e2e"

SERVER=$1
CLIENT=$2
PORT=${PORT:-19990}
LOG=/tmp/rmtt-e2e-server.log

rm -f "$LOG"

fail() {
  echo "E2E FAILED: $1" >&2
  if [ -f "$LOG" ]; then
    echo "---- server log ----" >&2
    tail -50 "$LOG" >&2
  fi
  exit 1
}

wait_ready() { # marker
  local marker=$1
  for _ in $(seq 1 100); do
    if grep -q "$marker" "$LOG" 2>/dev/null; then
      return 0
    fi
    if ! kill -0 "$SRV_PID" 2>/dev/null; then
      fail "server exited before printing $marker"
    fi
    sleep 0.2
  done
  fail "timed out waiting for $marker"
}

echo "==> starting ${SERVER} server on tcp port ${PORT}"
case "$SERVER" in
  go)
    "$BIN/go-server" "$PORT" >"$LOG" 2>&1 &
    SRV_PID=$!
    wait_ready GO_SERVER_READY
    ;;
  java)
    java -jar "$JAVA_SERVER_JAR" "$PORT" >"$LOG" 2>&1 &
    SRV_PID=$!
    wait_ready E2E_SERVER_READY
    ;;
  *)
    fail "unknown server: $SERVER"
    ;;
esac
echo "==> ${SERVER} server ready (pid $SRV_PID)"

echo "==> running ${CLIENT} client against ${SERVER} server"
case "$CLIENT" in
  go)
    out=$("$BIN/go-client" "$PORT" 2>&1)
    rc=$?
    marker="GO_E2E_PASS"
    ;;
  java)
    out=$(java -jar "$JAVA_CLIENT_JAR" 127.0.0.1 "$PORT" 2>&1)
    rc=$?
    marker="JAVA_CLIENT_E2E_PASS"
    ;;
  rust)
    out=$("$RUST_CLIENT" "$PORT" 2>&1)
    rc=$?
    marker="RUST_CLIENT_E2E_PASS"
    ;;
  *)
    fail "unknown client: $CLIENT"
    ;;
esac

kill "$SRV_PID" 2>/dev/null
wait "$SRV_PID" 2>/dev/null

echo "$out"
if [ "$rc" -ne 0 ]; then
  fail "client exited with $rc"
fi
if ! echo "$out" | grep -q "$marker"; then
  fail "client output missing $marker"
fi

echo "E2E PASS: ${SERVER} server <-> ${CLIENT} client"
