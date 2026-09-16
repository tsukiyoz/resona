package main

/*
#cgo pkg-config: opus
#cgo LDFLAGS: -lresona_opus_bench -lm
#cgo linux LDFLAGS: -ldl -lpthread
#include <opus.h>
#include <stdbool.h>
#include <stddef.h>
extern void *bench_encoder_new(int, int, int, bool);
extern int bench_encode(void *, const float *, unsigned char *, size_t);
extern void *bench_decoder_new(int);
extern int bench_decode(void *, const unsigned char *, size_t, float *);
extern void bench_encoder_free(void *);
extern void bench_decoder_free(void *);
static int configure_opus(OpusEncoder *e, int bitrate, int complexity, int cbr) {
    int result;
    if ((result = opus_encoder_ctl(e, OPUS_SET_BITRATE(bitrate))) != OPUS_OK) return result;
    if ((result = opus_encoder_ctl(e, OPUS_SET_COMPLEXITY(complexity))) != OPUS_OK) return result;
    if ((result = opus_encoder_ctl(e, OPUS_SET_VBR(!cbr))) != OPUS_OK) return result;
    if ((result = opus_encoder_ctl(e, OPUS_SET_VBR_CONSTRAINT(1))) != OPUS_OK) return result;
    if ((result = opus_encoder_ctl(e, OPUS_SET_DTX(0))) != OPUS_OK) return result;
    if ((result = opus_encoder_ctl(e, OPUS_SET_INBAND_FEC(0))) != OPUS_OK) return result;
    return opus_encoder_ctl(e, OPUS_SET_PACKET_LOSS_PERC(0));
}
static int bridge_noop(void) { return 0; }
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type nativeEncoder struct {
	kind int
	ptr  unsafe.Pointer
}
type nativeDecoder struct {
	kind int
	ptr  unsafe.Pointer
}

func newNativeEncoder(kind, bitrate, complexity int, cbr bool) (encoder, error) {
	e := &nativeEncoder{kind: kind}
	if kind == 0 {
		var status C.int
		p := C.opus_encoder_create(rate, 1, C.OPUS_APPLICATION_VOIP, &status)
		if p == nil || status != 0 {
			return nil, fmt.Errorf("libopus create: %d", status)
		}
		flag := C.int(0)
		if cbr {
			flag = 1
		}
		if status = C.configure_opus(p, C.int(bitrate), C.int(complexity), flag); status != 0 {
			C.opus_encoder_destroy(p)
			return nil, fmt.Errorf("libopus configure: %d", status)
		}
		e.ptr = unsafe.Pointer(p)
	} else {
		e.ptr = C.bench_encoder_new(C.int(kind), C.int(bitrate), C.int(complexity), C.bool(cbr))
		if e.ptr == nil {
			return nil, fmt.Errorf("Rust encoder create failed: %d", kind)
		}
	}
	return e, nil
}
func (e *nativeEncoder) Encode(pcm []float32, packet []byte) (int, error) {
	var n C.int
	if e.kind == 0 {
		n = C.opus_encode_float((*C.OpusEncoder)(e.ptr), (*C.float)(unsafe.Pointer(&pcm[0])), frameSize,
			(*C.uchar)(unsafe.Pointer(&packet[0])), C.opus_int32(len(packet)))
	} else {
		n = C.bench_encode(e.ptr, (*C.float)(unsafe.Pointer(&pcm[0])), (*C.uchar)(unsafe.Pointer(&packet[0])), C.size_t(len(packet)))
	}
	if n <= 0 || int(n) > len(packet) {
		return 0, fmt.Errorf("encode kind=%d result=%d", e.kind, n)
	}
	return int(n), nil
}
func (e *nativeEncoder) Close() {
	if e.kind == 0 {
		C.opus_encoder_destroy((*C.OpusEncoder)(e.ptr))
	} else {
		C.bench_encoder_free(e.ptr)
	}
}
func newNativeDecoder(kind int) (decoder, error) {
	d := &nativeDecoder{kind: kind}
	if kind == 0 {
		var status C.int
		d.ptr = unsafe.Pointer(C.opus_decoder_create(rate, 1, &status))
		if d.ptr == nil || status != 0 {
			return nil, fmt.Errorf("libopus decoder create: %d", status)
		}
	} else {
		d.ptr = C.bench_decoder_new(C.int(kind))
		if d.ptr == nil {
			return nil, fmt.Errorf("Rust decoder create failed: %d", kind)
		}
	}
	return d, nil
}
func (d *nativeDecoder) Decode(packet []byte, pcm []float32) (int, error) {
	var n C.int
	if d.kind == 0 {
		n = C.opus_decode_float((*C.OpusDecoder)(d.ptr), (*C.uchar)(unsafe.Pointer(&packet[0])), C.opus_int32(len(packet)),
			(*C.float)(unsafe.Pointer(&pcm[0])), frameSize, 0)
	} else {
		n = C.bench_decode(d.ptr, (*C.uchar)(unsafe.Pointer(&packet[0])), C.size_t(len(packet)), (*C.float)(unsafe.Pointer(&pcm[0])))
	}
	if n != frameSize {
		return 0, fmt.Errorf("decode kind=%d result=%d", d.kind, n)
	}
	return int(n), nil
}
func (d *nativeDecoder) Close() {
	if d.kind == 0 {
		C.opus_decoder_destroy((*C.OpusDecoder)(d.ptr))
	} else {
		C.bench_decoder_free(d.ptr)
	}
}
func nativeVersion() string { return C.GoString(C.opus_get_version_string()) }
func bridgeNoop()           { C.bridge_noop() }
