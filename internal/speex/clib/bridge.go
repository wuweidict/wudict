// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build cgo

// Package clib is a thin cgo wrapper over a vendored decode-only subset of
// libspeex (BSD-3, see LICENSE.speex). It exposes just enough to decode one
// Ogg-Speex audio packet to PCM; Ogg demuxing and header parsing live in the
// parent pure-Go package. The C glue is adapted from winlinvip/go-speex (MIT),
// rewritten to be driven by the stream's declared mode and to build the
// libspeex sources in-tree (no prebuilt library, no ./configure).
package clib

/*
#cgo CFLAGS: -DHAVE_CONFIG_H -I${SRCDIR}
#cgo LDFLAGS: -lm
#include <stdlib.h>
#include <string.h>
#include "speex/speex.h"
#include "speex/speex_callbacks.h"
#include "speex/speex_stereo.h"

typedef struct {
	void*     state;
	SpeexBits bits;
	int       frame_size;
	int       channels;         // 1 or 2; 2 expands each frame in place
	SpeexStereoState stereo;    // meaningful only when channels == 2
} spxdec_t;

// spxdec_new initializes a decoder for mode 0 (narrow), 1 (wide) or 2
// (ultra-wide), with channels 1 or 2. Perceptual enhancement is enabled to
// match the speexdec CLI.
//
// Speex stereo is INTENSITY stereo: the codec itself always decodes one mono
// signal, and each packet carries an in-band request holding that frame's
// left/right balance. Decoding a stereo file therefore needs no second
// decoder — only this handler, which keeps the balance in d->stereo for
// speex_decode_stereo_int to apply. Without it the in-band bits are merely
// skipped by libspeex's default handler, which is why a stereo stream decodes
// to plausible-sounding mono even when nothing here knows it is stereo.
// speexdec.c does exactly this (process_header, "if (!(*channels==1))").
static spxdec_t* spxdec_new(int mode_id, int channels) {
	const SpeexMode* mode = speex_lib_get_mode(mode_id);
	if (!mode) return NULL;
	void* st = speex_decoder_init(mode);
	if (!st) return NULL;
	spxdec_t* d = (spxdec_t*)malloc(sizeof(spxdec_t));
	if (!d) { speex_decoder_destroy(st); return NULL; }
	memset(d, 0, sizeof(*d));
	d->state = st;
	d->channels = (channels == 2) ? 2 : 1;
	speex_bits_init(&d->bits);
	spx_int32_t fs = 0;
	speex_decoder_ctl(st, SPEEX_GET_FRAME_SIZE, &fs);
	d->frame_size = (int)fs;
	spx_int32_t enh = 1;
	speex_decoder_ctl(st, SPEEX_SET_ENH, &enh);
	if (d->channels == 2) {
		// The macro initializer, as speexdec uses it. stereo.c's
		// COMPATIBILITY_HACK re-initializes any state whose reserved1 is not
		// 0xdeadbeef on first use, so this is correct in both the float and
		// the fixed-point build.
		SpeexStereoState init = SPEEX_STEREO_STATE_INIT;
		d->stereo = init;
		SpeexCallback cb;
		memset(&cb, 0, sizeof(cb));
		cb.callback_id = SPEEX_INBAND_STEREO;
		cb.func = speex_std_stereo_request_handler;
		cb.data = &d->stereo;              // d is heap-allocated and never moves
		// SPEEX_SET_HANDLER copies the three fields it uses; on mode 1/2 the
		// wideband decoder forwards the ctl to its narrowband half, which is
		// where in-band data is actually parsed (sb_celp.c:1105).
		speex_decoder_ctl(st, SPEEX_SET_HANDLER, &cb);
	}
	return d;
}

static int spxdec_frame_size(spxdec_t* d) { return d->frame_size; }
static int spxdec_channels(spxdec_t* d) { return d->channels; }

// spxdec_lookahead is the codec's algorithmic delay in samples: the number of
// leading samples the decoder emits before the encoder's first real one.
static int spxdec_lookahead(spxdec_t* d) {
	spx_int32_t la = 0;
	speex_decoder_ctl(d->state, SPEEX_GET_LOOKAHEAD, &la);
	return (int)la;
}

static void spxdec_free(spxdec_t* d) {
	if (!d) return;
	speex_decoder_destroy(d->state);
	speex_bits_destroy(&d->bits);
	free(d);
}

// spxdec_decode decodes up to `frames` frames from one packet into `out`
// (frames*frame_size*channels int16 samples). Returns samples written —
// interleaved when channels == 2 — or -2 on a corrupt frame.
static int spxdec_decode(spxdec_t* d, char* packet, int len, int frames, short* out) {
	speex_bits_read_from(&d->bits, packet, len);
	int n = 0;
	for (int i = 0; i < frames; i++) {
		// The frame is decoded mono into the first frame_size slots of its own
		// stride, then expanded in place over the full stride: stereo.c walks
		// i downwards writing data[2*i], so the source samples must sit at the
		// bottom of a buffer twice their length.
		int ret = speex_decode_int(d->state, &d->bits, (spx_int16_t*)(out + n));
		if (ret == -1) break;   // no more data in this packet
		if (ret == -2) return -2; // corrupt frame
		if (d->channels == 2) {
			speex_decode_stereo_int((spx_int16_t*)(out + n), d->frame_size, &d->stereo);
		}
		n += d->frame_size * d->channels;
	}
	return n;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Decoder is a libspeex decoder for one mode, mono or intensity stereo.
type Decoder struct {
	h *C.spxdec_t
}

// New creates a decoder for mode 0 (narrow, 8k), 1 (wide, 16k) or 2
// (ultra-wide, 32k), with channels 1 or 2; any other channel count is decoded
// as mono.
func New(mode, channels int) (*Decoder, error) {
	h := C.spxdec_new(C.int(mode), C.int(channels))
	if h == nil {
		return nil, fmt.Errorf("speex: unsupported mode %d", mode)
	}
	return &Decoder{h: h}, nil
}

// FrameSize is the number of samples PER CHANNEL produced per decoded frame.
func (d *Decoder) FrameSize() int { return int(C.spxdec_frame_size(d.h)) }

// Channels is 1 or 2 — the number the decoder was actually built with, which
// is what DecodePacket interleaves.
func (d *Decoder) Channels() int { return int(C.spxdec_channels(d.h)) }

// Lookahead is the codec's algorithmic delay in samples per channel — the
// leading samples the caller must drop to align the output with the encoder's
// input (speexdec does the same, via SPEEX_GET_LOOKAHEAD).
func (d *Decoder) Lookahead() int { return int(C.spxdec_lookahead(d.h)) }

// DecodePacket decodes `frames` frames from one Ogg-Speex audio packet,
// returning up to frames*FrameSize*Channels PCM samples (16-bit, host order,
// interleaved when stereo).
func (d *Decoder) DecodePacket(packet []byte, frames int) ([]int16, error) {
	if frames <= 0 {
		frames = 1
	}
	out := make([]int16, frames*d.FrameSize()*d.Channels())
	var pkt *C.char
	if len(packet) > 0 {
		pkt = (*C.char)(unsafe.Pointer(&packet[0]))
	}
	n := C.spxdec_decode(d.h, pkt, C.int(len(packet)), C.int(frames),
		(*C.short)(unsafe.Pointer(&out[0])))
	if int(n) == -2 {
		return nil, fmt.Errorf("speex: corrupt frame")
	}
	return out[:int(n)], nil
}

// Close releases the decoder.
func (d *Decoder) Close() {
	if d.h != nil {
		C.spxdec_free(d.h)
		d.h = nil
	}
}
