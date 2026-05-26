# nx-tray

System tray application for managing [SonicWall NetExtender](https://www.sonicwall.com/products/remote-access/vpn-clients/) VPN connections on Linux.

Wraps the `nxcli` command-line tool in a tray icon with menus for connecting, disconnecting, and managing saved connections.

![Ubuntu GNOME](https://img.shields.io/badge/Ubuntu-GNOME-E95420?logo=ubuntu)
![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)

## Features

- Connect/disconnect to saved VPN connections
- View connection status (server, protocol)
- Add, edit, and delete saved connections
- Password prompt via zenity
- Single static binary (~7MB) with embedded icon

## Requirements

**Runtime:**
- NetExtender with `nxcli` installed (default: `/usr/sbin/nxcli`)
- `zenity` (pre-installed on Ubuntu GNOME)
- GNOME with the AppIndicator extension (default on Ubuntu)

**Build:**
- Go 1.22+
- `libayatana-appindicator3-dev`
- `libgtk-3-dev`

## Install

### From release

```bash
wget https://github.com/fbinz/nx-tray/releases/latest/download/nx-tray
chmod +x nx-tray
./nx-tray install
```

This copies the binary to `~/.local/bin/`, extracts the icon to `~/.local/share/icons/`, and creates an autostart entry so it launches on login.

### From source

```bash
sudo apt install golang-go libayatana-appindicator3-dev libgtk-3-dev

git clone https://github.com/fbinz/nx-tray.git
cd nx-tray
make
./nx-tray install
```

### Uninstall

```bash
nx-tray uninstall
```

Removes the binary, icon, and autostart entry.

## Usage

```
nx-tray [command] [flags]

Commands:
  install       install to ~/.local/bin and set up autostart
  uninstall     remove installed files

Flags:
  -nxcli string   path to nxcli binary (default "/usr/sbin/nxcli", env: NXCLI)
  -v              enable info logging
  -vv             enable debug logging (includes nxcli command output)
```

The tray icon appears in the top panel. Click it to open the menu:

- **Status** — shows current connection state
- **Connect** — pick a saved connection, enter password
- **Disconnect** — disconnect the active VPN
- **Connections** — add, edit, or delete saved connections
- **Quit** — exit the tray app

## License

MIT
