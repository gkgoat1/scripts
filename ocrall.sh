#!/bin/sh
ls | xargs -i ocrmypdf --skip-text {} {}

