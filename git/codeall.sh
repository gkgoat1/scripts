#!/bin/sh
ls | go run $(dirname $0)/../forfiles/forfiles.go '^' sh -c "cd ^; code ."
