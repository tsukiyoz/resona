package main

import "syscall"

func cpuTime() int64 {
	handle, err := syscall.GetCurrentProcess()
	if err != nil {
		panic(err)
	}
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		panic(err)
	}
	// FILETIME durations use 100 ns ticks; Nano() would subtract the epoch.
	ticks := func(t syscall.Filetime) int64 {
		return int64(uint64(t.HighDateTime)<<32 | uint64(t.LowDateTime))
	}
	return (ticks(kernel) + ticks(user)) * 100
}
