#ifndef WISP_AUDIO_H
#define WISP_AUDIO_H

typedef struct WispAudio WispAudio;
int wispAudioAvailable(void);
WispAudio *wispAudioOpen(int input, int *status);
int wispAudioRead(WispAudio *, float *, int);
void wispAudioWrite(WispAudio *, const float *, int);
void wispAudioClose(WispAudio *);
#endif
