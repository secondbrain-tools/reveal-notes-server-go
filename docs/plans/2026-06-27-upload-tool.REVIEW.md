Issues:
- `Makefile` advertises `make build-upload`, but no such target exists, so the documented command fails.
- `internal/uploader/archive.go` prunes ignored directories during the walk, so negated patterns under an ignored parent (for example `dist/` + `!dist/keep.map`) never get a chance to re-include files.
