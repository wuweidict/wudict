# Convenience wrapper for working inside pages/ only.
#
# The real targets live in the repository root Makefile (D10) and are the ones
# CI runs: `make docs` renders the API explorer into docs/api/ first, so it
# builds the whole site. These two build only the prose.
.PHONY: build serve

build:
	zensical build --strict

serve:
	zensical serve
