//go:build !linux

package main

type StorageInfo struct {
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
	Used  uint64 `json:"used"`
}

// storageInfo is a no-op fallback for local development on non-Linux hosts.
func storageInfo(path string) StorageInfo { return StorageInfo{} }
