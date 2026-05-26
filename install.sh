#!/bin/sh
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ICON_SRC="$SCRIPT_DIR/nx-icon.png"
SCRIPT_SRC="$SCRIPT_DIR/nx-tray.py"

INSTALL_DIR="$HOME/.local/bin"
ICON_DIR="$HOME/.local/share/icons"
AUTOSTART_DIR="$HOME/.config/autostart"

mkdir -p "$INSTALL_DIR" "$ICON_DIR" "$AUTOSTART_DIR"

cp "$ICON_SRC" "$ICON_DIR/netextender.png"

sed \
    -e "s|^ICON = .*|ICON = os.path.expanduser(\"~/.local/share/icons/netextender.png\")|" \
    "$SCRIPT_SRC" > "$INSTALL_DIR/nx-tray.py"
chmod +x "$INSTALL_DIR/nx-tray.py"

UV_PATH="$(command -v uv 2>/dev/null || echo "$HOME/.local/bin/uv")"

cat > "$AUTOSTART_DIR/netextender-tray.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=NetExtender Tray
Comment=NetExtender VPN system tray manager
Exec=$UV_PATH run --script $INSTALL_DIR/nx-tray.py
Icon=$ICON_DIR/netextender.png
Terminal=false
X-GNOME-Autostart-enabled=true
EOF

echo "Installed:"
echo "  $INSTALL_DIR/nx-tray.py"
echo "  $ICON_DIR/netextender.png"
echo "  $AUTOSTART_DIR/netextender-tray.desktop"
echo ""
echo "Run now:  $INSTALL_DIR/nx-tray.py"
