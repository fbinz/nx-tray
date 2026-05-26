#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = [
#     "PySide6",
# ]
#
# [tool.uv]
# exclude-newer = "2026-05-26"
# ///

import argparse
import json
import logging
import os
import pty
import select
import subprocess
import sys
import threading

from PySide6.QtGui import QIcon
from PySide6.QtCore import QTimer
from PySide6.QtWidgets import (
    QApplication,
    QDialog,
    QDialogButtonBox,
    QFormLayout,
    QLabel,
    QLineEdit,
    QComboBox,
    QCheckBox,
    QMenu,
    QMessageBox,
    QSystemTrayIcon,
    QVBoxLayout,
)

log = logging.getLogger("nx-tray")

NXCLI = os.environ.get("NXCLI", "/usr/sbin/nxcli")
ICON = os.path.join(os.path.dirname(os.path.abspath(__file__)), "nx-icon.png")


def run_nxcli(*args, timeout=30):
    """Run nxcli with a pseudo-terminal (it refuses to run without a TTY)."""
    cmd_str = " ".join([NXCLI, *args])
    log.debug("exec: %s (timeout=%ds)", cmd_str, timeout)
    try:
        master_fd, slave_fd = pty.openpty()
        proc = subprocess.Popen(
            [NXCLI, *args],
            stdin=slave_fd,
            stdout=slave_fd,
            stderr=slave_fd,
            close_fds=True,
        )
        os.close(slave_fd)

        output = []
        while True:
            r, _, _ = select.select([master_fd], [], [], timeout)
            if not r:
                proc.kill()
                os.close(master_fd)
                log.warning("timeout: %s", cmd_str)
                return "", "Command timed out", 1
            try:
                chunk = os.read(master_fd, 4096)
                if not chunk:
                    break
                output.append(chunk.decode("utf-8", errors="replace"))
            except OSError:
                break

        proc.wait(timeout=5)
        os.close(master_fd)
        text = "".join(output).replace("\r\n", "\n").replace("\r", "")
        log.debug("exit %d: %s", proc.returncode, cmd_str)
        log.debug("output: %s", text.strip()[:500])
        return text.strip(), "", proc.returncode
    except Exception as e:
        log.error("failed: %s — %s", cmd_str, e)
        return "", str(e), 1


def get_connections():
    log.info("fetching connection list")
    out, _, rc = run_nxcli("connection", "list")
    if rc != 0 or not out:
        log.info("no connections found (rc=%d)", rc)
        return []
    connections = []
    for line in out.splitlines():
        line = line.strip().strip("\r")
        if not line or line.startswith("+"):
            continue
        cols = [c.strip() for c in line.split("|") if c.strip()]
        if len(cols) >= 2 and cols[0] != "ID":
            connections.append(cols[1])
    log.info("connections: %s", connections)
    return connections


def get_status():
    log.info("fetching status")
    out, _, rc = run_nxcli("status", "-f")
    if rc != 0:
        log.info("status query failed (rc=%d)", rc)
        return None
    try:
        data = json.loads(out.replace("\r", ""))
        log.info("status: connected to %s", data.get("server", "?"))
        return data
    except (json.JSONDecodeError, ValueError):
        log.info("status: disconnected")
        return None


class PasswordDialog(QDialog):
    def __init__(self, connection_name, parent=None):
        super().__init__(parent)
        self.setWindowTitle(f"Connect to {connection_name}")
        self.setMinimumWidth(350)

        layout = QVBoxLayout(self)
        layout.addWidget(QLabel("Password:"))

        self.entry = QLineEdit()
        self.entry.setEchoMode(QLineEdit.EchoMode.Password)
        layout.addWidget(self.entry)

        buttons = QDialogButtonBox(QDialogButtonBox.Ok | QDialogButtonBox.Cancel)
        buttons.accepted.connect(self.accept)
        buttons.rejected.connect(self.reject)
        layout.addWidget(buttons)

        self.entry.returnPressed.connect(self.accept)

    def get_password(self):
        return self.entry.text()


