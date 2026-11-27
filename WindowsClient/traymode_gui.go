//go:build traygui

package main

// defaultTrayMode is true for the dedicated GUI build (lywp-tray.exe).
// A GUI-subsystem binary never creates a console window, so double-clicking
// it starts the tray service silently with no black window flash.
const defaultTrayMode = true
