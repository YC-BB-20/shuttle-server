package app

import "github.com/YC-BB-20/shuttle-server/internal/launcher"

// loginItem is the owner's "open at login" choice, kept as a launchd agent.
type loginItem struct{ bin, data string }

func (l loginItem) Enabled() bool     { return launcher.LoginEnabled() }
func (l loginItem) Set(on bool) error { return launcher.SetLogin(on, l.bin, l.data) }
