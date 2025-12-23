ls | xargs -I '^' sh -c 'cd ^;echo >> .gitignore; echo target >> .gitignore; echo node_modules >> .gitignore; echo .DS_Store >> .gitignore'
sh $(dirname $0)/sortallignores.sh
