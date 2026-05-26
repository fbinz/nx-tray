package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	ptyPkg "github.com/creack/pty"
)

//go:embed nx-icon.png
var iconData []byte

const maxConns = 10

var (
	nxcliBin string
	verbose  bool
	debug    bool
)

func main() {
	flag.StringVar(&nxcliBin, "nxcli", envOr("NXCLI", "/usr/sbin/nxcli"), "path to nxcli binary")
	flag.BoolVar(&verbose, "v", false, "enable info logging")
	flag.BoolVar(&debug, "vv", false, "enable debug logging")
	flag.Parse()

	if !verbose && !debug {
		log.SetOutput(io.Discard)
	}
	if debug {
		verbose = true
	}

	systray.Run(onReady, func() {})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// --- nxcli ---

func redactArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if i > 0 && (args[i-1] == "-p" || args[i-1] == "--password") {
			out[i] = "***"
		} else {
			out[i] = a
		}
	}
	return strings.Join(out, " ")
}

func runNxcli(timeout time.Duration, args ...string) (string, int, error) {
	cmdStr := nxcliBin + " " + redactArgs(args)
	if debug {
		log.Printf("exec: %s (timeout=%s)", cmdStr, timeout)
	}

	cmd := exec.Command(nxcliBin, args...)
	ptmx, err := ptyPkg.Start(cmd)
	if err != nil {
		return "", -1, fmt.Errorf("pty start: %w", err)
	}

	var buf strings.Builder
	done := make(chan struct{})
	go func() {
		io.Copy(&buf, ptmx)
		close(done)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
	case <-timer.C:
		cmd.Process.Kill()
		ptmx.Close()
		<-done
		cmd.Wait()
		log.Printf("timeout: %s", cmdStr)
		return "", -1, fmt.Errorf("timed out")
	}

	ptmx.Close()
	cmd.Wait()

	rc := cmd.ProcessState.ExitCode()
	text := strings.ReplaceAll(buf.String(), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "")
	text = strings.TrimSpace(text)

	if debug {
		preview := text
		if len(preview) > 500 {
			preview = preview[:500]
		}
		log.Printf("exit %d: %s\noutput: %s", rc, cmdStr, preview)
	}

	return text, rc, nil
}

func getConnections() []string {
	if verbose {
		log.Println("fetching connection list")
	}
	out, rc, err := runNxcli(30*time.Second, "connection", "list")
	if err != nil || rc != 0 || out == "" {
		return nil
	}
	var result []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "+") {
			continue
		}
		var cols []string
		for _, part := range strings.Split(line, "|") {
			if s := strings.TrimSpace(part); s != "" {
				cols = append(cols, s)
			}
		}
		if len(cols) >= 2 && cols[0] != "ID" {
			result = append(result, cols[1])
		}
	}
	if verbose {
		log.Printf("connections: %v", result)
	}
	return result
}

type vpnStatus struct {
	Server string `json:"server"`
}

func getStatus() *vpnStatus {
	if verbose {
		log.Println("fetching status")
	}
	out, rc, err := runNxcli(30*time.Second, "status", "-f")
	if err != nil || rc != 0 {
		return nil
	}
	var s vpnStatus
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		if verbose {
			log.Println("status: disconnected")
		}
		return nil
	}
	if verbose {
		log.Printf("status: connected to %s", s.Server)
	}
	return &s
}

func getConnectionDetail(name string) map[string]string {
	out, rc, err := runNxcli(30*time.Second, "connection", "detail", name)
	if err != nil || rc != 0 || out == "" {
		return nil
	}
	detail := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, ":") {
			continue
		}
		key, value, _ := strings.Cut(line, ":")
		key = strings.TrimSpace(strings.ToLower(key))
		value = strings.TrimSpace(value)
		if value != "" {
			detail[key] = value
		}
	}
	return detail
}

// --- zenity dialogs ---

