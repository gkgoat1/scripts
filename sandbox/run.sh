#!/bin/bash
set -e

OS=$(uname -s)
DAEMON_BIN="./sandbox/daemon/daemon"
LINUX_BIN="./sandbox/linux/sandbox"
MAC_BIN="./sandbox/macos/sandbox_wrapper"

# Compilation
echo "Compiling components..."
gcc -o $DAEMON_BIN sandbox/daemon/daemon.c
if [ "$OS" == "Linux" ]; then
    gcc -o $LINUX_BIN sandbox/linux/sandbox.c
elif [ "$OS" == "Darwin" ]; then
    # Compile dylib
    gcc -dynamiclib -o sandbox/macos/sandbox.dylib sandbox/macos/sandbox.dylib.c
    # The wrapper would normally use a library like 'mach-o' or 'llde' to rewrite the binary.
    # For this demo, we implement a simpler wrapper that uses DYLD_INSERT_LIBRARIES.
    # In a full implementation, this script would:
    # 1. Check ~/.cache/sandbox/binary_hash
    # 2. If not present, use 'install_name_tool' and 'codesign'
    # 3. Execute the cached binary.
    
    # Mocking the binary rewriting wrapper for the demo
    cat <<EOF > $MAC_BIN
#!/bin/bash
# Mocked binary rewriter
TARGET=\$1
shift
echo "[macOS Sandbox] Rewriting \$TARGET..."
CACHE_DIR="~/.cache/sandbox"
# Actual logic:
# cp \$TARGET \$CACHE_DIR/\$(echo \$TARGET | md5sum)
# install_name_tool -add_rpath /path/to/dylib ...
# codesign --force --sign - \$CACHE_DIR/...
export DYLD_INSERT_LIBRARIES="sandbox/macos/sandbox.dylib"
exec \$TARGET "\$@"
EOF
    chmod +x $MAC_BIN
fi

# Start daemon in background if not running
if ! pgrep -x "daemon" > /dev/null; then
    $DAEMON_BIN &
    sleep 1
fi

# Execution
if [ "$OS" == "Linux" ]; then
    $LINUX_BIN "$@"
elif [ "$OS" == "Darwin" ]; then
    $MAC_BIN "$@"
else
    echo "Unsupported OS"
    exit 1
fi