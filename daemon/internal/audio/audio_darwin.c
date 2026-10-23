#include "audio_darwin.h"
#include <AudioToolbox/AudioToolbox.h>
#include <CoreAudio/CoreAudio.h>
#include <pthread.h>
#include <stdlib.h>
#include <stdbool.h>
#include <string.h>

#define FRAME 960
#define CAPACITY (FRAME * 5)
struct WispAudio {
    AudioQueueRef queue;
    pthread_mutex_t mutex;
    float samples[CAPACITY];
    int size;
};

void wispAudioWrite(WispAudio *s, const float *data, int count) {
    pthread_mutex_lock(&s->mutex);
    if (count > CAPACITY) { data += count - CAPACITY; count = CAPACITY; }
    int drop = s->size + count - CAPACITY;
    if (drop > 0) { memmove(s->samples, s->samples + drop, (s->size-drop)*sizeof(float)); s->size -= drop; }
    memcpy(s->samples+s->size,data,count*sizeof(float)); s->size += count;
    pthread_mutex_unlock(&s->mutex);
}

int wispAudioRead(WispAudio *s, float *out, int count) {
    pthread_mutex_lock(&s->mutex);
    int got = s->size >= count ? count : 0;
    if (got) {
        memcpy(out,s->samples,got*sizeof(float)); s->size -= got;
        memmove(s->samples,s->samples+got,s->size*sizeof(float));
    }
    pthread_mutex_unlock(&s->mutex);
    return got;
}

static void inputCallback(void *user, AudioQueueRef queue, AudioQueueBufferRef buffer,
                          const AudioTimeStamp *time, UInt32 packets, const AudioStreamPacketDescription *desc) {
    wispAudioWrite(user,buffer->mAudioData,buffer->mAudioDataByteSize/sizeof(float));
    AudioQueueEnqueueBuffer(queue,buffer,0,NULL);
}
static void outputCallback(void *user, AudioQueueRef queue, AudioQueueBufferRef buffer) {
    buffer->mAudioDataByteSize=FRAME*sizeof(float);
    if (!wispAudioRead(user,buffer->mAudioData,FRAME)) memset(buffer->mAudioData,0,buffer->mAudioDataByteSize);
    AudioQueueEnqueueBuffer(queue,buffer,0,NULL);
}
int wispAudioAvailable(void) {
    AudioDeviceID device=0;
    UInt32 size=sizeof(device);
    AudioObjectPropertyAddress address={kAudioHardwarePropertyDefaultOutputDevice,kAudioObjectPropertyScopeGlobal,kAudioObjectPropertyElementMain};
    return AudioObjectGetPropertyData(kAudioObjectSystemObject,&address,0,NULL,&size,&device)==0 && device!=kAudioObjectUnknown;
}
WispAudio *wispAudioOpen(int input, int *status) {
    WispAudio *s=calloc(1,sizeof(*s));
    if (!s) { *status=-1; return NULL; }
    pthread_mutex_init(&s->mutex,NULL);
    AudioStreamBasicDescription format={0};
    format.mSampleRate=16000; format.mFormatID=kAudioFormatLinearPCM;
    format.mFormatFlags=kAudioFormatFlagIsFloat|kAudioFormatFlagIsPacked;
    format.mBytesPerPacket=sizeof(float); format.mFramesPerPacket=1;
    format.mBytesPerFrame=sizeof(float); format.mChannelsPerFrame=1; format.mBitsPerChannel=32;
    *status=input ? AudioQueueNewInput(&format,inputCallback,s,NULL,NULL,0,&s->queue)
                  : AudioQueueNewOutput(&format,outputCallback,s,NULL,NULL,0,&s->queue);
    if (*status) { wispAudioClose(s); return NULL; }
    for (int i=0;i<3;i++) {
        AudioQueueBufferRef buffer;
        *status=AudioQueueAllocateBuffer(s->queue,FRAME*sizeof(float),&buffer);
        if (*status) { wispAudioClose(s); return NULL; }
        if (input) AudioQueueEnqueueBuffer(s->queue,buffer,0,NULL);
        else outputCallback(s,s->queue,buffer);
    }
    *status=AudioQueueStart(s->queue,NULL);
    if (*status) { wispAudioClose(s); return NULL; }
    return s;
}
void wispAudioClose(WispAudio *s) {
    if (s->queue) { AudioQueueStop(s->queue,true); AudioQueueDispose(s->queue,true); }
    pthread_mutex_destroy(&s->mutex); free(s);
}
