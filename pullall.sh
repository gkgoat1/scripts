#!/bin/sh
ls | xargs -I '^' sh -c "cd ^; git pull --no-rebase"
