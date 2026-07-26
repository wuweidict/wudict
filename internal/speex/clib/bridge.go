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
#include "speex/speex.h"

typedef struct {
	void*     state;
	SpeexBits bits;
	int       frame_size;
} spxdec_t;

// spxdec_new initializes a mono decoder for mode 0 (narrow), 1 (wide) or
// 2 (ultra-wide). Perceptual enhancement is enabled to match the speexdec CLI.
static spxdec_t* spxdec_new(int mode_id) {
	const SpeexMode* mode = speex_lib_get_mode(mode_id);
	if (!mode) return NULL;
	void* st = speex_decoder_init(mode);
	if (!st) return NULL;
	spxdec_t* d = (spxdec_t*)malloc(sizeof(spxdec_t));
	if (!d) { speex_decoder_destroy(st); return NULL; }
	d->state = st;
	speex_bits_init(&d->bits);
	spx_int32_t fs = 0;
	speex_decoder_ctl(st, SPEEX_GET_FRAME_SIZE, &fs);
	d->frame_size = (int)fs;
	spx_int32_t enh = 1;
	speex_decoder_ctl(st, SPEEX_SET_ENH, &enh);
	return d;
}

static int spxdec_frame_size(spxdec_t* d) { return d->frame_size; }

static void spxdec_free(spxdec_t* d) {
	if (!d) return;
	speex_decoder_destroy(d->state);
	speex_bits_destroy(&d->bits);
	free(d);
}

// spxdec_decode decodes up to `frames` frames from one packet into `out`
// (frames*frame_size int16 samples). Returns samples written, or -2 on a
// corrupt frame.
static int spxdec_decode(spxdec_t* d, char* packet, int len, int frames, short* out) {
	speex_bits_read_from(&d->bits, packet, len);
	int n = 0;
	for (int i = 0; i < frames; i++) {
		int ret = speex_decode_int(d->state, &d->bits, (spx_int16_t*)(out + n));
		if (ret == -1) break;   // no more data in this packet
		if (ret == -2) return -2; // corrupt frame
		n += d->frame_size;
	}
	return n;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Decoder is a mono libspeex decoder for one mode.
type Decoder struct {
	h *C.spxdec_t
}

// New creates a decoder for mode 0 (narrow, 8k), 1 (wide, 16k) or 2 (ultra-wide, 32k).
func New(mode int) (*Decoder, error) {
	h := C.spxdec_new(C.int(mode))
	if h == nil {
		return nil, fmt.Errorf("speex: unsupported mode %d", mode)
	}
	return &Decoder{h: h}, nil
}

// FrameSize is the number of samples produced per decoded frame.
func (d *Decoder) FrameSize() int { return int(C.spxdec_frame_size(d.h)) }

// DecodePacket decodes `frames` frames from one Ogg-Speex audio packet,
// returning frames*FrameSize PCM samples (16-bit, host order).
func (d *Decoder) DecodePacket(packet []byte, frames int) ([]int16, error) {
	if frames <= 0 {
		frames = 1
	}
	out := make([]int16, frames*d.FrameSize())
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
