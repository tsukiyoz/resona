package main

/*
#cgo LDFLAGS: -lresona_3a_bench -lm
#include <stdint.h>
#include <stddef.h>
extern void *bench_packet_new(uint32_t);
extern int32_t bench_packet_process(void *, float *, size_t);
extern void bench_packet_free(void *);
extern uint32_t bench_packet_model(void);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type processor struct{ handle unsafe.Pointer }

func newProcessor(kind int) (*processor, error) {
	p := C.bench_packet_new(C.uint32_t(kind))
	if p == nil {
		return nil, fmt.Errorf("cannot create processor %d", kind)
	}
	return &processor{p}, nil
}

func (p *processor) process(frame []float32) error {
	if len(frame) != frameSize || p == nil || p.handle == nil {
		return fmt.Errorf("processor requires a live handle and 960 samples")
	}
	if code := C.bench_packet_process(p.handle, (*C.float)(unsafe.Pointer(&frame[0])), C.size_t(len(frame))); code != 0 {
		return fmt.Errorf("processing failed: %d", code)
	}
	return nil
}

func (p *processor) close() {
	if p != nil && p.handle != nil {
		C.bench_packet_free(p.handle)
		p.handle = nil
	}
}

func modelName() string {
	if C.bench_packet_model() == 1 {
		return "little"
	}
	return "regular"
}
