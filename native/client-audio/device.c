#define MA_NO_DECODING
#define MA_NO_ENCODING
#define MA_NO_RESOURCE_MANAGER
#define MA_NO_NODE_GRAPH
#define MA_NO_ENGINE
#define MA_NO_GENERATION
#define MINIAUDIO_IMPLEMENTATION
#include "miniaudio.h"
#include <stdlib.h>
#include <string.h>

enum { RA_FRAME = 960, RA_CAPACITY = 8 };
typedef struct { ma_uint32 epoch; double time; float samples[RA_FRAME]; } ra_frame;
typedef struct {
    ma_context context;
    ma_device input, output;
    ma_rb capture;
    ma_pcm_rb playback, reference, monitor;
    ma_bool32 context_ok, input_ok, output_ok, capture_ok, playback_ok, reference_ok, monitor_ok;
    ma_atomic_uint32 epoch, failed, closing;
    ma_timer clock;
    ra_frame pending;
    ma_uint32 captured;
    void (*wake)(void*);
    void *user;
} ra_device;

static ma_uint32 rb_read(ma_pcm_rb *rb, float *out, ma_uint32 count, ma_uint32 channels) {
    ma_uint32 done = 0;
    while (done < count) {
        ma_uint32 n = count-done; void *data = NULL;
        if (ma_pcm_rb_acquire_read(rb, &n, &data) != MA_SUCCESS || n == 0) break;
        memcpy(out+done*channels, data, (size_t)n*channels*sizeof(float));
        ma_pcm_rb_commit_read(rb,n); done+=n;
    }
    return done;
}
static ma_uint32 rb_write(ma_pcm_rb *rb, const float *in, ma_uint32 count, ma_uint32 channels) {
    ma_uint32 done=0;
    while (done<count) {
        ma_uint32 n=count-done; void *data=NULL;
        if (ma_pcm_rb_acquire_write(rb,&n,&data)!=MA_SUCCESS || n==0) break;
        memcpy(data,in+done*channels,(size_t)n*channels*sizeof(float));
        ma_pcm_rb_commit_write(rb,n);done+=n;
    }
    return done;
}
static void data_callback(ma_device *device, void *output, const void *input, ma_uint32 frames) {
    ra_device *d=(ra_device*)device->pUserData;
    if (input) {
        const float *samples=(const float*)input;
        for (ma_uint32 i=0;i<frames;) {
            ma_uint32 epoch=ma_atomic_uint32_get(&d->epoch);
            if (d->pending.epoch!=epoch) {d->pending.epoch=epoch;d->captured=0;}
            ma_uint32 n=RA_FRAME-d->captured;if(n>frames-i)n=frames-i;
            memcpy(d->pending.samples+d->captured,samples+i,(size_t)n*sizeof(float));
            d->captured+=n;i+=n;
            if (d->captured==RA_FRAME) {
                d->pending.time=ma_timer_get_time_in_seconds(&d->clock);
                size_t size=sizeof(ra_frame);void *data=NULL;
                if (ma_rb_acquire_write(&d->capture,&size,&data)==MA_SUCCESS && size==sizeof(ra_frame)) {
                    memcpy(data,&d->pending,sizeof(ra_frame));ma_rb_commit_write(&d->capture,size);
                }
                d->captured=0;
            }
        }
    }
    if (output) {
        float *out=(float*)output;
        memset(out,0,(size_t)frames*2*sizeof(float));
        rb_read(&d->playback,out,frames,2);
        rb_write(&d->reference,out,frames,2);
        float local[RA_FRAME*2];
        for (ma_uint32 i=0;i<frames;) {
            ma_uint32 n=RA_FRAME;if(n>frames-i)n=frames-i;
            memset(local,0,(size_t)n*2*sizeof(float));rb_read(&d->monitor,local,n,2);
            for (ma_uint32 j=0;j<n*2;j++) {float v=out[i*2+j]+local[j];out[i*2+j]=v>1.0f?1.0f:(v< -1.0f? -1.0f:v);}
            i+=n;
        }
    }
    if (d->wake) d->wake(d->user);
}
static void notification_callback(const ma_device_notification *notification) {
    ra_device *d=(ra_device*)notification->pDevice->pUserData;
    if (notification->type==ma_device_notification_type_stopped && !ma_atomic_uint32_get(&d->closing)) {
        ma_atomic_uint32_set(&d->failed,1);if (d->wake) d->wake(d->user);
    }
}
void ra_device_close(ra_device *d) {
    if (!d) return;
    ma_atomic_uint32_set(&d->closing,1);
    if (d->input_ok) ma_device_uninit(&d->input);
    if (d->output_ok) ma_device_uninit(&d->output);
    if (d->context_ok) ma_context_uninit(&d->context);
    if (d->capture_ok) ma_rb_uninit(&d->capture);
    if (d->playback_ok) ma_pcm_rb_uninit(&d->playback);
    if (d->reference_ok) ma_pcm_rb_uninit(&d->reference);
    if (d->monitor_ok) ma_pcm_rb_uninit(&d->monitor);
    free(d);
}
static void id_string(const ma_device_id *id, char *out) {
    const unsigned char *bytes=(const unsigned char*)id;
    size_t n=sizeof(*id);while(n>1 && bytes[n-1]==0)n--;
    static const char digits[]="0123456789abcdef";
    for(size_t i=0;i<n;i++){out[2*i]=digits[bytes[i]>>4];out[2*i+1]=digits[bytes[i]&15];}out[2*n]=0;
}
typedef void (*ra_list_callback)(void*,const char*,const char*,int,int);
int ra_device_list(ra_list_callback callback,void *user) {
    ma_context context;ma_context_config config=ma_context_config_init();
    config.threadPriority=ma_thread_priority_normal;
    int error=ma_context_init(NULL,0,&config,&context);if(error!=MA_SUCCESS)return error;
    ma_device_info *outputs=NULL,*inputs=NULL;ma_uint32 out_count=0,in_count=0;
    error=ma_context_get_devices(&context,&outputs,&out_count,&inputs,&in_count);
    if(error==MA_SUCCESS) {
        for (int kind=0;kind<2;kind++) {
            ma_device_info *list=kind?inputs:outputs;ma_uint32 count=kind?in_count:out_count;
            if (count>256) {error=MA_TOO_BIG;break;}
            for(ma_uint32 i=0;i<count;i++) {char id[sizeof(ma_device_id)*2+1];id_string(&list[i].id,id);callback(user,id,list[i].name,kind,list[i].isDefault);}
        }
    }
    ma_context_uninit(&context);return error;
}
static int open_device(ra_device *d,int input,const char *selected) {
    ma_device_config config=ma_device_config_init(input?ma_device_type_capture:ma_device_type_playback);
    config.sampleRate=48000;config.periodSizeInFrames=RA_FRAME;
    config.performanceProfile=ma_performance_profile_low_latency;
    config.dataCallback=data_callback;config.notificationCallback=notification_callback;config.pUserData=d;
    if(input){config.capture.format=ma_format_f32;config.capture.channels=1;}
    else{config.playback.format=ma_format_f32;config.playback.channels=2;}
    ma_device_id id;
    if(selected && *selected) {
        ma_device_info *outputs=NULL,*inputs=NULL;ma_uint32 out_count=0,in_count=0;
        int error=ma_context_get_devices(&d->context,&outputs,&out_count,&inputs,&in_count);if(error!=MA_SUCCESS)return error;
        ma_device_info *list=input?inputs:outputs;ma_uint32 count=input?in_count:out_count;ma_bool32 found=MA_FALSE;
        for(ma_uint32 i=0;i<count;i++){char text[sizeof(ma_device_id)*2+1];id_string(&list[i].id,text);if(strcmp(text,selected)==0){id=list[i].id;found=MA_TRUE;break;}}
        if(!found)return MA_NO_DEVICE;
        if(input)config.capture.pDeviceID=&id;else config.playback.pDeviceID=&id;
    }
    ma_device *device=input?&d->input:&d->output;
    int error=ma_device_init(&d->context,&config,device);if(error!=MA_SUCCESS)return error;
    if(input)d->input_ok=MA_TRUE;else d->output_ok=MA_TRUE;
    return ma_device_start(device);
}
ra_device *ra_device_open(int input,int output,const char *input_id,const char *output_id,void(*wake)(void*),void *user,int *error) {
    ra_device *d=(ra_device*)calloc(1,sizeof(*d));if(!d){*error=MA_OUT_OF_MEMORY;return NULL;}
    d->wake=wake;d->user=user;ma_timer_init(&d->clock);
    *error=ma_rb_init(sizeof(ra_frame)*RA_CAPACITY,NULL,NULL,&d->capture);if(*error!=MA_SUCCESS)goto fail;d->capture_ok=MA_TRUE;
    *error=ma_pcm_rb_init(ma_format_f32,2,RA_FRAME*RA_CAPACITY,NULL,NULL,&d->playback);if(*error!=MA_SUCCESS)goto fail;d->playback_ok=MA_TRUE;
    *error=ma_pcm_rb_init(ma_format_f32,2,RA_FRAME*RA_CAPACITY,NULL,NULL,&d->reference);if(*error!=MA_SUCCESS)goto fail;d->reference_ok=MA_TRUE;
    *error=ma_pcm_rb_init(ma_format_f32,2,RA_FRAME*RA_CAPACITY,NULL,NULL,&d->monitor);if(*error!=MA_SUCCESS)goto fail;d->monitor_ok=MA_TRUE;
    ma_context_config config=ma_context_config_init();config.threadPriority=ma_thread_priority_highest;
    *error=ma_context_init(NULL,0,&config,&d->context);if(*error!=MA_SUCCESS)goto fail;d->context_ok=MA_TRUE;
    if(output){*error=open_device(d,0,output_id);if(*error!=MA_SUCCESS)goto fail;}
    if(input){*error=open_device(d,1,input_id);if(*error!=MA_SUCCESS)goto fail;}
    return d;
fail:ra_device_close(d);return NULL;
}
int ra_device_failed(ra_device *d){return (int)ma_atomic_uint32_get(&d->failed);}
void ra_device_epoch(ra_device *d,ma_uint32 epoch){ma_atomic_uint32_set(&d->epoch,epoch);}
int ra_device_capture(ra_device *d,ra_frame *out) {
    size_t n=sizeof(*out);void *data=NULL;
    if(ma_rb_acquire_read(&d->capture,&n,&data)!=MA_SUCCESS || n!=sizeof(*out))return 0;
    memcpy(out,data,n);ma_rb_commit_read(&d->capture,n);
    out->time=ma_timer_get_time_in_seconds(&d->clock)-out->time;return 1;
}
ma_uint32 ra_device_reference(ra_device *d,float *out){return rb_read(&d->reference,out,RA_FRAME,2);}
ma_uint32 ra_device_write(ra_device *d,const float *samples,int monitor){return rb_write(monitor?&d->monitor:&d->playback,samples,RA_FRAME,2);}