class ConnectionDialog(QDialog):
    def __init__(self, edit_name=None, parent=None):
        super().__init__(parent)
        title = f"Edit Connection: {edit_name}" if edit_name else "Add Connection"
        self.setWindowTitle(title)
        self.setMinimumWidth(400)
        self.edit_name = edit_name

        layout = QVBoxLayout(self)
        form = QFormLayout()

        self.name_entry = QLineEdit()
        form.addRow("Name:", self.name_entry)

        self.server_entry = QLineEdit()
        form.addRow("Server[:port]:", self.server_entry)

        self.domain_entry = QLineEdit()
        form.addRow("Domain:", self.domain_entry)

        self.username_entry = QLineEdit()
        form.addRow("Username:", self.username_entry)

        self.password_entry = QLineEdit()
        self.password_entry.setEchoMode(QLineEdit.EchoMode.Password)
        form.addRow("Password:", self.password_entry)

        self.protocol_combo = QComboBox()
        self.protocol_combo.addItems(["Auto", "TLS", "DTLS", "WireGuard"])
        form.addRow("Protocol:", self.protocol_combo)

        self.always_trust = QCheckBox("Always trust server certificate")
        form.addRow("", self.always_trust)

        layout.addLayout(form)

        buttons = QDialogButtonBox(QDialogButtonBox.Ok | QDialogButtonBox.Cancel)
        buttons.accepted.connect(self.accept)
        buttons.rejected.connect(self.reject)
        layout.addWidget(buttons)

        if edit_name:
            self.name_entry.setText(edit_name)
            self.name_entry.setEnabled(False)
            self._load_connection_details(edit_name)

    def _load_connection_details(self, name):
        out, _, rc = run_nxcli("connection", "detail", name)
        if rc != 0 or not out:
            return
        entries = {
            "server": self.server_entry,
            "domain": self.domain_entry,
            "username": self.username_entry,
            "user": self.username_entry,
        }
        protos = ["auto", "tls", "dtls", "wireguard"]
        for line in out.splitlines():
            line = line.strip()
            if ":" not in line:
                continue
            key, _, value = line.partition(":")
            key = key.strip().lower()
            value = value.strip()
            if key in entries and value:
                entries[key].setText(value)
            elif key == "protocol" and value.lower() in protos:
                self.protocol_combo.setCurrentIndex(protos.index(value.lower()))

    def get_values(self):
        return {
            "name": self.name_entry.text().strip(),
            "server": self.server_entry.text().strip(),
            "domain": self.domain_entry.text().strip(),
            "username": self.username_entry.text().strip(),
            "password": self.password_entry.text(),
            "protocol": self.protocol_combo.currentText(),
            "always_trust": self.always_trust.isChecked(),
        }


