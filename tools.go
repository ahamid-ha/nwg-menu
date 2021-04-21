package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/joshuarubin/go-sway"
)

/*
Window on-leave-notify event hides the dock with glib Timeout 1000 ms.
We might have left the window by accident, so let's clear the timeout if window re-entered.
Furthermore - hovering a button triggers window on-leave-notify event, and the timeout
needs to be cleared as well.
*/
func cancelClose() {
	if src > 0 {
		glib.SourceRemove(src)
		src = 0
	}
}

func inPinned(taskID string) bool {
	for _, id := range pinned {
		if strings.TrimSpace(taskID) == strings.TrimSpace(id) {
			return true
		}
	}
	return false
}

func createImage(appID string, size int) (*gtk.Image, error) {
	name, err := getIcon(appID)
	if err != nil {
		name = appID
	}
	pixbuf, err := createPixbuf(name, size)
	if err != nil {
		return nil, err
	}
	image, _ := gtk.ImageNewFromPixbuf(pixbuf)

	return image, nil
}

func createPixbuf(icon string, size int) (*gdk.Pixbuf, error) {
	if strings.HasPrefix(icon, "/") {
		pixbuf, err := gdk.PixbufNewFromFileAtSize(icon, size, size)
		if err != nil {
			println(err)
			return nil, err
		}
		return pixbuf, nil
	}

	iconTheme, err := gtk.IconThemeGetDefault()
	if err != nil {
		log.Fatal("Couldn't get default theme: ", err)
	}
	pixbuf, err := iconTheme.LoadIcon(icon, size, gtk.ICON_LOOKUP_FORCE_SIZE)
	if err != nil {
		ico, err := getIcon(icon)
		if err != nil {
			return nil, err
		}

		if strings.HasPrefix(ico, "/") {
			pixbuf, err := gdk.PixbufNewFromFileAtSize(ico, size, size)
			if err != nil {
				return nil, err
			}
			return pixbuf, nil
		}

		pixbuf, err := iconTheme.LoadIcon(ico, size, gtk.ICON_LOOKUP_FORCE_SIZE)
		if err != nil {
			return nil, err
		}
		return pixbuf, nil
	}
	return pixbuf, nil
}

func cacheDir() string {
	if os.Getenv("XDG_CACHE_HOME") != "" {
		return os.Getenv("XDG_CONFIG_HOME")
	}
	if os.Getenv("HOME") != "" && pathExists(filepath.Join(os.Getenv("HOME"), ".cache")) {
		p := filepath.Join(os.Getenv("HOME"), ".cache")
		return p
	}
	return ""
}

func tempDir() string {
	if os.Getenv("TMPDIR") != "" {
		return os.Getenv("TMPDIR")
	} else if os.Getenv("TEMP") != "" {
		return os.Getenv("TEMP")
	} else if os.Getenv("TMP") != "" {
		return os.Getenv("TMP")
	}
	return "/tmp"
}

func readTextFile(path string) (string, error) {
	bytes, err := ioutil.ReadFile(path)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

func configDir() string {
	if os.Getenv("XDG_CONFIG_HOME") != "" {
		return (fmt.Sprintf("%s/nwg-panel", os.Getenv("XDG_CONFIG_HOME")))
	}
	return (fmt.Sprintf("%s/.config/nwg-panel", os.Getenv("HOME")))
}

func createDir(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err := os.MkdirAll(dir, os.ModePerm)
		if err == nil {
			fmt.Println("Creating dir:", dir)
		}
	}
}

func copyFile(src, dst string) error {
	fmt.Println("Copying file:", dst)

	var err error
	var srcfd *os.File
	var dstfd *os.File
	var srcinfo os.FileInfo

	if srcfd, err = os.Open(src); err != nil {
		return err
	}
	defer srcfd.Close()

	if dstfd, err = os.Create(dst); err != nil {
		return err
	}
	defer dstfd.Close()

	if _, err = io.Copy(dstfd, srcfd); err != nil {
		return err
	}
	if srcinfo, err = os.Stat(src); err != nil {
		return err
	}
	return os.Chmod(dst, srcinfo.Mode())
}

