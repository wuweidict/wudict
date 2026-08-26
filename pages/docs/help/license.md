---
title: License
description: WuWeiDict is free software under the GPL, version 3 or later, and this is what it includes from other projects.
---

# License

**WuWeiDict** is free software. You may redistribute it and change it under the
terms of the **GNU General Public License**, as published by the Free Software
Foundation, either version 3 of the license or, at your option, any later
version.

This program is distributed in the hope that it is useful, but **with no
warranty**, and without even the implied warranty of merchantability or fitness
for a particular purpose.

The full text is in the repository at `LICENSE`, and at the
[Free Software Foundation](https://www.gnu.org/licenses/gpl-3.0.html).

## What WuWeiDict includes

| Component | Origin                                                                                                                                                                     | License |
| --- |----------------------------------------------------------------------------------------------------------------------------------------------------------------------------| --- |
| MDX and MDD parser | [go-mdict](https://github.com/terasum/go-mdict), forked and patched as `internal/gomdict`                                                                                  | GPL-3.0 |
| BGL parser | [pyglossary](https://github.com/ilius/pyglossary)'s `babylon_bgl` plugin, ported to Go; streaming modelled on [GoldenDict NG](https://github.com/xiaoyifang/goldendict-ng) | GPL-3.0 |
| Format knowledge | the original reverse engineering by **Raul Fernandes** and **Karl Grill**, which both parsers trace back to                                                                | - |
| mark.js | [markjs.io](https://markjs.io/)                                                                                                                                            | MIT |
| libspeex | [Speex](https://speex.org/), decoder only, vendored at `internal/speex/clib`                                                                                               | BSD-3-Clause |
