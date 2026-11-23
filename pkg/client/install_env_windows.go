//go:build windows

package client

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// repointInstallEnvPlatform re-points what install.ps1 wrote to the user environment
// (HKCU\Environment): LDM_LFR_TUNNEL_BIN when it names the old binary, and a user PATH entry for
// the old directory. Running programs are then told the environment changed, so a terminal opened
// afterwards sees the new values without signing out.
func repointInstallEnvPlatform(home, oldExe, newExe string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("opening the user environment: %w", err)
	}
	defer func() {
		if cerr := k.Close(); cerr != nil {
			fmt.Printf("[Warning] closing the user environment key: %v\n", cerr)
		}
	}()

	var errs []error
	changed := false
	if v, _, err := k.GetStringValue(ldmBinEnvVar); err == nil && strings.EqualFold(v, oldExe) {
		if err := k.SetStringValue(ldmBinEnvVar, newExe); err != nil {
			errs = append(errs, fmt.Errorf("setting %s: %w", ldmBinEnvVar, err))
		} else {
			changed = true
			fmt.Printf("[Update] %s now names %s.\n", ldmBinEnvVar, newExe)
		}
	}
	if v, typ, err := k.GetStringValue("Path"); err == nil {
		if nv, ok := replacePathEntry(v, filepath.Dir(oldExe), filepath.Dir(newExe), ";", true); ok {
			set := k.SetStringValue
			if typ == registry.EXPAND_SZ {
				set = k.SetExpandStringValue
			}
			if err := set("Path", nv); err != nil {
				errs = append(errs, fmt.Errorf("updating the user PATH: %w", err))
			} else {
				changed = true
				fmt.Printf("[Update] Your PATH now names %s instead of %s.\n", filepath.Dir(newExe), filepath.Dir(oldExe))
			}
		}
	}
	if changed {
		broadcastEnvironmentChange()
	}
	return errors.Join(errs...)
}

// broadcastEnvironmentChange sends WM_SETTINGCHANGE for "Environment", as setx and the System
// Properties dialog do, so Explorer picks up the new user environment. Best effort: without it the
// values still apply from the next sign-in.
func broadcastEnvironmentChange() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
	)
	env, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	// SendMessageTimeoutW takes the "Environment" string and the result slot by address; both
	// outlive the call, which is synchronous.
	if r, _, callErr := proc.Call(hwndBroadcast, wmSettingChange, 0,
		uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result))); r == 0 { //nolint:gosec // G103: a Win32 call by pointer
		fmt.Printf("[Notice] Could not announce the environment change (%v); it applies from your next sign-in.\n", callErr)
	}
}
