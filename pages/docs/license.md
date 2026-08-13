# WuWeiDict — license

**WuWeiDict** is free software: you may redistribute it and modify it
under the terms of the **GNU General Public License as published by the
Free Software Foundation, either version 3 of the License, or (at your
option) any later version.**

This program is distributed in the hope that it will be useful, but
**without any warranty**; without even the implied warranty of
merchantability or fitness for a particular purpose. See the
[GNU General Public License](https://www.gnu.org/licenses/gpl-3.0.html)
for the full text.

## Bundled works — attribution

WuWeiDict includes and derives from several open-source projects, each
with its own license:

| Component        | Origin                                                                                                                                                                    | License      |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------ |
| MDX/MDD parser   | [go-mdict](https://github.com/terasum/go-mdict) (forked as `internal/gomdict`)                                                                                            | GPL-3.0      |
| BGL parser       | [pyglossary](https://github.com/ilius/pyglossary) — `babylon_bgl` plugin, ported to Go; streaming modeled on [GoldenDict NG](https://github.com/xiaoyifang/goldendict-ng) | GPL-3.0      |
| Format knowledge | Both trace to the original reverse engineering by **Raul Fernandes** and **Karl Grill**                                                                                   | —            |
| mark.js          | [markjs.io](https://markjs.io/)                                                                                                                                           | MIT          |
| libspeex         | [Speex](https://speex.org/) — in-process decoder only, vendored at `internal/speex/clib`                                                                                  | BSD-3-Clause |

The full text of the GPL-3.0 license is available in the repository at
`LICENSE` and at the [Free Software Foundation](https://www.gnu.org/licenses/gpl-3.0.html).