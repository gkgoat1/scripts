#!/bin/sh
ls | xargs -I '^' sh -c 'cd ^; git add -A; git commit -m update; git push'
