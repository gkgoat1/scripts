#!/bin/sh
ls | xargs -I '^' sh -c "cd ^; codium ."