func zenityPassword(connName string) (string, bool) {
	cmd := exec.Command("zenity", "--password", "--title=Connect to "+connName)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func zenityError(msg string) {
	exec.Command("zenity", "--error", "--text="+msg, "--title=NetExtender").Run()
}

func zenityConfirm(msg string) bool {
	return exec.Command("zenity", "--question", "--text="+msg, "--title=NetExtender").Run() == nil
}

func zenityConnectionForm(title, info string) (map[string]string, bool) {
	args := []string{
		"--forms",
		"--title=" + title,
		"--separator=\n",
		"--add-entry=Name",
		"--add-entry=Server[:port]",
		"--add-entry=Domain",
		"--add-entry=Username",
		"--add-password=Password",
		"--add-combo=Protocol",
		"--combo-values=Auto|TLS|DTLS|WireGuard",
	}
	if info != "" {
		args = append(args, "--text="+info)
	}
	cmd := exec.Command("zenity", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 5 {
		return nil, false
	}
	result := map[string]string{
		"name":     lines[0],
		"server":   lines[1],
		"domain":   lines[2],
		"username": lines[3],
		"password": lines[4],
	}
	if len(lines) >= 6 {
		result["protocol"] = lines[5]
	}
	return result, true
}

// --- connection helpers ---

func buildAddArgs(name string, vals map[string]string) []string {
	args := []string{"connection", "add", name, "-s", vals["server"], "--force"}
	if vals["domain"] != "" {
		args = append(args, "-d", vals["domain"])
	}
	if vals["username"] != "" {
		args = append(args, "-u", vals["username"])
	}
	if vals["password"] != "" {
		args = append(args, "-p", vals["password"])
	}
	if vals["protocol"] != "" {
		args = append(args, "-v", vals["protocol"])
	}
	return args
}

// --- tray ---

type app struct {
	mu    sync.Mutex
	conns []string

	status     *systray.MenuItem
	connectSub [maxConns]*systray.MenuItem
	disconnect *systray.MenuItem
	addConn    *systray.MenuItem
	manageSub  [maxConns]struct {
		parent *systray.MenuItem
		edit   *systray.MenuItem
		del    *systray.MenuItem
	}
}

func onReady() {
	systray.SetIcon(iconData)
	systray.SetTooltip("NetExtender")

	a := &app{}

	a.status = systray.AddMenuItem("Disconnected", "")
	a.status.Disable()

	systray.AddSeparator()

	connectMenu := systray.AddMenuItem("Connect", "")
	for i := range maxConns {
		a.connectSub[i] = connectMenu.AddSubMenuItem("", "")
		a.connectSub[i].Hide()
	}

	a.disconnect = systray.AddMenuItem("Disconnect", "")
	a.disconnect.Disable()

	systray.AddSeparator()

	manageMenu := systray.AddMenuItem("Connections", "")
	a.addConn = manageMenu.AddSubMenuItem("Add New...", "")
	for i := range maxConns {
		a.manageSub[i].parent = manageMenu.AddSubMenuItem("", "")
		a.manageSub[i].edit = a.manageSub[i].parent.AddSubMenuItem("Edit...", "")
		a.manageSub[i].del = a.manageSub[i].parent.AddSubMenuItem("Delete", "")
		a.manageSub[i].parent.Hide()
	}

	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "")

	a.refresh()

	for i := range maxConns {
		go func(idx int) {
			for range a.connectSub[idx].ClickedCh {
				if name := a.connName(idx); name != "" {
					a.doConnect(name)
				}
			}
		}(i)
		go func(idx int) {
			for range a.manageSub[idx].edit.ClickedCh {
				if name := a.connName(idx); name != "" {
					a.doEditConnection(name)
				}
			}
		}(i)
		go func(idx int) {
			for range a.manageSub[idx].del.ClickedCh {
				if name := a.connName(idx); name != "" {
					a.doDeleteConnection(name)
				}
			}
		}(i)
	}

	go func() {
		for range a.disconnect.ClickedCh {
			a.doDisconnect()
		}
	}()

	go func() {
		for range a.addConn.ClickedCh {
			a.doAddConnection()
		}
	}()

	go func() {
		<-quit.ClickedCh
		systray.Quit()
	}()
}

func (a *app) connName(idx int) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if idx < len(a.conns) {
		return a.conns[idx]
	}
	return ""
}

