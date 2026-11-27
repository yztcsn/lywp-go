//go:build windows

package main

// runningAsRoot is never true on Windows; offline downloads there require a
// manually installed aria2c on PATH.
func runningAsRoot() bool { return false }
