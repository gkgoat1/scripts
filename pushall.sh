#!/bin/sh
ls | xargs -I '^' sh -c "cd ^; git push"
