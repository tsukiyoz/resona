#include <stdlib.h>
#include <speex/speex_echo.h>
#include <speex/speex_preprocess.h>

typedef struct {
    SpeexEchoState *echo;
    SpeexPreprocessState *pre;
    int frame;
    spx_int16_t capture[960], render[960], output[960];
} BenchSpeex;

void bench_speex_free(BenchSpeex *p) {
    if (!p) return;
    if (p->echo) speex_echo_state_destroy(p->echo);
    if (p->pre) speex_preprocess_state_destroy(p->pre);
    free(p);
}

BenchSpeex *bench_speex_new_frame(int echo, int ns, int agc, int frame) {
    if (frame != 480 && frame != 960) return NULL;
    BenchSpeex *p = calloc(1, sizeof(*p));
    if (!p) return NULL;
    p->frame = frame;
    p->pre = speex_preprocess_state_init(frame, 48000);
    if (!p->pre) { bench_speex_free(p); return NULL; }
    int noise_db = -20, max_gain = 18;
    float target = 8192.0f;
    speex_preprocess_ctl(p->pre, SPEEX_PREPROCESS_SET_DENOISE, &ns);
    speex_preprocess_ctl(p->pre, SPEEX_PREPROCESS_SET_NOISE_SUPPRESS, &noise_db);
    speex_preprocess_ctl(p->pre, SPEEX_PREPROCESS_SET_AGC, &agc);
    speex_preprocess_ctl(p->pre, SPEEX_PREPROCESS_SET_AGC_LEVEL, &target);
    speex_preprocess_ctl(p->pre, SPEEX_PREPROCESS_SET_AGC_MAX_GAIN, &max_gain);
    if (echo) {
        p->echo = speex_echo_state_init(frame, 9600);
        if (!p->echo) { bench_speex_free(p); return NULL; }
        int rate = 48000;
        speex_echo_ctl(p->echo, SPEEX_ECHO_SET_SAMPLING_RATE, &rate);
        speex_preprocess_ctl(p->pre, SPEEX_PREPROCESS_SET_ECHO_STATE, p->echo);
    }
    return p;
}

BenchSpeex *bench_speex_new(int echo, int ns, int agc) {
    return bench_speex_new_frame(echo, ns, agc, 480);
}

static spx_int16_t pcm16(float x) {
    float v = x * 32768.0f;
    return (spx_int16_t)(v > 32767 ? 32767 : v < -32768 ? -32768 : v);
}

void bench_speex_process(BenchSpeex *p, float *capture, const float *render) {
    for (int i = 0; i < p->frame; ++i) {
        p->capture[i] = pcm16(capture[i]);
        p->render[i] = pcm16(render[i]);
    }
    spx_int16_t *out = p->capture;
    if (p->echo) {
        speex_echo_cancellation(p->echo, p->capture, p->render, p->output);
        out = p->output;
    }
    speex_preprocess_run(p->pre, out);
    for (int i = 0; i < p->frame; ++i) capture[i] = out[i] / 32768.0f;
}