func getAppDirs() []string {
	var dirs []string
	xdgDataDirs := ""

	home := os.Getenv("HOME")
	xdgDataHome := os.Getenv("XDG_DATA_HOME")
	if os.Getenv("XDG_DATA_DIRS") != "" {
		xdgDataDirs = os.Getenv("XDG_DATA_DIRS")
	} else {
		xdgDataDirs = "/usr/local/share/:/usr/share/"
	}
	if xdgDataHome != "" {
		dirs = append(dirs, filepath.Join(xdgDataHome, "applications"))
	} else if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local/share/applications"))
	}
	for _, d := range strings.Split(xdgDataDirs, ":") {
		dirs = append(dirs, filepath.Join(d, "applications"))
	}
	flatpakDirs := []string{filepath.Join(home, ".local/share/flatpak/exports/share/applications"),
		"/var/lib/flatpak/exports/share/applications"}

	for _, d := range flatpakDirs {
		if !isIn(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

func isIn(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

func getIcon(appName string) (string, error) {
	p := ""
	for _, d := range appDirs {
		path := filepath.Join(d, fmt.Sprintf("%s.desktop", appName))
		if pathExists(path) {
			p = path
		} else if pathExists(strings.ToLower(path)) {
			p = strings.ToLower(path)
		}
	}
	/* Some apps' app_id varies from their .desktop file name, e.g. 'gimp-2.9.9' or 'pamac-manager'.
	   Let's try to find a matching .desktop file name */
	if !strings.HasPrefix(appName, "/") && p == "" { // skip icon paths given instead of names
		p = searchDesktopDirs(appName)
	}

	if p != "" {
		lines, err := loadTextFile(p)
		if err != nil {
			return "", err
		}
		for _, line := range lines {
			if strings.HasPrefix(strings.ToUpper(line), "ICON") {
				return strings.Split(line, "=")[1], nil
			}
		}
	}
	return "", errors.New("Couldn't find the icon")
}

func searchDesktopDirs(badAppID string) string {
	b4Hyphen := strings.Split(badAppID, "-")[0]
	for _, d := range appDirs {
		items, _ := ioutil.ReadDir(d)
		for _, item := range items {
			if strings.Contains(item.Name(), b4Hyphen) {
				//Let's check items starting from 'org.' first
				if strings.Count(item.Name(), ".") > 1 {
					return filepath.Join(d, item.Name())
				}
			}
		}
	}
	return ""
}

func getExec(appName string) (string, error) {
	cmd := appName
	if strings.HasPrefix(strings.ToUpper(appName), "GIMP") {
		cmd = "gimp"
	}
	for _, d := range appDirs {
		files, _ := ioutil.ReadDir(d)
		path := ""
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".desktop") {
				if f.Name() == fmt.Sprintf("%s.desktop", appName) ||
					f.Name() == fmt.Sprintf("%s.desktop", strings.ToLower(appName)) {
					path = filepath.Join(d, f.Name())
					break
				}
			}
		}

		// as above in getIcon - for tasks w/ improper app_id
		if path == "" {
			path = searchDesktopDirs(appName)
		}

		if path != "" {
			lines, err := loadTextFile(path)
			if err != nil {
				return "", err
			}
			for _, line := range lines {
				if strings.HasPrefix(strings.ToUpper(line), "EXEC") {
					l := line[5:]
					cutAt := strings.Index(l, "%")
					if cutAt != -1 {
						l = l[:cutAt-1]
					}
					cmd = l
					break
				}
			}
			return cmd, nil
		}
	}
	return cmd, nil
}

func getName(appName string) string {
	name := appName
	for _, d := range appDirs {
		files, _ := ioutil.ReadDir(d)
		path := ""
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".desktop") {
				if f.Name() == fmt.Sprintf("%s.desktop", appName) ||
					f.Name() == fmt.Sprintf("%s.desktop", strings.ToLower(appName)) {
					path = filepath.Join(d, f.Name())
					break
				}
			}
		}

		if path != "" {
			lines, err := loadTextFile(path)
			if err != nil {
				return name
			}
			for _, line := range lines {
				if strings.HasPrefix(strings.ToUpper(line), "NAME") {
					name = line[5:]
					break
				}
			}
		}
	}
	return name
}

