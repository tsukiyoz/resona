//go:build cgo

package webrtc

/*
#cgo linux LDFLAGS: -ldl
#include <stdint.h>
#include <stdlib.h>
#include <stdio.h>
#ifdef _WIN32
#include <windows.h>
typedef HMODULE resona_library;
static resona_library resona_open(const char *path) {
    int n = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, NULL, 0);
    if (!n) return NULL;
    wchar_t *wide = (wchar_t *)malloc((size_t)n * sizeof(wchar_t));
    if (!wide) return NULL;
    MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, wide, n);
    resona_library lib = LoadLibraryW(wide);
    free(wide);
    return lib;
}
#define resona_symbol GetProcAddress
#define resona_unload FreeLibrary
#else
#include <dlfcn.h>
typedef void *resona_library;
static resona_library resona_open(const char *path) { return dlopen(path, RTLD_NOW | RTLD_LOCAL); }
static void *resona_symbol(resona_library lib, const char *name) { return dlsym(lib, name); }
static void resona_unload(resona_library lib) { dlclose(lib); }
#endif

typedef uint32_t (*version_fn)(void);
typedef void *(*new_fn)(uint32_t, int32_t, int32_t, int32_t);
typedef int32_t (*process_fn)(void *, float *, const float *);
typedef void (*free_fn)(void *);
typedef struct {
    resona_library lib;
    void *instance;
    process_fn process;
    free_fn release;
} resona_stage;

static resona_stage *resona_stage_open(const char *path, uint32_t kind, int32_t level,
                                      int32_t headroom, int32_t max_gain, char *error, size_t size) {
    resona_library lib = resona_open(path);
    if (!lib) { snprintf(error, size, "cannot load WebRTC audio library"); return NULL; }
    version_fn version = (version_fn)resona_symbol(lib, "resona_apm_abi_version");
    new_fn create = (new_fn)resona_symbol(lib, "resona_apm_new");
    process_fn process = (process_fn)resona_symbol(lib, "resona_apm_process");
    free_fn release = (free_fn)resona_symbol(lib, "resona_apm_free");
    if (!version || version() != 1 || !create || !process || !release) {
        snprintf(error, size, "incompatible WebRTC audio library");
        resona_unload(lib);
        return NULL;
    }
    void *instance = create(kind, level, headroom, max_gain);
    if (!instance) {
        snprintf(error, size, "cannot initialize WebRTC audio processor");
        resona_unload(lib);
        return NULL;
    }
    resona_stage *stage = (resona_stage *)malloc(sizeof(*stage));
    if (!stage) {
        release(instance);
        resona_unload(lib);
        snprintf(error, size, "cannot allocate WebRTC audio processor");
        return NULL;
    }
    *stage = (resona_stage){lib, instance, process, release};
    return stage;
}

static int32_t resona_stage_process(resona_stage *stage, float *samples, const float *reference) {
    return stage->process(stage->instance, samples, reference);
}
static void resona_stage_close(resona_stage *stage) {
    if (!stage) return;
    stage->release(stage->instance);
    resona_unload(stage->lib);
    free(stage);
}
*/
import "C"

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"
)

type Stage struct{ handle *C.resona_stage }

func libraryPath() string {
	name := "libresona_webrtc_apm.so"
	switch runtime.GOOS {
	case "darwin":
		name = "libresona_webrtc_apm.dylib"
	case "windows":
		name = "resona_webrtc_apm.dll"
	}
	dir := os.Getenv("RESONA_WEBRTC_PLUGIN_DIR")
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			return ""
		}
		dir = filepath.Dir(exe)
		if runtime.GOOS == "darwin" && filepath.Base(dir) == "MacOS" {
			return filepath.Join(filepath.Dir(dir), "Frameworks", name)
		}
	}
	return filepath.Join(dir, name)
}

func Available() bool {
	path := libraryPath()
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// New loads a backend only when selected. A Stage is owned by one audio worker.
func New(kind uint32, level, headroom, maxGain int) (*Stage, error) {
	path := libraryPath()
	if path == "" {
		return nil, errors.New("WebRTC 音频库路径不可用")
	}
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))
	var message [128]C.char
	handle := C.resona_stage_open(name, C.uint32_t(kind), C.int32_t(level), C.int32_t(headroom), C.int32_t(maxGain), &message[0], C.size_t(len(message)))
	if handle == nil {
		return nil, errors.New(C.GoString(&message[0]))
	}
	return &Stage{handle: handle}, nil
}

// Process accepts one 48 kHz mono 20 ms frame. Nonzero codes are native errors.
func (s *Stage) Process(samples, reference []float32) int {
	if s == nil || s.handle == nil || len(samples) != 960 {
		return -1
	}
	var render *C.float
	if len(reference) == 960 {
		render = (*C.float)(unsafe.Pointer(&reference[0]))
	}
	return int(C.resona_stage_process(s.handle, (*C.float)(unsafe.Pointer(&samples[0])), render))
}

func (s *Stage) Close() {
	if s != nil && s.handle != nil {
		C.resona_stage_close(s.handle)
		s.handle = nil
	}
}
