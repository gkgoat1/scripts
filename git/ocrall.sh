#!/bin/sh
ls | xargs -I {} ocrmypdf --skip-text {} {}

