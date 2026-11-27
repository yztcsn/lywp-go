//go:build !traygui

package main

// defaultTrayMode is false for the normal console build (lywp-server.exe),
// which keeps install/serve/hash commands and their console output.
const defaultTrayMode = false
