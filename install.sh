#!/bin/sh
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY="$SCRIPT_DIR/nx-tray"

INSTALL_DIR="$HOME/.local/bin"
AUTOSTART_DIR="$HOME/.config/autostart"

if [ ! -f "$BINARY" ]; then
    echo "Binary not found. Building..."
    cd "$SCRIPT_DIR"
    go build -o nx-tray .
fi

mkdir -p "$INSTALL_DIR" "$AUTOSTART_DIR"

cp "$BINARY" "$INSTALL_DIR/nx-tray"

cat > "$AUTOSTART_DIR/netextender-tray.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=NetExtender Tray
Comment=NetExtender VPN system tray manager
Exec=$INSTALL_DIR/nx-tray
Terminal=false
X-GNOME-Autostart-enabled=true
EOF

echo "Installed:"
echo "  $INSTALL_DIR/nx-tray"
echo "  $AUTOSTART_DIR/netextender-tray.desktop"
echo ""
echo "Run now:  $INSTALL_DIR/nx-tray"
