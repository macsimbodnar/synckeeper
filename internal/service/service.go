// Package service installs synckeeper watch as a login service: launchd
// agent (macOS), systemd user unit (Linux), Task Scheduler task (Windows).
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const label = "com.macsimbodnar.synckeeper"

// Install writes the platform wrapper for `binPath watch` and activates it.
// Returns a human-readable summary of what happened.
func Install(binPath string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return installLaunchd(binPath)
	case "linux":
		return installSystemd(binPath)
	case "windows":
		return installTaskScheduler(binPath)
	default:
		return "", fmt.Errorf("no service wrapper for %s", runtime.GOOS)
	}
}

// Uninstall deactivates and removes the wrapper.
func Uninstall() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return uninstallLaunchd()
	case "linux":
		return uninstallSystemd()
	case "windows":
		return uninstallTaskScheduler()
	default:
		return "", fmt.Errorf("no service wrapper for %s", runtime.GOOS)
	}
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --- macOS: launchd ------------------------------------------------------

func launchdPlist(binPath, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>watch</string>
	</array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
	<key>StandardOutPath</key><string>%s</string>
	<key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, label, binPath, logPath, logPath)
}

func launchdPaths() (plist, log string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"),
		filepath.Join(home, "Library", "Logs", "synckeeper.log"), nil
}

func installLaunchd(binPath string) (string, error) {
	plistPath, logPath, err := launchdPaths()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(plistPath, []byte(launchdPlist(binPath, logPath)), 0o644); err != nil {
		return "", err
	}
	run("launchctl", "unload", plistPath) // ignore: not loaded on first install
	if err := run("launchctl", "load", plistPath); err != nil {
		return "", err
	}
	return fmt.Sprintf("launchd agent installed and started.\n  plist: %s\n  logs:  %s", plistPath, logPath), nil
}

func uninstallLaunchd() (string, error) {
	plistPath, _, err := launchdPaths()
	if err != nil {
		return "", err
	}
	run("launchctl", "unload", plistPath) // ignore: may not be loaded
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return "launchd agent stopped and removed.", nil
}

// --- Linux: systemd user unit --------------------------------------------

func systemdUnit(binPath string) string {
	return fmt.Sprintf(`[Unit]
Description=Synckeeper continuous sync
After=network-online.target

[Service]
ExecStart=%s watch
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
`, binPath)
}

func systemdUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", "synckeeper.service"), nil
}

func installSystemd(binPath string) (string, error) {
	unitPath, err := systemdUnitPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(unitPath, []byte(systemdUnit(binPath)), 0o644); err != nil {
		return "", err
	}
	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Sprintf("unit written to %s but systemctl failed; enable manually:\n  systemctl --user enable --now synckeeper", unitPath), nil
	}
	if err := run("systemctl", "--user", "enable", "--now", "synckeeper"); err != nil {
		return "", err
	}
	return fmt.Sprintf("systemd user unit installed and started.\n  unit: %s\n  logs: journalctl --user -u synckeeper", unitPath), nil
}

func uninstallSystemd() (string, error) {
	unitPath, err := systemdUnitPath()
	if err != nil {
		return "", err
	}
	run("systemctl", "--user", "disable", "--now", "synckeeper") // ignore: may not be enabled
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	run("systemctl", "--user", "daemon-reload")
	return "systemd user unit stopped and removed.", nil
}

// --- Windows: Task Scheduler ----------------------------------------------

const taskName = "Synckeeper"

func taskSchedulerXML(binPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers>
    <LogonTrigger><Enabled>true</Enabled></LogonTrigger>
  </Triggers>
  <Settings>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>10</Count>
    </RestartOnFailure>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
  </Settings>
  <Actions>
    <Exec>
      <Command>%s</Command>
      <Arguments>watch</Arguments>
    </Exec>
  </Actions>
</Task>
`, binPath)
}

func installTaskScheduler(binPath string) (string, error) {
	xmlPath := filepath.Join(filepath.Dir(binPath), "synckeeper-task.xml")
	if err := os.WriteFile(xmlPath, []byte(taskSchedulerXML(binPath)), 0o644); err != nil {
		return "", err
	}
	if err := run("schtasks", "/Create", "/TN", taskName, "/F", "/XML", xmlPath); err != nil {
		return "", err
	}
	if err := run("schtasks", "/Run", "/TN", taskName); err != nil {
		return "", err
	}
	return fmt.Sprintf("scheduled task %q installed and started.\n  xml: %s", taskName, xmlPath), nil
}

func uninstallTaskScheduler() (string, error) {
	run("schtasks", "/End", "/TN", taskName) // ignore: may not be running
	if err := run("schtasks", "/Delete", "/TN", taskName, "/F"); err != nil {
		return "", err
	}
	return fmt.Sprintf("scheduled task %q removed.", taskName), nil
}
