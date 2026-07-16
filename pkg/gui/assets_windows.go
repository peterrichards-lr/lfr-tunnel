//go:build windows

package gui

import _ "embed"

//go:embed assets/icon_active.ico
var IconActive []byte

//go:embed assets/icon_inactive.ico
var IconInactive []byte
