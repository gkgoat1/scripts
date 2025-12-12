#!/bin/sh
ls | xargs -I '^' sh -c 'cd ^; cargo update; git add -A; git commit -m update;'
