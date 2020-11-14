#!/bin/bash
MIN_SECS=${REPOS_TOOL_PARAM_min:-0}
MAX_SECS=${REPOS_TOOL_PARAM_max:-$((MIN_SECS+10))}

echo "C"

test -z "$REPOS_TOOL_PARAM_fail" || {
    echo "requested to fail" >&2
    exit 1
}

R=$RANDOM
dur=$((MAX_SECS - MIN_SECS))
secs=$((dur*10/32767))
msecs=$((dur*10000/32767))
sleep $((secs+MIN_SECS)).${msecs}s