func (a *app) refresh() {
	if verbose {
		log.Println("refreshing menu")
	}

	status := getStatus()
	if status != nil {
		label := "Connected"
		if status.Server != "" {
			label = "Connected to " + status.Server
		}
		a.status.SetTitle(label)
		a.disconnect.Enable()
	} else {
		a.status.SetTitle("Disconnected")
		a.disconnect.Disable()
	}

	a.mu.Lock()
	a.conns = getConnections()
	current := make([]string, len(a.conns))
	copy(current, a.conns)
	a.mu.Unlock()

	for i := range maxConns {
		if i < len(current) {
			a.connectSub[i].SetTitle(current[i])
			a.connectSub[i].Show()
			a.manageSub[i].parent.SetTitle(current[i])
			a.manageSub[i].parent.Show()
		} else {
			a.connectSub[i].Hide()
			a.manageSub[i].parent.Hide()
		}
	}
}

func (a *app) doConnect(name string) {
	log.Printf("connect: %s", name)
	password, ok := zenityPassword(name)
	if !ok {
		log.Println("connect cancelled")
		return
	}
	a.status.SetTitle("Connecting...")
	out, _, err := runNxcli(120*time.Second, "connect", name, "-p", password)
	if err != nil || strings.Contains(strings.ToLower(out), "error") {
		msg := out
		if err != nil {
			msg = err.Error()
		}
		log.Printf("connect failed: %s", msg)
		zenityError("Connect failed:\n" + msg)
	} else {
		log.Printf("connect succeeded: %s", name)
	}
	a.refresh()
}

func (a *app) doDisconnect() {
	log.Println("disconnect")
	a.status.SetTitle("Disconnecting...")
	out, _, err := runNxcli(30*time.Second, "disconnect")
	if err != nil {
		log.Printf("disconnect failed: %s", err)
		zenityError("Disconnect failed:\n" + err.Error())
	} else if strings.Contains(strings.ToLower(out), "error") {
		log.Printf("disconnect failed: %s", out)
		zenityError("Disconnect failed:\n" + out)
	} else {
		log.Println("disconnect succeeded")
	}
	a.refresh()
}

func (a *app) doAddConnection() {
	log.Println("add connection")
	vals, ok := zenityConnectionForm("Add Connection", "")
	if !ok {
		return
	}
	if vals["name"] == "" || vals["server"] == "" {
		zenityError("Name and Server are required.")
		return
	}

	log.Printf("adding: %s -> %s", vals["name"], vals["server"])
	args := buildAddArgs(vals["name"], vals)
	out, _, err := runNxcli(30*time.Second, args...)
	if err != nil {
		zenityError("Failed to add connection:\n" + err.Error())
	} else if strings.Contains(strings.ToLower(out), "error") || strings.Contains(strings.ToLower(out), "failed") {
		zenityError("Failed to add connection:\n" + out)
	}
	a.refresh()
}

func (a *app) doEditConnection(name string) {
	log.Printf("edit: %s", name)
	detail := getConnectionDetail(name)
	info := ""
	if detail != nil {
		info = fmt.Sprintf("Current: server=%s, domain=%s, user=%s, proto=%s",
			detail["server"], detail["domain"], detail["username"], detail["protocol"])
	}

	vals, ok := zenityConnectionForm("Edit: "+name, info)
	if !ok {
		return
	}
	vals["name"] = name

	if vals["server"] == "" {
		zenityError("Server is required.")
		return
	}

	log.Printf("deleting old: %s", name)
	runNxcli(30*time.Second, "connection", "del", name)

	log.Printf("re-adding: %s -> %s", name, vals["server"])
	args := buildAddArgs(name, vals)
	out, _, err := runNxcli(30*time.Second, args...)
	if err != nil {
		zenityError("Failed to save connection:\n" + err.Error())
	} else if strings.Contains(strings.ToLower(out), "error") || strings.Contains(strings.ToLower(out), "failed") {
		zenityError("Failed to save connection:\n" + out)
	}
	a.refresh()
}

func (a *app) doDeleteConnection(name string) {
	if !zenityConfirm(fmt.Sprintf("Delete connection '%s'?", name)) {
		return
	}
	log.Printf("deleting: %s", name)
	out, _, err := runNxcli(30*time.Second, "connection", "del", name)
	if err != nil {
		zenityError("Failed to delete:\n" + err.Error())
	} else if strings.Contains(strings.ToLower(out), "error") || strings.Contains(strings.ToLower(out), "failed") {
		zenityError("Failed to delete:\n" + out)
	}
	a.refresh()
}
