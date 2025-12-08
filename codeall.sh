#!/bin/sh
ls | xargs -I '^' sh -c "cd ^; code ."
