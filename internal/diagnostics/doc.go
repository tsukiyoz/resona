// Package diagnostics records bounded, best-effort Go client events in memory.
// Capacity and time limits prevent unbounded retention; recording never writes
// log files. Only an explicit diagnostic export may persist client events.
package diagnostics
