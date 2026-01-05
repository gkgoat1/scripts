#!/bin/sh
ls | go run $(dirname $0)/../forfiles/forfiles.go '^' sh -c 'cd ^; cargo update; git add -A; git commit -m update;'
