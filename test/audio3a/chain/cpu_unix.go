//go:build darwin || linux

package main

import "syscall"

func cpuTime() int64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		panic(err)
	}
	return usage.Utime.Nano() + usage.Stime.Nano()
}
