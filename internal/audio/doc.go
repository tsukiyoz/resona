// Package audio provides the Go reference voice pipeline used by tests, bots and benchmarks.
// It owns bounded capture/playback buffers and DSP lifetimes independently of UI.
// The shipped desktop uses the Rust client audio pipeline.
package audio
