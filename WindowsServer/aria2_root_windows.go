//go:build windows

package main

// runningAsRoot is never true on Windows; offline downloads there require a
// manually installed aria2c.exe either on PATH or next to the server binary.
func runningAsRoot() bool { return false }

const aria2Hint = "未找到 aria2；请把 aria2c.exe 放在本程序同目录，或加入 PATH 后重试"
