//go:build windows

package main

import (
	"log/slog"

	"golang.org/x/sys/windows"
)

// noConsoleSession is what WTSGetActiveConsoleSessionId returns when no
// session is attached to the console.
const noConsoleSession = 0xFFFFFFFF

// consoleUser returns the short name of the user logged on at the console, as
// Munki's ConsoleUser does, or "" when nobody is or the lookup fails.
// WTSQueryUserToken needs SYSTEM, so an interactive run reports "".
func consoleUser() string {
	session := windows.WTSGetActiveConsoleSessionId()
	if session == noConsoleSession {
		return ""
	}
	var token windows.Token
	if err := windows.WTSQueryUserToken(session, &token); err != nil {
		slog.Debug("no console user token", "session", session, "err", err)
		return ""
	}
	defer func() { _ = token.Close() }()
	tokenUser, err := token.GetTokenUser()
	if err != nil {
		slog.Debug("unable to read console user token", "err", err)
		return ""
	}
	account, _, _, err := tokenUser.User.Sid.LookupAccount("")
	if err != nil {
		slog.Debug("unable to look up console user", "err", err)
		return ""
	}
	return account
}
