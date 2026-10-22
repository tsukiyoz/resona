/* Standalone callback/ring checks; does not enumerate or open OS devices. */
#include "device.c"
#include <assert.h>
#include <math.h>

int main(void) {
    ra_device *d=(ra_device*)calloc(1,sizeof(*d));assert(d);
    ma_timer_init(&d->clock);
    assert(ma_rb_init(sizeof(ra_frame)*RA_CAPACITY,NULL,NULL,&d->capture)==MA_SUCCESS);d->capture_ok=MA_TRUE;
    assert(ma_pcm_rb_init(ma_format_f32,2,RA_FRAME*RA_CAPACITY,NULL,NULL,&d->playback)==MA_SUCCESS);d->playback_ok=MA_TRUE;
    assert(ma_pcm_rb_init(ma_format_f32,2,RA_FRAME*RA_CAPACITY,NULL,NULL,&d->reference)==MA_SUCCESS);d->reference_ok=MA_TRUE;
    assert(ma_pcm_rb_init(ma_format_f32,2,RA_FRAME*RA_CAPACITY,NULL,NULL,&d->monitor)==MA_SUCCESS);d->monitor_ok=MA_TRUE;
    ma_device fake={0};fake.pUserData=d;
    float input[RA_FRAME],out[RA_FRAME*2],stereo[RA_FRAME*2];
    for(int i=0;i<RA_FRAME;i++)input[i]=0.25f;
    for(int i=0;i<RA_FRAME*2;i++)stereo[i]=0.4f;
    ra_frame frame;
    for(unsigned epoch=1;epoch<=100;epoch++) {
        ra_device_epoch(d,epoch);
        for(int i=0;i<20;i++)data_callback(&fake,NULL,input,RA_FRAME);
        int count=0;
        while(ra_device_capture(d,&frame)) {
            assert(frame.epoch==epoch);assert(frame.time>=0 && frame.time<1);
            for(int i=0;i<RA_FRAME;i++)assert(frame.samples[i]==0.25f);
            count++;
        }
        assert(count==RA_CAPACITY);
    }
    ra_device_epoch(d,101);data_callback(&fake,NULL,input,RA_FRAME/2);
    ra_device_epoch(d,102);data_callback(&fake,NULL,input,RA_FRAME/2);
    assert(!ra_device_capture(d,&frame));
    data_callback(&fake,NULL,input,RA_FRAME/2);assert(ra_device_capture(d,&frame));assert(frame.epoch==102);
    assert(ra_device_write(d,stereo,0)==RA_FRAME);
    assert(ra_device_write(d,stereo,1)==RA_FRAME);
    data_callback(&fake,out,NULL,RA_FRAME);
    for(int i=0;i<RA_FRAME*2;i++)assert(fabsf(out[i]-0.8f)<0.00001f);
    assert(ra_device_reference(d,out)==RA_FRAME);
    for(int i=0;i<RA_FRAME*2;i++)assert(out[i]==0.4f);
    data_callback(&fake,out,NULL,RA_FRAME);
    for(int i=0;i<RA_FRAME*2;i++)assert(out[i]==0.0f);
    assert(ra_device_pending(d)==0);
    for(int i=0;i<20;i++) {
        assert(ra_device_write(d,stereo,0)==RA_FRAME);
        assert(ra_device_write(d,stereo,0)==RA_FRAME);
        assert(ra_device_pending(d)==RA_FRAME*2);
        for(int part=0;part<4;part++) {
            data_callback(&fake,out,NULL,RA_FRAME/2);
            assert(ra_device_pending(d)==RA_FRAME*2-(part+1)*RA_FRAME/2);
            for(int j=0;j<RA_FRAME;j++)assert(out[j]==0.4f);
        }
    }
    ra_device_close(d);
    return 0;
}
