# Sail runtime fork

These Dockerfiles, php.ini, and supervisord.conf files are forked from
[laravel/sail](https://github.com/laravel/sail).

## Upstream

- **Source:** `vendor/laravel/sail/runtimes/<version>/` at tag `v1.64.0` (commit `e8f64580340b09f15e37c961b2faf7811a1205b6`)
- **Fetched:** 2026-07-27
- **Maintainer:** https://github.com/laravel/sail

## Modifications

None at v1 cut-off. The fork is byte-identical to upstream. Future diffs
must be listed here with rationale.

## Sync procedure

```bash
git clone --depth=1 --branch=<new-tag> https://github.com/laravel/sail /tmp/sail
diff -ruN internal/stack/laravel/runtimes/<v>/ /tmp/sail/vendor/laravel/sail/runtimes/<v>/
# Apply non-trivial changes by hand. Update the header comment and this file.
```
