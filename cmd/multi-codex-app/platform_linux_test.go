package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLinuxStandaloneProfiles(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "desktop app")
	probe := "#!/bin/sh\nprintf '%s\\n' \"$CODEX_HOME\" \"${CODEX_ELECTRON_USER_DATA_PATH-unset}\" \"${CODEX_DESKTOP_RELAUNCH_OPEN_EVENTS-unset}\" \"$PWD\" \"$GIT_CEILING_DIRECTORIES\" \"$@\"\n"
	if err := os.WriteFile(app, []byte(probe), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", "/foreign-home")
	t.Setenv("CODEX_ELECTRON_USER_DATA_PATH", "/foreign-desktop")
	t.Setenv("CODEX_DESKTOP_RELAUNCH_OPEN_EVENTS", "foreign-event")
	t.Setenv("GIT_CEILING_DIRECTORIES", "/foreign-ceiling")
	uri := "codex://connector/oauth_callback?code=a%2Bb&state=c%2Fd&literal=$(false)"
	for _, p := range []Profile{
		{ID: "1", Name: "Personal", CodexHome: root, WorkingDirectory: root},
		{ID: "2", Name: "Work", CodexHome: root, UserDataDir: filepath.Join(root, "desktop data"), WorkingDirectory: root, WMClass: "chatgpt-work"},
	} {
		p.Environment = map[string]string{"GIT_CEILING_DIRECTORIES": "/work-ceiling"}
		c := Config{Version: 1, AppPath: app, CLIPath: app, Profiles: []Profile{p}}
		cmd := desktopCommand(c, p, uri)
		if cmd.Path != app {
			t.Fatal("standalone launch was wrapped", cmd.Path)
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		desktop := "unset"
		args := []string{}
		if p.UserDataDir != "" {
			desktop = p.UserDataDir
			args = append(args, "--user-data-dir="+p.UserDataDir)
		}
		want := append([]string{root, desktop, "unset", root, "/work-ceiling"}, append(args, "--class="+linuxWindowClass(p), uri)...)
		got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("profile %s received %q; want %q", p.ID, got, want)
		}
		if err := saveConfig(root, c); err != nil {
			t.Fatal(err)
		}
		read, err := readConfig(root)
		if err != nil || !equalProfiles(read.Profiles[0], p) {
			t.Fatal("launch settings did not survive manifest round trip", err)
		}
		body := linuxDesktopEntry(root, c, p)
		if !strings.Contains(body, "StartupWMClass="+linuxWindowClass(p)+"\n") {
			t.Fatal("menu and window classes differ", body)
		}
	}
}

func TestLinuxProfileValidation(t *testing.T) {
	root := t.TempDir()
	for _, mutate := range []func(*Profile){
		func(p *Profile) { p.UserDataDir = "relative" },
		func(p *Profile) { p.WorkingDirectory = "relative" },
		func(p *Profile) { p.WorkingDirectory = "/work\nExec=other" },
		func(p *Profile) { p.WMClass = "class\nExec=other" },
		func(p *Profile) { p.Environment = map[string]string{"CODEX_HOME": "/wrong"} },
		func(p *Profile) { p.Environment = map[string]string{"BAD=KEY": "value"} },
		func(p *Profile) { p.Environment = map[string]string{"BAD": "nul\x00value"} },
	} {
		p := Profile{ID: "1", Name: "Personal", CodexHome: root}
		mutate(&p)
		if validateConfig(Config{Version: 1, AppPath: root, CLIPath: root, Profiles: []Profile{p}}) == nil {
			t.Fatal("invalid launch settings accepted", p)
		}
	}
}

func TestLinuxDesktopEntries(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	bin := t.TempDir()
	for name, body := range map[string]string{
		"xdg-mime":                "#!/bin/sh\nif [ \"$1\" = query ]; then printf '%s\\n' chatgpt.desktop; fi\n",
		"update-desktop-database": "#!/bin/sh\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := Config{CLIPath: filepath.Join(root, "cli"), Profiles: []Profile{{ID: "2", Name: "Work", WorkingDirectory: filepath.Join(root, "work space")}}}
	if err := installIntegration(root, &c, false); err != nil {
		t.Fatal(err)
	}
	if c.PreviousHandler != "chatgpt.desktop" {
		t.Fatal("previous callback handler lost", c.PreviousHandler)
	}
	dir := filepath.Join(home, "data", "applications")
	launcher, helper := filepath.Join(dir, "multi-codex-profile-2.desktop"), filepath.Join(dir, "multi-codex-helper.desktop")
	if desktopValues(launcher)["StartupWMClass"] != "multi-codex-profile-2" {
		t.Fatal("default window class missing")
	}
	if !strings.HasPrefix(desktopValues(helper)["Exec"], "/usr/bin/env ") {
		t.Fatal("callback entry is not discoverable by xdg-utils")
	}
	if validator, err := exec.LookPath("desktop-file-validate"); err == nil {
		if out, err := exec.Command(validator, launcher, helper).CombinedOutput(); err != nil {
			t.Fatalf("invalid desktop entries: %v: %s", err, out)
		}
	}
}
