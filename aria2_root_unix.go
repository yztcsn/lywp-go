//go:build !windows

package main

import "os"

// runningAsRoot reports whether automatic package installation is possible.
func runningAsRoot() bool { return os.Geteuid() == 0 }
