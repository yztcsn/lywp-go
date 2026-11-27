//go:build linux

package main

import "syscall"

type StorageInfo struct {
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
	Used  uint64 `json:"used"`
}

func storageInfo(path string) StorageInfo {
	var st syscall.Statfs_t
	var out StorageInfo
	if err := syscall.Statfs(path, &st); err != nil {
		return out
	}
	out.Total = st.Blocks * uint64(st.Bsize)
	out.Free = st.Bavail * uint64(st.Bsize)
	out.Used = out.Total - st.Bfree*uint64(st.Bsize)
	return out
}