func pathExists(name string) bool {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

func loadTextFile(path string) ([]string, error) {
	bytes, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(bytes), "\n")
	var output []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			output = append(output, line)
		}

	}
	return output, nil
}

func pinTask(itemID string) {
	for _, item := range pinned {
		if item == itemID {
			println(item, "already pinned")
			return
		}
	}
	pinned = append(pinned, itemID)
	savePinned()
	refresh = true
}

func unpinTask(itemID string) {
	pinned = remove(pinned, itemID)
	savePinned()
	refresh = true
}

func remove(s []string, r string) []string {
	for i, v := range s {
		if v == r {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func savePinned() {
	f, err := os.OpenFile(pinnedFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		log.Fatal(err)
	}

	defer f.Close()

	for _, line := range pinned {
		if line != "" {
			_, err := f.WriteString(line + "\n")

			if err != nil {
				println("Error saving pinned", err)
			}
		}

	}
}

func launch(ID string) {
	e, err := getExec(ID)
	if err != nil {
		println(err)
	}

	elements := strings.Split(e, " ")

	// find prepended env variables, if any
	envVarsNum := strings.Count(e, "=")
	var envVars []string

	cmdIdx := 0
	lastEnvVarIdx := 0

	if envVarsNum > 0 {
		for idx, item := range elements {
			if strings.Contains(item, "=") {
				lastEnvVarIdx = idx
				envVars = append(envVars, item)
			}
		}
		cmdIdx = lastEnvVarIdx + 1
	}

	cmd := exec.Command(elements[cmdIdx], elements[1+cmdIdx:]...)

	if len(envVars) > 0 {
		cmd.Env = os.Environ()
		for _, envVar := range envVars {
			cmd.Env = append(cmd.Env, envVar)
		}
	}

	msg := fmt.Sprintf("env vars: %s; command: '%s'; args: %s\n", envVars, elements[cmdIdx], elements[1+cmdIdx:])
	println(msg)

	go cmd.Run()

	if *autohide {
		/*src, _ = glib.TimeoutAdd(uint(1000), func() bool {
			dockWindow.Hide()
			return false
		})*/
		mainWindow.Hide()
	}
}

// Returns map output name -> gdk.Monitor
func mapOutputs() (map[string]*gdk.Monitor, error) {
	result := make(map[string]*gdk.Monitor)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	client, err := sway.New(ctx)
	if err != nil {
		return nil, err
	}

	outputs, err := client.GetOutputs(ctx)
	if err != nil {
		return nil, err
	}

	display, err := gdk.DisplayGetDefault()
	if err != nil {
		return nil, err
	}

	num := display.GetNMonitors()
	for i := 0; i < num; i++ {
		monitor, _ := display.GetMonitor(i)
		geometry := monitor.GetGeometry()
		// assign output to monitor on the basis of the same x, y coordinates
		for _, output := range outputs {
			if int(output.Rect.X) == geometry.GetX() && int(output.Rect.Y) == geometry.GetY() {
				result[output.Name] = monitor
			}
		}
	}
	return result, nil
}

func listMonitors() ([]gdk.Monitor, error) {
	var monitors []gdk.Monitor
	display, err := gdk.DisplayGetDefault()
	if err != nil {
		return nil, err
	}

	num := display.GetNMonitors()
	for i := 0; i < num; i++ {
		monitor, _ := display.GetMonitor(i)
		monitors = append(monitors, *monitor)
	}
	return monitors, nil
}
