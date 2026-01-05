ls | go run $(dirname $0)/../forfiles/forfiles.go '^' sh -c 'cd ^; (cat .gitignore || true) | sort | uniq > .gitignore.2; mv .gitignore.2 .gitignore'
