/* Portable floating-point build of the vendored libspeex decode subset.
 *
 * Hand-written replacement for the autotools-generated config.h: no SIMD
 * (works identically on amd64/arm64/all), floating-point arithmetic, the
 * self-contained smallft FFT, and C99 variable-length arrays for scratch
 * buffers (portable across gcc/clang; avoids alloca.h). gonow-dict compiles
 * these .c files directly via cgo — see ../ (backend_cgo.go). */

#ifndef GONOW_SPEEX_CONFIG_H
#define GONOW_SPEEX_CONFIG_H

/* Floating point, no fixed-point/SIMD paths. */
#ifndef FIXED_POINT
#define FLOATING_POINT
#endif
#define USE_SMALLFT

/* Scratch allocation: C99 VLAs (portable, no alloca). */
#define VAR_ARRAYS

/* No symbol-visibility annotations needed for a static in-tree build. */
#define EXPORT

/* Standard C headers we rely on. */
#define HAVE_STDINT_H 1
#define HAVE_STRING_H 1

#endif /* GONOW_SPEEX_CONFIG_H */
