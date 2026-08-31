#!/usr/bin/env sh
# mem-x dependency gate (AGENTS.md §5):
#   - runtime (non-test) code must be stdlib-only
#   - test/tooling deps must come from the allowlist
set -eu
cd "$(dirname "$0")/.."

echo "==> runtime deps (excluding test files) must be stdlib-only"
# go list -deps -test=false lists only runtime dependencies. Third-party paths
# carry a dot (e.g. github.com/...); the Go distribution's own vendored
# packages start with vendor/ and are part of the stdlib.
runtime_deps=$(go list -deps -test=false ./... 2>/dev/null | grep -E '\.' | grep -v '^vendor/' || true)
if [ -n "$runtime_deps" ]; then
    echo "FAIL: runtime depends on non-stdlib packages:"
    echo "$runtime_deps"
    exit 1
fi
echo "runtime stdlib-only: OK"

echo "==> declared dependencies (direct only) must be in the allowlist"
allowlist="github.com/redis/go-redis/v9 github.com/stretchr/testify"
bad=""
for m in $(grep -E '^[[:space:]]*[a-zA-Z0-9_./-]+[[:space:]]+v[0-9]' go.mod | grep -v '// indirect' | awk '{print $1}' | sort -u); do
    case " $allowlist " in
        *" $m "*) ;;
        *) bad="$bad $m" ;;
    esac
done
if [ -n "$bad" ]; then
    echo "FAIL: dependencies outside the allowlist:$bad"
    exit 1
fi
echo "allowlist: OK"