/* Hand-written replacement for the autotools-generated speex_config_types.h
 * (normally produced from speex_config_types.h.in). Uses <stdint.h>, portable
 * across all platforms gonow-dict targets. */
#ifndef __SPEEX_TYPES_H__
#define __SPEEX_TYPES_H__

#include <stdint.h>

typedef int16_t spx_int16_t;
typedef uint16_t spx_uint16_t;
typedef int32_t spx_int32_t;
typedef uint32_t spx_uint32_t;

#endif
