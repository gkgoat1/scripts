#!/bin/sh
ls | go run $(dirname $0)/../forfiles/forfiles.go '^' sh -c 'cd ^; git add -A; git commit -m update; git push'
