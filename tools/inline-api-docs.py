#!/usr/bin/env python3
"""Make a Redocly build-docs page self-contained.

build-docs emits an HTML file that still pulls redoc.standalone.js from a CDN,
so the page needs the network every time it is opened. This fetches that script
once and inlines it, which is the property the page is for: an API explorer you
can read on a plane, from a checkout, with no server running.

Idempotent. If the download fails, the page is left as it was and the CDN
reference still works - the failure is reported and never silent.

Usage: inline-api-docs.py <html file>
"""

import re
import sys
import urllib.request

SCRIPT = re.compile(r'<script\s+src="(https://[^"]+\.js)"[^>]*>\s*</script>')


def main(path: str) -> int:
    with open(path, encoding="utf-8") as fh:
        html = fh.read()

    m = SCRIPT.search(html)
    if not m:
        print(f"{path}: already self-contained")
        return 0

    url = m.group(1)
    try:
        with urllib.request.urlopen(url, timeout=60) as resp:
            js = resp.read().decode("utf-8")
    except Exception as err:  # network, TLS, DNS, 404 - all the same to us
        print(f"{path}: could not inline {url}: {err}", file=sys.stderr)
        print("the page still works, but needs the network to open", file=sys.stderr)
        return 0

    # </script> inside the payload would close our tag early. It cannot appear
    # in valid JS outside a string, and escaping it keeps the string identical.
    js = js.replace("</script>", "<\\/script>")
    html = html[: m.start()] + "<script>" + js + "</script>" + html[m.end():]

    with open(path, "w", encoding="utf-8") as fh:
        fh.write(html)
    print(f"{path}: inlined {len(js) // 1024} KiB from {url}")
    return 0


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    sys.exit(main(sys.argv[1]))