class NxTray:
    def __init__(self, app):
        self.app = app
        icon = QIcon(ICON) if os.path.exists(ICON) else QIcon.fromTheme("network-vpn")
        self.tray = QSystemTrayIcon(icon, app)
        self.tray.setToolTip("NetExtender")
        self._build_menu()
        self.tray.show()
        log.info("tray icon shown")

    def _build_menu(self):
        self.menu = QMenu()
        self.menu.aboutToShow.connect(self._on_menu_open)

        self.status_action = self.menu.addAction("Disconnected")
        self.status_action.setEnabled(False)

        self.menu.addSeparator()

        self.connect_menu = self.menu.addMenu("Connect")

        self.disconnect_action = self.menu.addAction("Disconnect")
        self.disconnect_action.triggered.connect(self._on_disconnect)
        self.disconnect_action.setEnabled(False)

        self.menu.addSeparator()

        self.manage_menu = self.menu.addMenu("Connections")

        self.menu.addSeparator()

        quit_action = self.menu.addAction("Quit")
        quit_action.triggered.connect(self.app.quit)

        self.tray.setContextMenu(self.menu)

    def _on_menu_open(self):
        log.info("menu opened — refreshing")

        status = get_status()
        if status and isinstance(status, dict):
            server = status.get("server", "")
            self.status_action.setText(f"Connected to {server}" if server else "Connected")
            self.disconnect_action.setEnabled(True)
        else:
            self.status_action.setText("Disconnected")
            self.disconnect_action.setEnabled(False)

        connections = get_connections()

        self.connect_menu.clear()
        if not connections:
            a = self.connect_menu.addAction("(no connections)")
            a.setEnabled(False)
        else:
            for name in connections:
                action = self.connect_menu.addAction(name)
                action.triggered.connect(lambda checked, n=name: self._on_connect(n))

        self.manage_menu.clear()
        add_action = self.manage_menu.addAction("Add New...")
        add_action.triggered.connect(self._on_add_connection)
        self.manage_menu.addSeparator()
        if not connections:
            a = self.manage_menu.addAction("(no connections)")
            a.setEnabled(False)
        else:
            for name in connections:
                sub = self.manage_menu.addMenu(name)
                edit_action = sub.addAction("Edit...")
                edit_action.triggered.connect(lambda checked, n=name: self._on_edit_connection(n))
                del_action = sub.addAction("Delete")
                del_action.triggered.connect(lambda checked, n=name: self._on_delete_connection(n))

    def _on_connect(self, connection_name):
        log.info("connect requested: %s", connection_name)
        dialog = PasswordDialog(connection_name)
        if dialog.exec() != QDialog.Accepted:
            log.info("connect cancelled by user")
            return
        password = dialog.get_password()
        self.status_action.setText("Connecting...")
        threading.Thread(
            target=self._do_connect,
            args=(connection_name, password),
            daemon=True,
        ).start()

    def _do_connect(self, connection_name, password):
        out, err, rc = run_nxcli("connect", connection_name, "-p", password, timeout=120)
        if rc != 0 or "error" in out.lower():
            msg = err or out or "Connection failed"
            log.error("connect failed: %s", msg)
            QTimer.singleShot(0, lambda: self._show_error(f"Connect failed:\n{msg}"))
        else:
            log.info("connect succeeded: %s", connection_name)

    def _on_disconnect(self):
        log.info("disconnect requested")
        self.status_action.setText("Disconnecting...")
        threading.Thread(target=self._do_disconnect, daemon=True).start()

    def _do_disconnect(self):
        out, err, rc = run_nxcli("disconnect")
        if rc != 0:
            msg = err or out
            log.error("disconnect failed: %s", msg)
            QTimer.singleShot(0, lambda: self._show_error(f"Disconnect failed:\n{msg}"))
        else:
            log.info("disconnect succeeded")

    def _on_add_connection(self):
        log.info("add connection dialog opened")
        dialog = ConnectionDialog()
        if dialog.exec() != QDialog.Accepted:
            return
        values = dialog.get_values()
        if not values["name"] or not values["server"]:
            self._show_error("Name and Server are required.")
            return

        log.info("adding connection: %s -> %s", values["name"], values["server"])
        args = ["connection", "add", values["name"], "-s", values["server"]]
        if values["domain"]:
            args += ["-d", values["domain"]]
        if values["username"]:
            args += ["-u", values["username"]]
        if values["password"]:
            args += ["-p", values["password"]]
        if values["protocol"]:
            args += ["-v", values["protocol"]]
        if values["always_trust"]:
            args.append("--always-trust")

        out, err, rc = run_nxcli(*args)
        if rc != 0:
            log.error("add connection failed: %s", err or out)
            self._show_error(f"Failed to add connection:\n{err or out}")

    def _on_edit_connection(self, name):
        log.info("edit connection dialog opened: %s", name)
        dialog = ConnectionDialog(edit_name=name)
        if dialog.exec() != QDialog.Accepted:
            return
        values = dialog.get_values()

        log.info("deleting old connection: %s", name)
        run_nxcli("connection", "del", name)

        log.info("re-adding connection: %s -> %s", name, values["server"])
        args = ["connection", "add", name, "-s", values["server"], "--force"]
        if values["domain"]:
            args += ["-d", values["domain"]]
        if values["username"]:
            args += ["-u", values["username"]]
        if values["password"]:
            args += ["-p", values["password"]]
        if values["protocol"]:
            args += ["-v", values["protocol"]]
        if values["always_trust"]:
            args.append("--always-trust")

        out, err, rc = run_nxcli(*args)
        if rc != 0:
            log.error("edit connection failed: %s", err or out)
            self._show_error(f"Failed to save connection:\n{err or out}")

    def _on_delete_connection(self, name):
        reply = QMessageBox.question(
            None,
            "Delete Connection",
            f"Delete connection '{name}'?",
        )
        if reply == QMessageBox.Yes:
            log.info("deleting connection: %s", name)
            out, err, rc = run_nxcli("connection", "del", name)
            if rc != 0:
                log.error("delete failed: %s", err or out)
                self._show_error(f"Failed to delete:\n{err or out}")

    def _show_error(self, message):
        QMessageBox.critical(None, "NetExtender", message)


def main():
    parser = argparse.ArgumentParser(description="NetExtender system tray")
    parser.add_argument("-v", "--verbose", action="store_true", help="enable info logging")
    parser.add_argument("-vv", "--debug", action="store_true", help="enable debug logging (includes nxcli output)")
    opts = parser.parse_args()

    level = logging.WARNING
    if opts.debug:
        level = logging.DEBUG
    elif opts.verbose:
        level = logging.INFO
    logging.basicConfig(
        level=level,
        format="%(asctime)s %(levelname)s %(message)s",
        datefmt="%H:%M:%S",
    )

    log.info("starting nx-tray (nxcli=%s)", NXCLI)

    app = QApplication(sys.argv)
    app.setQuitOnLastWindowClosed(False)

    if not QSystemTrayIcon.isSystemTrayAvailable():
        QMessageBox.critical(None, "NetExtender", "System tray not available.")
        sys.exit(1)

    tray = NxTray(app)
    sys.exit(app.exec())


if __name__ == "__main__":
    main()
