# Third-party notices

WuWeiDict (`wudict`) is licensed GPL-3.0-or-later; see [LICENSE](LICENSE). It
also contains, or links, the third-party software listed here. Each component
keeps its own licence, reproduced in full below — several of them require that,
in binary distributions as well as source.

`wudict licenses` prints this file from inside the binary, so a release carries
its notices even when it is a single executable with nothing beside it.

Regenerate with `make notices` after changing dependencies.

## Code included in this repository

### Speex — `internal/speex/clib`

The reference Speex decoder, vendored so `.spx` pronunciation audio plays
without an external tool. Copyright (C) 2002-2009 Jean-Marc Valin, the Xiph.Org
Foundation, David Rowe and Analog Devices Inc.

```
Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions
are met:

- Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.

- Redistributions in binary form must reproduce the above copyright
notice, this list of conditions and the following disclaimer in the
documentation and/or other materials provided with the distribution.

- Neither the name of the Xiph.org Foundation nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
``AS IS'' AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED.  IN NO EVENT SHALL THE FOUNDATION OR
CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

### kiss_fft — `internal/speex/clib/_kiss_fft_guts.h`

The FFT Speex uses. Copyright (c) 2003-2004, Mark Borgerding. All rights
reserved.

```
Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

    * Redistributions of source code must retain the above copyright notice,
      this list of conditions and the following disclaimer.
    * Redistributions in binary form must reproduce the above copyright notice,
      this list of conditions and the following disclaimer in the documentation
      and/or other materials provided with the distribution.
    * Neither the author nor the names of any contributors may be used to
      endorse or promote products derived from this software without specific
      prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND
ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE LIABLE FOR
ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES
(INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES;
LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON
ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

### medict — `internal/gomdict`

The MDX/MDD parser is derived from [medict](https://github.com/terasum/medict)
(`internal/libs/go-mdict`), Copyright (C) 2023 Quan Chen
<chenquan_act@163.com>, GPL-3.0-or-later. Modified in 2026 for wudict: MDX v3
support, indexed record access, and a logging shim in place of the go-logging
dependency. Per-file notices name what changed.

### pyglossary — `internal/format/bgl`

The Babylon BGL reader is ported from
[pyglossary](https://github.com/ilius/pyglossary)'s `babylon_bgl` plugin,
Copyright (C) 2008-2021 Saeed Rasooli (ilius) and Copyright (C) 2011-2012
kubtek, GPL-3.0-or-later, which credits the reverse engineering of the format
to Raul Fernandes and Karl Grill.

### SQLite

Both SQLite drivers embed SQLite itself, which is in the public domain: its
authors "have dedicated all copyright to the public domain". See
<https://sqlite.org/copyright.html>.

## Go modules linked into the binaries

The union over every shipped build configuration — the cgo and pure-Go SQLite
drivers, and each supported operating system — so nothing is missing from a
release that happened to be built with other tags.
